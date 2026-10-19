package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (topUp *TopUp) BeforeCreate(tx *gorm.DB) error {
	if topUp.PublicID == "" {
		topUp.PublicID = common.GeneratePublicID("top")
	}
	if topUp.ProjectID == 0 && topUp.UserId > 0 && tx.Migrator().HasTable(&Project{}) {
		var project Project
		if err := tx.Where("owner_user_id = ?", topUp.UserId).First(&project).Error; err != nil {
			return err
		}
		topUp.ProjectID, topUp.PaidByUserID, topUp.StripeCustomerID = project.ID, topUp.UserId, project.StripeCustomerID
	}
	return nil
}

// The order is durable before contacting Stripe. The beneficiary and the
// person initiating payment are immutable, including on an HTTP retry.
type ProjectCheckoutConfig struct {
	PriceID        string
	ReturnBase     string
	PromotionCodes bool
}

func BeginProjectTopUp(projectID, actorID int, operationKey string, amount int64, configs ...ProjectCheckoutConfig) (TopUp, error) {
	var order TopUp
	if strings.TrimSpace(operationKey) == "" || len(operationKey) > 128 || amount <= 0 || amount > 10000 {
		return order, ErrProjectInvalidInput
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("project-topup:%d:%d:%s", projectID, actorID, operationKey)))
	reference := "ref_" + hex.EncodeToString(digest[:])
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		actor, err := lockProjectMember(tx, projectID, actorID)
		if err != nil {
			return err
		}
		settings, err := project.GetSettings()
		if err != nil {
			return err
		}
		if !CanProjectAction(*actor, nil, ProjectTopUp, settings) {
			return ErrProjectForbidden
		}
		err = tx.Where("trade_no = ?", reference).First(&order).Error
		if err == nil {
			if order.Amount != amount {
				return ErrProjectStateConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		shared := project.UpgradedAt != nil
		order = TopUp{StripeProjectName: project.Name, StripeProjectShared: &shared, ProjectID: projectID, PaidByUserID: actorID, UserId: project.OwnerUserID, StripeCustomerID: project.StripeCustomerID,
			Amount: amount, PaidAmount: float64(amount), CreditedMicrousd: billing.MicroUSD(amount * billing.MicroUSDPerUSD),
			TradeNo: reference, PaymentMethod: "stripe", Status: common.TopUpStatusPending, InvoiceStatus: "pending", CreateTime: time.Now().Unix()}
		if len(configs) > 0 {
			order.StripePriceID = configs[0].PriceID
			order.StripeReturnBase = configs[0].ReturnBase
			order.StripePromotionCodes = configs[0].PromotionCodes
		}
		return tx.Create(&order).Error
	})
	return order, err
}

// Persist the first remote attempt before making a Stripe call. A timeout
// must not create another Customer after Stripe's idempotency retention ends.
func BeginProjectStripeCustomer(projectID int) (Project, error) {
	var project Project
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		if project.StripeCustomerID == "" && project.StripeCustomerStartedAt == 0 {
			project.StripeCustomerStartedAt = time.Now().Unix()
			return tx.Model(&project).Update("stripe_customer_started_at", project.StripeCustomerStartedAt).Error
		}
		return nil
	})
	return project, err
}

func SetProjectStripeCustomer(projectID int, customerID string) error {
	if customerID == "" {
		return ErrProjectInvalidInput
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, projectID).Error; err != nil {
			return err
		}
		if project.StripeCustomerID != "" && project.StripeCustomerID != customerID {
			return ErrProjectStateConflict
		}
		return tx.Model(&project).Update("stripe_customer_id", customerID).Error
	})
}

func AttachProjectCheckout(orderID int, customerID, sessionID, url string) error {
	if customerID == "" || sessionID == "" || url == "" {
		return ErrProjectInvalidInput
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, orderID).Error; err != nil {
			return err
		}
		if order.StripeCheckoutSessionID != "" && order.StripeCheckoutSessionID != sessionID {
			return ErrProjectStateConflict
		}
		if order.StripeCustomerID != "" && order.StripeCustomerID != customerID {
			return ErrProjectStateConflict
		}
		return tx.Model(&order).Updates(map[string]interface{}{"stripe_customer_id": customerID, "stripe_checkout_session_id": sessionID, "stripe_checkout_url": url}).Error
	})
}

