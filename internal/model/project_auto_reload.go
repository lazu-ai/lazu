package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProjectAutoReload is a project's auto-reload: when the owner's balance
// falls under ThresholdUSD, the worker charges the saved card AmountUSD, at
// most MonthlyCapUSD per calendar month (UTC). Off by default; only the
// project owner turns it on, and only their card is charged.
//
// The card is saved through a Stripe Checkout Session in setup mode. The
// session is recorded before it is opened so a completion that arrives late
// (webhook, or the confirm call on return) still finds its row.
type ProjectAutoReload struct {
	ID            int   `json:"-" gorm:"primaryKey"`
	ProjectID     int   `json:"-" gorm:"not null;uniqueIndex"`
	Enabled       bool  `json:"enabled" gorm:"not null;default:false"`
	ThresholdUSD  int64 `json:"threshold_usd" gorm:"not null;default:0"`
	AmountUSD     int64 `json:"amount_usd" gorm:"not null;default:0"`
	MonthlyCapUSD int64 `json:"monthly_cap_usd" gorm:"not null;default:0"`

	StripePaymentMethodID string `json:"-" gorm:"size:255"`
	CardBrand             string `json:"-" gorm:"size:32"`
	CardLast4             string `json:"-" gorm:"size:4"`

	SetupSessionID string `json:"-" gorm:"size:255;index"`
	SetupStartedAt int64  `json:"-"`

	// PausedReason is empty while reloads run. card_declined waits for a new
	// card or a manual re-enable; cap_reached clears itself when a reload
	// fits under the cap again, which is the next month at the latest.
	PausedReason string `json:"paused_reason" gorm:"size:32;not null;default:''"`
	PausedAt     int64  `json:"paused_at"`
	LastFailure  string `json:"last_failure" gorm:"size:191"`
	LastFailedAt int64  `json:"last_failed_at"`
	LastRunAt    int64  `json:"last_run_at"`
	LastChargeAt int64  `json:"last_charge_at"`
	// ClaimedAt is the worker's lease: a row claimed in the last few minutes
	// is skipped by every other replica.
	ClaimedAt int64 `json:"-" gorm:"index"`
	CreatedAt int64 `json:"-" gorm:"autoCreateTime"`
	UpdatedAt int64 `json:"-" gorm:"autoUpdateTime"`
}

const (
	AutoReloadPauseCardDeclined = "card_declined"
	AutoReloadPauseCapReached   = "cap_reached"

	// AutoReloadPaymentMethod marks an order the worker opened; the card label
	// replaces it once the charge is known. The typed auto_reload column on
	// top_ups, not this string, is what the monthly cap sums.
	AutoReloadPaymentMethod = "auto_reload"

	// autoReloadClaimLease is how long a claimed row is left alone. The worker
	// runs every five minutes, so a crash mid-run costs at most one cycle.
	autoReloadClaimLease = 4 * time.Minute
)

// ErrAutoReloadInFlight says a reload for this wallet is already pending.
var ErrAutoReloadInFlight = errors.New("an auto-reload is already in flight for this project")

// CardLabel is the customer-visible card, e.g. "Visa ···· 4242".
func (r ProjectAutoReload) CardLabel() string {
	if r.StripePaymentMethodID == "" {
		return ""
	}
	return ProjectPaymentMethod("card", r.CardBrand, r.CardLast4)
}

// ThresholdMicrousd is the balance under which a reload starts.
func (r ProjectAutoReload) ThresholdMicrousd() billing.MicroUSD {
	return billing.MicroUSD(r.ThresholdUSD * billing.MicroUSDPerUSD)
}

// ProjectAutoReloadSettings are the three numbers the owner sets.
type ProjectAutoReloadSettings struct {
	ThresholdUSD  int64
	AmountUSD     int64
	MonthlyCapUSD int64
}

// autoReloadMonthlyCapCeiling keeps the cap a number a person meant.
const autoReloadMonthlyCapCeiling = 100 * setting.StripeMaxTopUp

// Validate checks the three numbers against the deployment's top-up range.
// The reload amount is a card top-up, so it obeys the operator's floor.
func (s ProjectAutoReloadSettings) Validate() error {
	minTopUp := int64(setting.NormalizeMinTopUp(setting.StripeMinTopUp))
	maxTopUp := int64(setting.StripeMaxTopUp)
	invalid := func(field string) error {
		return types.Err(types.ErrorCodeAutoReloadSettingsInvalid, types.D{"field": field, "min_amount": minTopUp, "max_amount": maxTopUp})
	}
	if s.ThresholdUSD < 1 || s.ThresholdUSD > maxTopUp {
		return invalid("threshold_usd")
	}
	if s.AmountUSD < minTopUp || s.AmountUSD > maxTopUp {
		return invalid("amount_usd")
	}
	if s.MonthlyCapUSD < s.AmountUSD || s.MonthlyCapUSD > autoReloadMonthlyCapCeiling {
		return invalid("monthly_cap_usd")
	}
	return nil
}

