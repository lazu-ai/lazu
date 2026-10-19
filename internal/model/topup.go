package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TopUp struct {
	StripeProjectName       string           `json:"-" gorm:"size:191"`
	StripeProjectShared     *bool            `json:"-"`
	StripePriceID           string           `json:"-" gorm:"size:255"`
	StripeReturnBase        string           `json:"-" gorm:"type:text"`
	StripePromotionCodes    bool             `json:"-"`
	PublicID                string           `json:"public_id" gorm:"size:64;uniqueIndex"`
	StripeCheckoutURL       string           `json:"-" gorm:"type:text"`
	StripePaymentIntentID   string           `json:"-" gorm:"size:255;index"`
	StripeChargeID          string           `json:"-" gorm:"size:255;index"`
	StripeRefundedCents     int64            `json:"-" gorm:"not null;default:0"`
	ReversedCreditMicrousd  billing.MicroUSD `json:"-" gorm:"type:bigint;not null;default:0"`
	StripeDisputeID         string           `json:"-" gorm:"size:255;index"`
	StripeDisputeCents      int64            `json:"-" gorm:"not null;default:0"`
	StripeChargeCents       int64            `json:"-" gorm:"not null;default:0"`
	DisputeStatus           string           `json:"-" gorm:"size:16"`
	ProjectID               int              `json:"-" gorm:"index;not null;default:0;index:idx_top_ups_project_auto_reload,priority:1"`
	PaidByUserID            int              `json:"-" gorm:"index;not null;default:0"`
	StripeCustomerID        string           `json:"-" gorm:"size:64;index"`
	StripeCheckoutSessionID string           `json:"-" gorm:"size:255;index"`
	StripeInvoiceID         string           `json:"-" gorm:"size:255;index"`
	StripeInvoiceNumber     string           `json:"-" gorm:"size:255"`
	StripeInvoiceURL        string           `json:"-" gorm:"type:text"`
	StripeInvoicePDF        string           `json:"-" gorm:"type:text"`
	StripeReceiptURL        string           `json:"-" gorm:"type:text"`
	BillTo                  JSONValue        `json:"-" gorm:"type:json"`
	InvoiceStatus           string           `json:"-" gorm:"size:16;not null;default:'legacy'"`
	DocumentCheckedAt       int64            `json:"-" gorm:"index"`
	// Owner wallet balance right after this credit, the figure the payment
	// e-mail reports even when it is rendered later.
	BalanceAfterMicrousd *billing.MicroUSD `json:"-" gorm:"type:bigint"`
	// AutoReload marks an order the auto-reload worker opened and charged
	// off-session. The monthly cap sums this column; payment_method is the
	// card label and is rewritten by charge events.
	AutoReload bool `json:"-" gorm:"not null;default:false;index:idx_top_ups_project_auto_reload,priority:2"`

	Id                  int              `json:"id"`
	UserId              int              `json:"user_id" gorm:"index;index:idx_top_ups_user_status_complete,priority:1"`
	Amount              int64            `json:"amount"`
	TradeNo             string           `json:"trade_no" gorm:"unique;type:varchar(255)"`
	PaymentMethod       string           `json:"payment_method" gorm:"type:varchar(50)"`
	PaidAmount          float64          `json:"paid_amount" gorm:"type:decimal(20,10);default:0"`
	CreditedMicrousd    billing.MicroUSD `json:"credited_microusd" gorm:"type:bigint;default:0"`
	BonusMicrousd       billing.MicroUSD `json:"bonus_microusd" gorm:"type:bigint;default:0"`
	CreateTime          int64            `json:"create_time"`
	CompleteTime        int64            `json:"complete_time" gorm:"index:idx_top_ups_user_status_complete,priority:3"`
	Status              string           `json:"status" gorm:"index:idx_top_ups_user_status_complete,priority:2"`
	PendingNotifySentAt int64            `json:"-" gorm:"column:pending_notify_sent_at"`
}

const (
	topUpQueryWindowSeconds   int64 = 30 * 24 * 60 * 60
	searchTopUpCountHardLimit       = 10000
	topUpCreditedMicrousdSQL        = "CASE WHEN credited_microusd <> 0 THEN CASE WHEN credited_microusd - COALESCE(bonus_microusd, 0) > 0 THEN credited_microusd - COALESCE(bonus_microusd, 0) ELSE 0 END ELSE CAST(amount AS BIGINT) * 1000000 END"
)