type ProjectPaymentReceipt struct {
	Reference       string
	CustomerID      string
	SessionID       string
	PaymentIntentID string
	ChargeID        string
	PaidMicrousd    billing.MicroUSD
	Currency        string
	Method          string
	ReceiptURL      string
}

// Credit is independent of Invoice readiness. Repeated/late paid events never
// add money twice, and an expired event cannot undo an already paid order.
func CompleteProjectTopUp(input ProjectPaymentReceipt) (TopUp, bool, error) {
	var order TopUp
	credited := false
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", input.Reference).First(&order).Error; err != nil {
			return err
		}
		if order.ProjectID <= 0 || input.CustomerID == "" || input.SessionID == "" || strings.ToUpper(input.Currency) != "USD" || input.PaidMicrousd < 0 || input.PaidMicrousd > billing.MicroUSD(order.Amount*billing.MicroUSDPerUSD) {
			return ErrProjectInvalidInput
		}
		var project Project
		if err := tx.First(&project, order.ProjectID).Error; err != nil {
			return err
		}
		if order.UserId != project.OwnerUserID || project.StripeCustomerID != input.CustomerID || order.StripeCustomerID != "" && order.StripeCustomerID != input.CustomerID || order.StripeCheckoutSessionID != "" && order.StripeCheckoutSessionID != input.SessionID {
			return ErrProjectStateConflict
		}
		if order.StripePaymentIntentID != "" && input.PaymentIntentID != "" && order.StripePaymentIntentID != input.PaymentIntentID {
			return ErrProjectStateConflict
		}
		if !order.StripePromotionCodes && input.PaidMicrousd != billing.MicroUSD(order.Amount*billing.MicroUSDPerUSD) {
			return ErrProjectInvalidInput
		}
		if order.Status == common.TopUpStatusSuccess {
			return nil
		}
		if order.Status != common.TopUpStatusPending && order.Status != common.TopUpStatusExpired && order.Status != "failed" {
			return ErrProjectStateConflict
		}
		order.StripeCustomerID = input.CustomerID
		order.StripeCheckoutSessionID = input.SessionID
		if input.PaymentIntentID != "" {
			order.StripePaymentIntentID = input.PaymentIntentID
		}
		if input.ChargeID != "" {
			order.StripeChargeID = input.ChargeID
		}
		if input.PaidMicrousd == 0 && input.PaymentIntentID == "" && order.StripePromotionCodes {
			input.Method = "promotion"
		}
		order.PaidAmount = input.PaidMicrousd.Dollars()
		order.BonusMicrousd = order.CreditedMicrousd - input.PaidMicrousd
		if err := runTopUpCompletingHooks(tx, &order); err != nil {
			return err
		}
		if input.ReceiptURL != "" {
			order.StripeReceiptURL = input.ReceiptURL
		}
		order.Status = common.TopUpStatusSuccess
		order.CompleteTime = time.Now().Unix()
		if input.Method != "" && (order.PaymentMethod == "" || order.PaymentMethod == "stripe") {
			order.PaymentMethod = input.Method
		}
		order.NormalizeLedgerFields()
		if err := tx.Save(&order).Error; err != nil {
			return err
		}
		if err := applyTopUpWalletCredit(tx, &order); err != nil {
			return err
		}
		if err := markUserHasToppedUpTx(tx, order.UserId); err != nil {
			return err
		}
		var owner User
		if err := tx.Select("quota").First(&owner, project.OwnerUserID).Error; err != nil {
			return err
		}
		order.BalanceAfterMicrousd = &owner.Quota
		if err := tx.Model(&order).Update("balance_after_microusd", owner.Quota).Error; err != nil {
			return err
		}
		if err := queueProjectPaymentEmailTx(tx, project, order, true); err != nil {
			return err
		}
		credited = true
		return nil
	})
	if err == nil && credited {
		_ = invalidateUserCache(order.UserId)
		_ = InvalidateUserTokensCache(order.UserId)
	}
	return order, credited, err
}

type ProjectInvoiceUpdate struct {
	Reference  string
	CustomerID string
	InvoiceID  string
	Number     string
	HostedURL  string
	PDFURL     string
	BillTo     JSONValue
	Ready      bool
	Failed     bool
}