// ClampAutoReloadAmount is the amount the worker charges: the owner's
// number raised to the floor the operator has set since, never above the
// ceiling. A clamp is recorded on the order, so the receipt says what was
// charged.
func ClampAutoReloadAmount(amount int64) int64 {
	minTopUp := int64(setting.NormalizeMinTopUp(setting.StripeMinTopUp))
	if amount < minTopUp {
		return minTopUp
	}
	if amount > setting.StripeMaxTopUp {
		return setting.StripeMaxTopUp
	}
	return amount
}

// GetProjectAutoReload reads a project's row. found is false when the owner
// has never touched auto-reload; the console draws nothing as a fact then.
func GetProjectAutoReload(projectID int) (ProjectAutoReload, bool, error) {
	var row ProjectAutoReload
	err := CoreDB().Where("project_id = ?", projectID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ProjectAutoReload{ProjectID: projectID}, false, nil
	}
	return row, err == nil, err
}

func lockProjectAutoReload(tx *gorm.DB, projectID int) (*ProjectAutoReload, error) {
	var row ProjectAutoReload
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("project_id = ?", projectID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = ProjectAutoReload{ProjectID: projectID}
		if err := tx.Create(&row).Error; err != nil {
			return nil, err
		}
		return &row, nil
	}
	return &row, err
}

// SaveProjectAutoReloadSettings stores the three numbers and, when asked,
// turns reloads on or off. Turning on needs a saved card; it also clears a
// card_declined pause, since the owner has looked at the state and chosen
// to continue with the card on file.
func SaveProjectAutoReloadSettings(projectID int, settings ProjectAutoReloadSettings, enabled *bool) (ProjectAutoReload, error) {
	if err := settings.Validate(); err != nil {
		return ProjectAutoReload{}, err
	}
	var result ProjectAutoReload
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		row, err := lockProjectAutoReload(tx, projectID)
		if err != nil {
			return err
		}
		updates := map[string]interface{}{"threshold_usd": settings.ThresholdUSD, "amount_usd": settings.AmountUSD, "monthly_cap_usd": settings.MonthlyCapUSD}
		if enabled != nil {
			if *enabled && row.StripePaymentMethodID == "" {
				return types.Err(types.ErrorCodeAutoReloadCardRequired)
			}
			updates["enabled"] = *enabled
			if *enabled && row.PausedReason == AutoReloadPauseCardDeclined {
				updates["paused_reason"], updates["paused_at"] = "", 0
			}
			if !*enabled {
				updates["paused_reason"], updates["paused_at"] = "", 0
			}
		}
		if err := tx.Model(row).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("project_id = ?", projectID).First(&result).Error
	})
	return result, err
}

// BeginProjectAutoReloadSetup records the numbers and the moment a card is
// about to be saved, before Stripe is contacted. The settings are applied
// when the card lands; reloads stay off until then.
func BeginProjectAutoReloadSetup(projectID int, settings ProjectAutoReloadSettings, now time.Time) (ProjectAutoReload, error) {
	if err := settings.Validate(); err != nil {
		return ProjectAutoReload{}, err
	}
	var result ProjectAutoReload
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		row, err := lockProjectAutoReload(tx, projectID)
		if err != nil {
			return err
		}
		updates := map[string]interface{}{"threshold_usd": settings.ThresholdUSD, "amount_usd": settings.AmountUSD, "monthly_cap_usd": settings.MonthlyCapUSD,
			"setup_session_id": "", "setup_started_at": now.Unix()}
		if err := tx.Model(row).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("project_id = ?", projectID).First(&result).Error
	})
	return result, err
}

// AttachProjectAutoReloadSetupSession remembers the Checkout Session that
// is collecting the card.
func AttachProjectAutoReloadSetupSession(projectID int, sessionID string) error {
	if sessionID == "" {
		return ErrProjectInvalidInput
	}
	return CoreDB().Model(&ProjectAutoReload{}).Where("project_id = ?", projectID).Update("setup_session_id", sessionID).Error
}

// ClearProjectAutoReloadSetup forgets a card save that will not complete:
// the session expired on Stripe, or the owner never came back within the
// window a Checkout Session lives. Nothing else changes.
func ClearProjectAutoReloadSetup(projectID int) error {
	return CoreDB().Model(&ProjectAutoReload{}).Where("project_id = ?", projectID).Updates(map[string]interface{}{"setup_session_id": "", "setup_started_at": 0}).Error
}