func (topUp TopUp) PaidCreditedMicrousd() billing.MicroUSD {
	if topUp.CreditedMicrousd != 0 {
		paid := topUp.CreditedMicrousd - topUp.BonusMicrousd
		if paid > 0 {
			return paid
		}
		return 0
	}
	return USDToMicrousd(topUp.OrderUSD())
}

// OrderUSD is the order's amount in dollars. Every order is priced in whole
// dollars (amount); it is what the customer agreed to pay before any discount.
func (topUp TopUp) OrderUSD() float64 {
	return float64(topUp.Amount)
}

func topUpQueryCutoff() int64 {
	return common.GetTimestamp() - topUpQueryWindowSeconds
}

// GetUserTopupTotalMicrousd sums the credited amount of a single account's
// successful top-ups across all time. It powers the account drawer's
// lifetime top-up figure.
func GetUserTopupTotalMicrousd(userID int) (billing.MicroUSD, error) {
	var row struct {
		Total int64
	}
	err := CoreDB().Model(&TopUp{}).
		Select("coalesce(sum("+topUpCreditedMicrousdSQL+"), 0) as total").
		Where("user_id = ? AND status = ?", userID, common.TopUpStatusSuccess).
		Scan(&row).Error
	return billing.MicroUSD(row.Total), err
}

func sanitizeLikePattern(keyword string) (string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return "", types.Err(types.ErrorCodeTopupSearchKeywordInvalid)
	}
	if strings.Contains(keyword, "%%") {
		return "", types.Err(types.ErrorCodeTopupSearchKeywordInvalid)
	}
	if strings.Count(keyword, "%") > 4 {
		return "", types.Err(types.ErrorCodeTopupSearchKeywordInvalid)
	}

	stripped := strings.NewReplacer("%", "", "_", "", "!", "").Replace(keyword)
	if len([]rune(strings.TrimSpace(stripped))) < 2 {
		return "", types.Err(types.ErrorCodeTopupSearchKeywordInvalid)
	}

	escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(keyword)
	return "%" + escaped + "%", nil
}

func (topUp *TopUp) Insert() error {
	var err error
	err = CoreDB().Create(topUp).Error
	return err
}

func (topUp *TopUp) Update() error {
	var err error
	err = CoreDB().Save(topUp).Error
	return err
}

func (topUp *TopUp) NormalizeLedgerFields() {
	if topUp.PaidAmount == 0 && !(topUp.ProjectID > 0 && topUp.StripePromotionCodes && topUp.StripeCheckoutSessionID != "") {
		topUp.PaidAmount = float64(topUp.Amount)
	}
	if topUp.CreditedMicrousd == 0 {
		topUp.CreditedMicrousd = USDToMicrousd(topUp.OrderUSD())
	}
	paidMicrousd := USDToMicrousd(topUp.PaidAmount)
	if topUp.BonusMicrousd == 0 && topUp.CreditedMicrousd > paidMicrousd {
		topUp.BonusMicrousd = topUp.CreditedMicrousd - paidMicrousd
	}
}

func applyTopUpWalletCredit(tx *gorm.DB, topUp *TopUp) error {
	if topUp == nil {
		return nil
	}
	topUp.NormalizeLedgerFields()
	paidCreditMicrousd := topUp.CreditedMicrousd - topUp.BonusMicrousd
	if paidCreditMicrousd < 0 {
		return errors.New("invalid topup credit split")
	}
	if paidCreditMicrousd > 0 {
		if err := adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{
			UserID:         topUp.UserId,
			DeltaQuota:     MicrousdToQuota(paidCreditMicrousd),
			Kind:           WalletLedgerKindTopup,
			RefType:        "topup",
			RefID:          topUp.TradeNo + ":paid",
			PricingVersion: defaultPricingSeedVersion,
			Metadata:       topUpLedgerMetadata(topUp),
		}); err != nil {
			return err
		}
	}
	if topUp.BonusMicrousd > 0 {
		if err := adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{
			UserID:         topUp.UserId,
			DeltaQuota:     MicrousdToQuota(topUp.BonusMicrousd),
			Kind:           WalletLedgerKindBonus,
			RefType:        "topup",
			RefID:          topUp.TradeNo + ":bonus",
			PricingVersion: defaultPricingSeedVersion,
			Metadata:       topUpLedgerMetadata(topUp),
		}); err != nil {
			return err
		}
	}
	return nil
}