func UpdateProjectInvoice(input ProjectInvoiceUpdate) error {
	if input.Reference == "" || input.InvoiceID == "" || input.CustomerID == "" {
		return ErrProjectInvalidInput
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", input.Reference).First(&order).Error; err != nil {
			return err
		}
		var project Project
		if err := tx.First(&project, order.ProjectID).Error; err != nil {
			return err
		}
		if project.StripeCustomerID != input.CustomerID || order.StripeCustomerID != "" && order.StripeCustomerID != input.CustomerID || order.StripeInvoiceID != "" && order.StripeInvoiceID != input.InvoiceID {
			return ErrProjectStateConflict
		}
		if order.InvoiceStatus == "ready" {
			return queueProjectPaymentEmailTx(tx, project, order, false)
		}
		order.StripeInvoiceID = input.InvoiceID
		if input.Ready {
			if input.HostedURL == "" || input.PDFURL == "" || input.Number == "" {
				return ErrProjectInvalidInput
			}
			order.InvoiceStatus = "ready"
			order.StripeInvoiceNumber = input.Number
			order.StripeInvoiceURL = input.HostedURL
			order.StripeInvoicePDF = input.PDFURL
			order.BillTo = input.BillTo
		} else if input.Failed {
			order.InvoiceStatus = "failed"
		}
		if err := tx.Model(&order).Select("stripe_invoice_id", "invoice_status", "stripe_invoice_number", "stripe_invoice_url", "stripe_invoice_pdf", "bill_to").Updates(&order).Error; err != nil {
			return err
		}
		if input.Ready && order.Status == common.TopUpStatusSuccess {
			return queueProjectPaymentEmailTx(tx, project, order, false)
		}
		return nil
	})
}

// paymentEmailFallbackDelay is how long the payment e-mail waits for Stripe's
// Invoice and Receipt before it goes out without them. Documents usually land
// within seconds; the e-mail is never sent twice.
const paymentEmailFallbackDelay = 60 * time.Second

func projectPaymentDocumentsReady(order TopUp) bool {
	return order.Status == common.TopUpStatusSuccess && order.InvoiceStatus == "ready" && order.StripeInvoiceNumber != "" && order.StripeInvoicePDF != "" &&
		(order.PaidAmount <= 0 || order.StripeReceiptURL != "")
}