// ProjectAutoReloadCard is what a completed setup session hands back.
type ProjectAutoReloadCard struct {
	ProjectID       int
	CustomerID      string
	SessionID       string
	PaymentMethodID string
	Brand           string
	Last4           string
}

// CompleteProjectAutoReloadSetup stores the saved card and turns reloads on.
// A card_declined pause ends here: the owner changed the card. A completion
// for a session this row is not waiting for is ignored (false, nil): a
// stale tab finishing after a newer card was saved must not replace it.
func CompleteProjectAutoReloadSetup(card ProjectAutoReloadCard) (ProjectAutoReload, bool, error) {
	if card.ProjectID <= 0 || card.CustomerID == "" || card.PaymentMethodID == "" {
		return ProjectAutoReload{}, false, ErrProjectInvalidInput
	}
	var result ProjectAutoReload
	applied := false
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.First(&project, card.ProjectID).Error; err != nil {
			return err
		}
		if project.StripeCustomerID != card.CustomerID {
			return ErrProjectStateConflict
		}
		row, err := lockProjectAutoReload(tx, card.ProjectID)
		if err != nil {
			return err
		}
		if row.StripePaymentMethodID == card.PaymentMethodID {
			result = *row
			return nil
		}
		if row.SetupSessionID != "" && card.SessionID != "" && row.SetupSessionID != card.SessionID {
			result = *row
			return nil
		}
		updates := map[string]interface{}{"stripe_payment_method_id": card.PaymentMethodID, "card_brand": card.Brand, "card_last4": card.Last4,
			"enabled": true, "setup_session_id": "", "setup_started_at": 0, "last_failure": ""}
		if row.PausedReason == AutoReloadPauseCardDeclined {
			updates["paused_reason"], updates["paused_at"] = "", 0
		}
		if err := tx.Model(row).Updates(updates).Error; err != nil {
			return err
		}
		applied = true
		return tx.Where("project_id = ?", card.ProjectID).First(&result).Error
	})
	return result, applied, err
}

// DisableProjectAutoReload turns reloads off. The card stays on file so the
// owner can turn them back on without another trip to Stripe.
func DisableProjectAutoReload(projectID int) (ProjectAutoReload, error) {
	var result ProjectAutoReload
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		row, err := lockProjectAutoReload(tx, projectID)
		if err != nil {
			return err
		}
		if err := tx.Model(row).Updates(map[string]interface{}{"enabled": false, "paused_reason": "", "paused_at": 0, "setup_session_id": "", "setup_started_at": 0}).Error; err != nil {
			return err
		}
		return tx.Where("project_id = ?", projectID).First(&result).Error
	})
	return result, err
}

// SetProjectAutoReloadPause records why reloads stopped, or clears it.
func SetProjectAutoReloadPause(projectID int, reason string, now time.Time) error {
	updates := map[string]interface{}{"paused_reason": reason, "paused_at": now.Unix()}
	if reason == "" {
		updates["paused_at"] = 0
	}
	return CoreDB().Model(&ProjectAutoReload{}).Where("project_id = ?", projectID).Updates(updates).Error
}

// ClaimProjectAutoReloadDue leases the rows the worker should look at: on,
// with a card, and not paused by a declined card. cap_reached rows are
// included so the cap can be re-read; the worker resumes them itself.
func ClaimProjectAutoReloadDue(now time.Time, limit int) ([]ProjectAutoReload, error) {
	if limit <= 0 || limit > 500 {
		return nil, ErrProjectInvalidInput
	}
	var rows []ProjectAutoReload
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		query := tx.Where("enabled = ? AND stripe_payment_method_id <> '' AND paused_reason IN ? AND claimed_at <= ?", true, []string{"", AutoReloadPauseCapReached}, now.Add(-autoReloadClaimLease).Unix()).
			Order("last_run_at ASC, id ASC").Limit(limit)
		if tx.Dialector.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]int, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		return tx.Model(&ProjectAutoReload{}).Where("id IN ?", ids).Updates(map[string]interface{}{"claimed_at": now.Unix(), "last_run_at": now.Unix()}).Error
	})
	return rows, err
}

// ProjectAutoReloadMonthSpentUSD is what auto-reload has charged this
// calendar month (UTC), in whole USD. Auto-reload orders are whole dollars,
// so the order amount is the charge.
func ProjectAutoReloadMonthSpentUSD(projectID int, now time.Time) (int64, error) {
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	var spent int64
	err := CoreDB().Model(&TopUp{}).Where("project_id = ? AND auto_reload = ? AND status = ? AND complete_time >= ?", projectID, true, common.TopUpStatusSuccess, start).
		Select("COALESCE(SUM(amount), 0)").Scan(&spent).Error
	return spent, err
}