func completePendingTopUpTx(tx *gorm.DB, topUp *TopUp) error {
	if tx == nil || topUp == nil {
		return errors.New("complete top-up: no order")
	}
	topUp.NormalizeLedgerFields()
	now := common.GetTimestamp()
	update := tx.Model(&TopUp{}).
		Where("id = ? AND status = ?", topUp.Id, common.TopUpStatusPending).
		Updates(map[string]any{
			"complete_time":     now,
			"status":            common.TopUpStatusSuccess,
			"paid_amount":       topUp.PaidAmount,
			"credited_microusd": topUp.CreditedMicrousd,
			"bonus_microusd":    topUp.BonusMicrousd,
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected == 0 {
		return types.Err(types.ErrorCodeTopupOrderNotPending, types.D{"trade_no": topUp.TradeNo})
	}
	topUp.CompleteTime = now
	topUp.Status = common.TopUpStatusSuccess
	return nil
}

func markUserHasToppedUpTx(tx *gorm.DB, userID int) error {
	if userID <= 0 {
		return nil
	}
	return tx.Model(&User{}).Where("id = ?", userID).Updates(map[string]any{
		"has_topped_up":  true,
		"first_topup_at": gorm.Expr("CASE WHEN first_topup_at = 0 THEN ? ELSE first_topup_at END", common.GetTimestamp()),
	}).Error
}

func topUpLedgerMetadata(topUp *TopUp) map[string]any {
	return map[string]any{
		"trade_no":       topUp.TradeNo,
		"payment_method": topUp.PaymentMethod,
		"paid_amount":    topUp.PaidAmount,
	}
}

func GetTopUpById(id int) *TopUp {
	var topUp *TopUp
	var err error
	err = CoreDB().Where("id = ?", id).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
}

func GetTopUpByTradeNo(tradeNo string) *TopUp {
	var topUp *TopUp
	var err error
	err = CoreDB().Where("trade_no = ?", tradeNo).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
}

func Recharge(referenceId string, customerId string, callerIp string, callbackPaymentMethod string) (quotaAdded billing.MicroUSD, userId int, err error) {
	if referenceId == "" {
		return 0, 0, types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "client_reference_id"})
	}

	topUp := &TopUp{}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		err := tx.Where(refCol+" = ?", referenceId).First(topUp).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return types.Err(types.ErrorCodeTopupOrderNotFound, types.D{"trade_no": referenceId})
		}
		if err != nil {
			return err
		}

		if topUp.Status != common.TopUpStatusPending {
			return types.Err(types.ErrorCodeTopupOrderNotPending, types.D{"trade_no": referenceId})
		}

		if err = runTopUpCompletingHooks(tx, topUp); err != nil {
			return err
		}
		if err = completePendingTopUpTx(tx, topUp); err != nil {
			return err
		}

		quotaAdded = MicrousdToQuota(topUp.CreditedMicrousd)
		if customerId != "" {
			if topUp.ProjectID > 0 {
				var project Project
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, topUp.ProjectID).Error; err != nil {
					return err
				}
				if project.OwnerUserID != topUp.UserId || project.StripeCustomerID != "" && project.StripeCustomerID != customerId {
					return ErrProjectStateConflict
				}
				if err := tx.Model(&project).Update("stripe_customer_id", customerId).Error; err != nil {
					return err
				}
			}
		}
		if err = applyTopUpWalletCredit(tx, topUp); err != nil {
			return err
		}
		if err = markUserHasToppedUpTx(tx, topUp.UserId); err != nil {
			return err
		}
		userId = topUp.UserId

		return nil
	})

	if err != nil {
		return 0, 0, fmt.Errorf("recharge top-up %s: %w", referenceId, err)
	}
	if err := invalidateUserCache(topUp.UserId); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", topUp.UserId, err.Error()))
	}
	if err := InvalidateUserTokensCache(topUp.UserId); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate token/auth projection cache for user %d: %s", topUp.UserId, err.Error()))
	}

	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用在线充值成功，充值金额: %v，支付金额：%d", logger.FormatQuota(quotaAdded), topUp.Amount), callerIp, topUp.PaymentMethod, callbackPaymentMethod)
	return quotaAdded, topUp.UserId, nil
}

func GetUserTopUps(userId int, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	// Start transaction
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	cutoff := topUpQueryCutoff()

	// Get total count within transaction
	err = tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userId, cutoff).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated topups within same transaction
	err = tx.Where("user_id = ? AND create_time >= ?", userId, cutoff).Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Commit transaction
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return topups, total, nil
}

// GetAllTopUps 获取全平台的充值记录（管理员使用，不限制时间窗口）
func GetAllTopUps(pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err = tx.Model(&TopUp{}).Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return topups, total, nil
}