// queueProjectPaymentEmailTx queues the one payment e-mail for the wallet
// owner and, when someone else paid, for the payer too. Checkout, invoice and
// receipt events may arrive in any order and more than once:
//   - atCredit: called when the credit lands. With documents ready it queues
//     the full e-mail now; otherwise a version without the invoice link that
//     waits paymentEmailFallbackDelay.
//   - later calls: once documents are ready, replace that waiting e-mail
//     while it is still pending. After it was sent nothing more goes out.
func queueProjectPaymentEmailTx(tx *gorm.DB, project Project, order TopUp, atCredit bool) error {
	if order.Status != common.TopUpStatusSuccess {
		return nil
	}
	ready := projectPaymentDocumentsReady(order)
	if !ready && !atCredit {
		return nil
	}
	recipients := []int{project.OwnerUserID}
	if order.PaidByUserID > 0 && order.PaidByUserID != project.OwnerUserID {
		recipients = append(recipients, order.PaidByUserID)
	}
	var balance billing.MicroUSD
	if order.BalanceAfterMicrousd != nil {
		balance = *order.BalanceAfterMicrousd
	} else {
		// Orders credited before the snapshot column existed.
		var owner User
		if err := tx.Select("quota").First(&owner, project.OwnerUserID).Error; err != nil {
			return err
		}
		balance = owner.Quota
	}
	credit := fmt.Sprintf("$%.2f", order.CreditedMicrousd.Dollars())
	subject := credit + " added to your Lazu account"
	first := common.MailFact{Label: "Paid on", Value: paymentEmailDate(order.CompleteTime)}
	if len(recipients) > 1 {
		var payer User
		if err := tx.Unscoped().Select("display_name", "username").First(&payer, order.PaidByUserID).Error; err != nil {
			return err
		}
		payerName := strings.TrimSpace(payer.DisplayName)
		if payerName == "" {
			payerName = payer.Username
		}
		first = common.MailFact{Label: "Paid by", Value: payerName}
	}
	facts := []common.MailFact{first}
	if method := paymentEmailMethod(order.PaymentMethod); method != "" {
		facts = append(facts, common.MailFact{Label: "Payment method", Value: method})
	}
	facts = append(facts, common.MailFact{Label: "Wallet balance", Value: fmt.Sprintf("$%.2f", balance.Dollars())})
	if ready {
		facts = append(facts, common.MailFact{Label: "Invoice", Value: order.StripeInvoiceNumber, Mono: true})
	} else {
		facts = append(facts, common.MailFact{Label: "Top-up ID", Value: order.PublicID, Mono: true})
	}
	content := common.PaymentEmailContent{
		LogoURL: paymentEmailLogo(order.StripeReturnBase), Eyebrow: "PAYMENT RECEIVED",
		Hero: credit, CompactHero: order.CreditedMicrousd.Dollars() >= 1000,
		Message: "API credit added to your Lazu account.", Preheader: subject, Facts: facts,
		ActionLabel: "Open wallet",
	}
	if ready {
		// The hosted Invoice page says "paid" and offers both the Invoice and
		// the Receipt; Stripe's Invoice PDF alone still reads "Amount due".
		content.LinkURL = paymentEmailDocumentURL(order.StripeReturnBase, project.PublicID, order.PublicID, "invoice_page")
		content.LinkLabel = "View invoice"
	}
	// The wallet link opens each reader's own wallet: an account from another
	// surface gets that surface's billing page. The rest of the mail is the same.
	bodies := map[string]string{}
	renderFor := func(surface string) (string, error) {
		if body, ok := bodies[surface]; ok {
			return body, nil
		}
		content.ActionURL = paymentEmailWalletURL(order.StripeReturnBase, project.PublicID, order.PublicID, "", surface)
		body, err := common.RenderPaymentEmail(content)
		if err != nil {
			return "", err
		}
		bodies[surface] = body
		return body, nil
	}
	for _, id := range recipients {
		// Orders from before the single e-mail already sent the invoice mail.
		var legacy int64
		if err := tx.Model(&ProjectNotification{}).Where("event_key = ?", fmt.Sprintf("invoice:%s:%d", order.PublicID, id)).Count(&legacy).Error; err != nil {
			return err
		}
		if legacy > 0 {
			continue
		}
		var user User
		if err := tx.Unscoped().Select("email", "home_surface").First(&user, id).Error; err != nil {
			return err
		}
		body, err := renderFor(NormalizeUserHomeSurface(user.HomeSurface))
		if err != nil {
			return err
		}
		email := ProjectEmail{Receiver: user.Email, Subject: subject, HTML: body}
		key := fmt.Sprintf("payment:%s:%d", order.PublicID, id)
		if ready {
			err = replacePendingProjectEmailTx(tx, project.ID, id, key, email)
		} else {
			err = queueProjectEmailAtTx(tx, project.ID, id, key, email, time.Now().Add(paymentEmailFallbackDelay).Unix())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func siteAPIHost() string {
	if parsed, err := url.Parse(common.SiteAPIOrigin()); err == nil {
		return parsed.Hostname()
	}
	return ""
}

func projectTopUpDetailsURL(returnBase, projectID, topUpID string) string {
	return strings.TrimRight(returnBase, "/") + "/payment/success?project_id=" + url.QueryEscape(projectID) + "&top_up_id=" + url.QueryEscape(topUpID)
}

func paymentEmailLogo(returnBase string) string {
	if frontend := strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL")); frontend != "" {
		returnBase = frontend
	} else if parsed, err := url.Parse(returnBase); err == nil && parsed.Hostname() != "" && parsed.Hostname() == siteAPIHost() {
		// The logo lives on the web console, not on the gateway host.
		returnBase = common.SiteWebOrigin()
	}
	if returnBase == "" {
		returnBase = common.SiteWebOrigin()
	}
	return strings.TrimRight(returnBase, "/") + "/lazu-email-logo-paper.png"
}

func paymentEmailWalletURL(returnBase, projectID, topUpID, document, surface string) string {
	if returnBase == "" {
		returnBase = common.SiteWebOrigin()
	}
	result := strings.TrimRight(returnBase, "/") + UserSurfaceTopUpPath(surface, projectID, topUpID)
	if document != "" {
		result += "&document=" + url.QueryEscape(document)
	}
	return result
}

func paymentEmailDocumentURL(returnBase, projectID, topUpID, document string) string {
	if returnBase == "" {
		returnBase = common.SiteWebOrigin()
	}
	return strings.TrimRight(returnBase, "/") + "/payment/document?project_id=" + url.QueryEscape(projectID) + "&top_up_id=" + url.QueryEscape(topUpID) + "&document=" + url.QueryEscape(document)
}

func paymentEmailDate(timestamp int64) string {
	return time.Unix(timestamp, 0).UTC().Format("Jan 2, 2006")
}

func paymentEmailMethod(method string) string {
	if strings.Contains(method, " ···· ") {
		return method
	}
	switch strings.ToLower(method) {
	case "", "stripe":
		return ""
	case "card":
		return "Card"
	case "alipay":
		return "Alipay"
	case "wechat_pay":
		return "WeChat Pay"
	case "link":
		return "Link"
	case "paypal":
		return "PayPal"
	case "cashapp":
		return "Cash App"
	case "us_bank_account":
		return "US bank account"
	case "ach_debit":
		return "ACH debit"
	case "sepa_debit":
		return "SEPA debit"
	default:
		parts := strings.FieldsFunc(method, func(r rune) bool { return r == '_' || r == '-' || unicode.IsSpace(r) })
		for i, part := range parts {
			parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
		}
		return strings.Join(parts, " ")
	}
}

// ProjectPaymentMethod stores only the customer-visible card brand and last
// four digits. Other Stripe methods are normalized by paymentEmailMethod.
func ProjectPaymentMethod(kind, brand, last4 string) string {
	if kind != "card" || brand == "" || len(last4) != 4 {
		return kind
	}
	for _, digit := range last4 {
		if digit < '0' || digit > '9' {
			return kind
		}
	}
	label := strings.ToUpper(brand[:1]) + brand[1:]
	if strings.EqualFold(brand, "amex") {
		label = "Amex"
	}
	return label + " ···· " + last4
}

func ExpireProjectTopUp(reference string, failed bool) error {
	status := common.TopUpStatusExpired
	if failed {
		status = "failed"
	}
	return CoreDB().Model(&TopUp{}).Where("trade_no = ? AND status = ?", reference, common.TopUpStatusPending).Update("status", status).Error
}

func UpdateProjectPaymentReceipt(reference, customerID, intentID, chargeID, receiptURL, method string) error {
	if reference == "" || customerID == "" || intentID == "" || receiptURL == "" {
		return ErrProjectInvalidInput
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", reference).First(&order).Error; err != nil {
			return err
		}
		var project Project
		if err := tx.First(&project, order.ProjectID).Error; err != nil {
			return err
		}
		if project.StripeCustomerID != customerID || order.StripePaymentIntentID != "" && order.StripePaymentIntentID != intentID {
			return ErrProjectStateConflict
		}
		if order.StripeChargeID != "" && order.StripeChargeID != chargeID {
			return ErrProjectStateConflict
		}
		updates := map[string]interface{}{"stripe_payment_intent_id": intentID, "stripe_charge_id": chargeID, "stripe_receipt_url": receiptURL}
		if method != "" {
			updates["payment_method"] = method
		}
		if err := tx.Model(&order).Updates(updates).Error; err != nil {
			return err
		}
		order.StripeReceiptURL = receiptURL
		if method != "" {
			order.PaymentMethod = method
		}
		return queueProjectPaymentEmailTx(tx, project, order, false)
	})
}