// FindPendingProjectAutoReloadTopUp is the one order that may be in flight.
func FindPendingProjectAutoReloadTopUp(projectID int) (TopUp, bool, error) {
	var order TopUp
	err := CoreDB().Where("project_id = ? AND auto_reload = ? AND status = ?", projectID, true, common.TopUpStatusPending).Order("id DESC").First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return order, false, nil
	}
	return order, err == nil, err
}

// BeginProjectAutoReloadTopUp opens the pending order the worker is about
// to charge. The order is durable before Stripe is contacted, and only one
// auto-reload order per project is pending at a time: a second attempt while
// one is in flight returns ErrAutoReloadInFlight.
func BeginProjectAutoReloadTopUp(projectID int, amount int64, returnBase string, now time.Time) (TopUp, error) {
	var order TopUp
	if amount <= 0 || amount > setting.StripeMaxTopUp {
		return order, ErrProjectInvalidInput
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		if project.StripeCustomerID == "" {
			return ErrProjectStateConflict
		}
		var pending int64
		if err := tx.Model(&TopUp{}).Where("project_id = ? AND auto_reload = ? AND status = ?", projectID, true, common.TopUpStatusPending).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return ErrAutoReloadInFlight
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("project-auto-reload:%d:%d", projectID, now.UnixNano())))
		shared := project.UpgradedAt != nil
		order = TopUp{StripeProjectName: project.Name, StripeProjectShared: &shared, ProjectID: projectID, PaidByUserID: project.OwnerUserID, UserId: project.OwnerUserID, StripeCustomerID: project.StripeCustomerID,
			Amount: amount, PaidAmount: float64(amount), CreditedMicrousd: billing.MicroUSD(amount * billing.MicroUSDPerUSD),
			TradeNo: "ref_" + hex.EncodeToString(digest[:]), PaymentMethod: AutoReloadPaymentMethod, AutoReload: true,
			Status: common.TopUpStatusPending, InvoiceStatus: "legacy", StripeReturnBase: strings.TrimRight(returnBase, "/"), CreateTime: now.Unix()}
		return tx.Create(&order).Error
	})
	return order, err
}

// RecordProjectAutoReloadIntent ties the PaymentIntent to its order as soon
// as Stripe names it, so a webhook can find the order even when the worker
// died before settling.
func RecordProjectAutoReloadIntent(orderID int, intentID string) error {
	if intentID == "" {
		return ErrProjectInvalidInput
	}
	return CoreDB().Model(&TopUp{}).Where("id = ? AND (stripe_payment_intent_id = '' OR stripe_payment_intent_id = ?)", orderID, intentID).Update("stripe_payment_intent_id", intentID).Error
}

// FailProjectAutoReloadTopUp closes a declined reload: the order is failed,
// reloads pause until the owner changes the card, and the owner is told
// once. Worker and webhook may both arrive here; the second finds the order
// already failed and does nothing.
func FailProjectAutoReloadTopUp(reference, code, message string, now time.Time) (bool, error) {
	failed := false
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", reference).First(&order).Error; err != nil {
			return err
		}
		if !order.AutoReload || order.Status != common.TopUpStatusPending {
			return nil
		}
		if err := tx.Model(&order).Update("status", "failed").Error; err != nil {
			return err
		}
		row, err := lockProjectAutoReload(tx, order.ProjectID)
		if err != nil {
			return err
		}
		detail := strings.TrimSpace(code)
		if detail == "" {
			detail = "card_declined"
		}
		if err := tx.Model(row).Updates(map[string]interface{}{"paused_reason": AutoReloadPauseCardDeclined, "paused_at": now.Unix(), "last_failure": detail, "last_failed_at": now.Unix()}).Error; err != nil {
			return err
		}
		var project Project
		if err := tx.First(&project, order.ProjectID).Error; err != nil {
			return err
		}
		var owner User
		if err := tx.Unscoped().Select("id", "email", "setting", "home_surface", "quota").First(&owner, project.OwnerUserID).Error; err != nil {
			return err
		}
		if owner.Email == "" {
			failed = true
			return nil
		}
		email, err := renderAutoReloadFailedEmail(owner, project, *row, order, detail, message, now)
		if err != nil {
			return err
		}
		failed = true
		return queueProjectEmailAtTx(tx, project.ID, owner.Id, fmt.Sprintf("auto-reload-failed:%s:%d", order.PublicID, owner.Id), email, now.Unix())
	})
	return failed, err
}