// SearchUserTopUps 按订单号搜索某用户的充值记录
func SearchUserTopUps(userId int, keyword string, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userId, topUpQueryCutoff())
	if keyword != "" {
		pattern, perr := sanitizeLikePattern(keyword)
		if perr != nil {
			tx.Rollback()
			return nil, 0, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}

	if err = query.Limit(searchTopUpCountHardLimit).Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, types.WrapErr(fmt.Errorf("count top-up search: %w", err), types.ErrorCodeInternalError)
	}

	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		return nil, 0, types.WrapErr(fmt.Errorf("search top-ups: %w", err), types.ErrorCodeInternalError)
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return topups, total, nil
}

// SearchAllTopUps searches all platform top-ups by trade number and/or an
// exact account owner. Admin account deep links use owner independently from
// the free-form trade-number keyword.
func SearchAllTopUps(keyword string, owner string, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := CoreDB().Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&TopUp{})
	if owner != "" {
		query = query.Where("user_id IN (?)", tx.Model(&User{}).
			Select("id").Where("username = ? OR public_id = ?", owner, owner))
	}
	if keyword != "" {
		pattern, perr := sanitizeLikePattern(keyword)
		if perr != nil {
			tx.Rollback()
			return nil, 0, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}

	if err = query.Limit(searchTopUpCountHardLimit).Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, types.WrapErr(fmt.Errorf("count top-up search: %w", err), types.ErrorCodeInternalError)
	}

	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		return nil, 0, types.WrapErr(fmt.Errorf("search top-ups: %w", err), types.ErrorCodeInternalError)
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return topups, total, nil
}

// ManualCompleteTopUp lets an administrator complete a pending Stripe order and credit the wallet.
func ManualCompleteTopUp(tradeNo string, callerIp string) (userId int, quotaAdded billing.MicroUSD, err error) {
	if tradeNo == "" {
		return 0, 0, types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "trade_no"})
	}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	var payMoney float64
	var paymentMethod string

	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		topUp := &TopUp{}
		if err := tx.Where(refCol+" = ?", tradeNo).First(topUp).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return types.Err(types.ErrorCodeTopupOrderNotFound, types.D{"trade_no": tradeNo})
		} else if err != nil {
			return err
		}

		// Idempotent path: return the existing successful credit.
		if topUp.Status == common.TopUpStatusSuccess {
			topUp.NormalizeLedgerFields()
			if err := markUserHasToppedUpTx(tx, topUp.UserId); err != nil {
				return err
			}
			userId = topUp.UserId
			quotaAdded = MicrousdToQuota(topUp.CreditedMicrousd)
			payMoney = topUp.OrderUSD()
			paymentMethod = topUp.PaymentMethod
			return nil
		}

		if topUp.Status != common.TopUpStatusPending {
			return types.Err(types.ErrorCodeTopupOrderNotPending, types.D{"trade_no": tradeNo})
		}

		if topUp.PaymentMethod != "stripe" {
			return types.Err(types.ErrorCodeTopupMethodUnsupported)
		}

		topUp.NormalizeLedgerFields()
		if topUp.CreditedMicrousd <= 0 {
			return types.Err(types.ErrorCodeTopupCreditInvalid)
		}
		if err := runTopUpCompletingHooks(tx, topUp); err != nil {
			return err
		}
		quotaAdded = MicrousdToQuota(topUp.CreditedMicrousd)

		if err := completePendingTopUpTx(tx, topUp); err != nil {
			return err
		}

		if err := applyTopUpWalletCredit(tx, topUp); err != nil {
			return err
		}
		if err := markUserHasToppedUpTx(tx, topUp.UserId); err != nil {
			return err
		}

		userId = topUp.UserId
		payMoney = topUp.OrderUSD()
		paymentMethod = topUp.PaymentMethod
		return nil
	})

	if err != nil {
		return 0, 0, err
	}
	if err := invalidateUserCache(userId); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate user cache for user %d: %s", userId, err.Error()))
	}
	if err := InvalidateUserTokensCache(userId); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate token/auth projection cache for user %d: %s", userId, err.Error()))
	}

	// 事务外记录日志，避免阻塞
	RecordTopupLog(userId, fmt.Sprintf("管理员补单成功，充值金额: %v，支付金额：%f", logger.FormatQuota(quotaAdded), payMoney), callerIp, paymentMethod, "admin")
	return userId, quotaAdded, nil
}