// Backfill the payment e-mail for recently paid orders whose documents became
// ready without an event reaching the fast path. Event keys are unique, so
// repeated runs are safe.
func QueueProjectPaymentEmailForTopUp(id int, fallbackReturnBase string) error {
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id).Error; err != nil {
			return err
		}
		var project Project
		if err := tx.First(&project, order.ProjectID).Error; err != nil {
			return err
		}
		if order.UserId != project.OwnerUserID || order.StripeCustomerID == "" || order.StripeCustomerID != project.StripeCustomerID {
			return ErrProjectStateConflict
		}
		if order.StripeReturnBase == "" {
			order.StripeReturnBase = strings.TrimRight(strings.TrimSpace(fallbackReturnBase), "/")
		}
		if order.StripeReturnBase == "" {
			return errors.New("payment return base is unavailable for payment email")
		}
		return queueProjectPaymentEmailTx(tx, project, order, false)
	})
}

// A paid order with complete documents may still lack its payment e-mail when
// every event reached a replica that failed mid-way. Check both recipients; an
// invoice mail from before the single e-mail counts as sent.
func ListProjectTopUpsMissingDocumentEmails(limit int, since int64) ([]TopUp, error) {
	if limit <= 0 || limit > 100 {
		return nil, ErrProjectInvalidInput
	}
	var orders []TopUp
	sent := func(column string) string {
		return "EXISTS (SELECT 1 FROM project_notifications WHERE event_key IN ('payment:' || top_ups.public_id || ':' || CAST(top_ups." + column + " AS TEXT), 'invoice:' || top_ups.public_id || ':' || CAST(top_ups." + column + " AS TEXT)))"
	}
	err := CoreDB().Model(&TopUp{}).
		Where("top_ups.project_id > 0 AND top_ups.status = ? AND top_ups.complete_time >= ? AND top_ups.invoice_status = ? AND top_ups.stripe_invoice_id <> '' AND top_ups.stripe_invoice_number <> '' AND top_ups.stripe_invoice_pdf <> '' AND (top_ups.paid_amount <= 0 OR (top_ups.stripe_receipt_url <> '' AND top_ups.stripe_payment_intent_id <> ''))", common.TopUpStatusSuccess, since, "ready").
		Where("NOT " + sent("user_id") + " OR (top_ups.paid_by_user_id > 0 AND top_ups.paid_by_user_id <> top_ups.user_id AND NOT " + sent("paid_by_user_id") + ")").
		Order("top_ups.complete_time ASC, top_ups.id ASC").Limit(limit).Find(&orders).Error
	return orders, err
}