// autoReloadWalletURL opens the owner's wallet on the auto-reload block.
func autoReloadWalletURL(owner User, project Project, action string) string {
	path := UserSurfacePath(owner.HomeSurface, SurfaceLinkWallet)
	if NormalizeUserHomeSurface(owner.HomeSurface) != "" {
		// Another surface's billing page has no project block to open.
		return common.MailSiteURL(path)
	}
	return common.MailSiteURL(path + "?project_id=" + url.QueryEscape(project.PublicID) + "&auto_reload=" + url.QueryEscape(action))
}

// renderAutoReloadFailedEmail is the one mail a declined reload sends: ink
// tier like the low-balance mail, the balance as the figure, the card and
// the bank's reason as facts, and the two things the owner can do.
func renderAutoReloadFailedEmail(owner User, project Project, row ProjectAutoReload, order TopUp, code, message string, now time.Time) (ProjectEmail, error) {
	lang := common.MailLang(owner.GetSetting().Language)
	balance := common.MailUSD(owner.Quota.Dollars())
	amount := common.MailUSD(float64(order.Amount))
	card := row.CardLabel()
	if card == "" {
		card = common.MailText(lang, "已保存的卡", "the saved card")
	}
	reason := autoReloadDeclineText(lang, code)
	subject := common.MailText(lang, "自动续费没有扣成功，已暂停", "Auto-reload could not charge your card and is paused")
	body := common.MailText(lang,
		card+" 被拒绝，这次 "+amount+" 的自动充值没有完成。自动续费已暂停，不会反复扣款；更换卡或手动充值后恢复。",
		"The "+amount+" auto-reload on "+card+" was declined. Auto-reload is paused and will not retry; change the card or top up by hand to continue.")
	facts := []common.MailFact{
		{Label: common.MailText(lang, "当前余额", "Balance"), Value: balance},
		{Label: common.MailText(lang, "余额低于", "Reloads under"), Value: common.MailUSD(float64(row.ThresholdUSD))},
		{Label: common.MailText(lang, "扣款的卡", "Card"), Value: card},
		{Label: common.MailText(lang, "银行的说法", "Bank's reason"), Value: reason},
	}
	_ = message
	html, err := common.RenderMail(common.Mail{
		Lang: lang, Tier: common.MailInk, Eyebrow: "AUTO-RELOAD PAUSED",
		Preheader: common.MailText(lang, card+" 被拒绝 · 余额 "+balance, card+" declined · balance "+balance),
		Hero:      balance, CompactHero: owner.Quota.Dollars() >= 1000 || owner.Quota.Dollars() <= -1000,
		Message: body,
		Blocks: []common.MailBlock{
			{Facts: facts},
			{Primary: &common.MailAction{Label: common.MailText(lang, "更换卡", "Change card"), URL: autoReloadWalletURL(owner, project, "card")},
				Secondary: &common.MailAction{Label: common.MailText(lang, "手动充 "+amount, "Top up "+amount), URL: autoReloadWalletURL(owner, project, "topup")}},
			{Text: common.MailText(lang, "时间 "+now.UTC().Format("2006-01-02 15:04 UTC"), "Time "+now.UTC().Format("2006-01-02 15:04 UTC")), Muted: true},
		},
	})
	if err != nil {
		return ProjectEmail{}, err
	}
	return ProjectEmail{Receiver: owner.Email, Subject: subject, HTML: html}, nil
}

// autoReloadDeclineText says Stripe's code in the reader's words.
func autoReloadDeclineText(lang, code string) string {
	switch strings.ToLower(code) {
	case "insufficient_funds":
		return common.MailText(lang, "余额不足", "Insufficient funds")
	case "expired_card":
		return common.MailText(lang, "卡已过期", "The card has expired")
	case "authentication_required":
		return common.MailText(lang, "需要持卡人验证（3-D Secure），离线扣款做不到", "The bank asked for cardholder authentication, which an unattended charge cannot do")
	case "incorrect_cvc", "incorrect_number", "invalid_card":
		return common.MailText(lang, "卡片信息有误", "The card details were rejected")
	case "lost_card", "stolen_card", "pickup_card":
		return common.MailText(lang, "卡被银行停用", "The bank has blocked this card")
	case "do_not_honor", "generic_decline", "card_declined", "":
		return common.MailText(lang, "银行拒绝了这次扣款", "The bank declined the charge")
	}
	return common.MailText(lang, "银行拒绝了这次扣款（"+code+"）", "The bank declined the charge ("+code+")")
}