// Recent paid orders with an incomplete document pair are retried against
// Stripe. The charge and invoice webhooks are the fast path; this is the
// compensation path when either event is delayed or lost.
func ListProjectTopUpsMissingDocuments(limit int, since int64) ([]TopUp, error) {
	if limit <= 0 || limit > 100 {
		return nil, ErrProjectInvalidInput
	}
	var orders []TopUp
	err := CoreDB().Where("project_id > 0 AND status = ? AND complete_time >= ? AND stripe_checkout_session_id <> '' AND (invoice_status <> ? OR (paid_amount > 0 AND stripe_receipt_url = ''))", common.TopUpStatusSuccess, since, "ready").
		Order("document_checked_at ASC, complete_time ASC, id ASC").Limit(limit).Find(&orders).Error
	return orders, err
}

// Move an attempted order behind untouched ones, including when Stripe has
// not produced a final invoice yet. A failed old order must not block the queue.
func MarkProjectTopUpDocumentChecked(id int, checkedAt int64) error {
	return CoreDB().Model(&TopUp{}).Where("id = ?", id).Update("document_checked_at", checkedAt).Error
}

// ClaimProjectTopUpsForDocumentReconciliation records a bounded retry lease
// before any external Stripe call. Workers release their database lock after
// this transaction; a failed call becomes eligible again after five minutes.
func ClaimProjectTopUpsForDocumentReconciliation(limit int, since, checkedAt int64) ([]TopUp, error) {
	if limit <= 0 || limit > 100 {
		return nil, ErrProjectInvalidInput
	}
	var orders []TopUp
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		query := tx.Where("project_id > 0 AND status = ? AND complete_time >= ? AND stripe_checkout_session_id <> '' AND (invoice_status <> ? OR (paid_amount > 0 AND stripe_receipt_url = ''))", common.TopUpStatusSuccess, since, "ready").
			Where("document_checked_at <= ?", checkedAt-int64((5*time.Minute)/time.Second)).
			Order("document_checked_at ASC, complete_time ASC, id ASC").Limit(limit)
		if tx.Dialector.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if err := query.Find(&orders).Error; err != nil {
			return err
		}
		if len(orders) == 0 {
			return nil
		}
		ids := make([]int, len(orders))
		for i, order := range orders {
			ids[i] = order.Id
		}
		return tx.Model(&TopUp{}).Where("id IN ?", ids).Update("document_checked_at", checkedAt).Error
	})
	return orders, err
}
