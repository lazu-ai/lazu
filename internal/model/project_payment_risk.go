package model

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type StripeChargeRefund struct {
	ChargeID        string
	PaymentIntentID string
	CustomerID      string
	Currency        string
	AmountCents     int64
	RefundedCents   int64
	AdminEmail      string
}

type LegacyStripePayment struct {
	Reference, SessionID, PaymentIntentID, ChargeID, CustomerID, Currency string
	SubtotalCents, AmountCents                                            int64
}

// BindLegacyStripePayment accepts only a Checkout Session already verified by
// Stripe and ties it to the original order before credit or risk handling.
func BindLegacyStripePayment(input LegacyStripePayment) error {
	if input.Reference == "" || input.SessionID == "" || input.PaymentIntentID == "" || input.ChargeID == "" || input.CustomerID == "" || input.Currency != "usd" || input.AmountCents <= 0 || input.SubtotalCents <= 0 {
		return ErrProjectInvalidInput
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", input.Reference).First(&order).Error; err != nil {
			return err
		}
		if (order.Status != common.TopUpStatusPending && order.Status != common.TopUpStatusSuccess) || order.InvoiceStatus != "legacy" || order.ProjectID <= 0 || order.Amount <= 0 || order.Amount > int64(^uint64(0)>>1)/100 || order.Amount*100 != input.SubtotalCents ||
			order.StripePaymentIntentID != "" && paymentPaidCents(order) != input.AmountCents ||
			order.StripeCheckoutSessionID != "" && order.StripeCheckoutSessionID != input.SessionID ||
			order.StripePaymentIntentID != "" && order.StripePaymentIntentID != input.PaymentIntentID ||
			order.StripeChargeID != "" && order.StripeChargeID != input.ChargeID ||
			(order.StripePaymentIntentID != "" || order.StripeCheckoutSessionID != "") && order.StripeCustomerID != "" && order.StripeCustomerID != input.CustomerID {
			return ErrProjectStateConflict
		}
		var existing int64
		if err := tx.Model(&TopUp{}).Where("id <> ? AND (stripe_payment_intent_id = ? OR stripe_charge_id = ? OR stripe_checkout_session_id = ?)", order.Id, input.PaymentIntentID, input.ChargeID, input.SessionID).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return ErrProjectStateConflict
		}
		return tx.Model(&order).Updates(map[string]any{
			"stripe_checkout_session_id": input.SessionID, "stripe_payment_intent_id": input.PaymentIntentID,
			"stripe_charge_id": input.ChargeID, "stripe_customer_id": input.CustomerID,
			"paid_amount": float64(input.AmountCents) / 100,
		}).Error
	})
}

type PaymentRiskChange struct {
	TopUpID        string
	ProjectID      int
	OwnerUserID    int
	RefundedCents  int64
	ReversedCredit billing.MicroUSD
	Balance        billing.MicroUSD
	DisputeID      string
	Changed        bool
}

func paymentCreditForOrder(order TopUp) billing.MicroUSD {
	if order.CreditedMicrousd > 0 {
		return order.CreditedMicrousd
	}
	return USDToMicrousd(order.OrderUSD())
}

func paymentPaidCents(order TopUp) int64 {
	return int64(USDToMicrousd(order.PaidAmount)+5_000) / 10_000
}

func proratedReversal(credit billing.MicroUSD, refundedCents, paidCents int64) billing.MicroUSD {
	if refundedCents >= paidCents {
		return credit
	}
	// Divide before multiplying so a large purchased balance cannot overflow.
	whole := int64(credit) / paidCents
	remainder := int64(credit) % paidCents
	return billing.MicroUSD(whole*refundedCents + remainder*refundedCents/paidCents)
}

func combinedLossCents(refunded, disputed, charge int64) int64 {
	if refunded >= charge || disputed >= charge-refunded {
		return charge
	}
	return refunded + disputed
}

func queuePaymentRiskNoticeTx(tx *gorm.DB, order TopUp, email, key, severity, title string, facts []common.MailFact, action *common.MailAction) error {
	email = strings.TrimSpace(email)
	if email == "" {
		common.SysError("payment risk notification not queued: admin email is not configured")
		return nil
	}
	blocks := []common.MailBlock{{Facts: facts}}
	if action != nil {
		blocks = append(blocks, common.MailBlock{Primary: action})
	}
	body, err := common.RenderMail(common.Mail{Tier: common.MailOps, Source: "PAYMENT", Severity: severity, Title: title, Preheader: order.PublicID, Blocks: blocks})
	if err != nil {
		return err
	}
	return QueueProjectEmailTx(tx, order.ProjectID, 0, key, ProjectEmail{Receiver: email, Subject: "[Lazu ops] " + title, HTML: body})
}

func reversePaymentCreditTx(tx *gorm.DB, order *TopUp, target billing.MicroUSD, refType, refID string) (billing.MicroUSD, error) {
	credit := paymentCreditForOrder(*order)
	if target < order.ReversedCreditMicrousd || target > credit {
		return 0, ErrProjectStateConflict
	}
	delta := target - order.ReversedCreditMicrousd
	if delta == 0 {
		return 0, nil
	}
	if err := adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{
		UserID: order.UserId, DeltaQuota: -delta, Kind: WalletLedgerKindPaymentReversal,
		RefType: refType, RefID: refID, PricingVersion: defaultPricingSeedVersion,
		AllowNegative: true, IncludeDeletedUser: true, Metadata: map[string]any{"top_up_id": order.PublicID},
	}); err != nil {
		return 0, err
	}
	if err := runTopUpReversedHooks(tx, order.Id); err != nil {
		return 0, err
	}
	order.ReversedCreditMicrousd = target
	return delta, nil
}

// ApplyStripeChargeRefund treats Stripe's amount_refunded as a cumulative
// value. Older or duplicated snapshots cannot subtract the same credit twice.
func ApplyStripeChargeRefund(input StripeChargeRefund) (PaymentRiskChange, error) {
	var result PaymentRiskChange
	if input.ChargeID == "" || input.PaymentIntentID == "" || input.CustomerID == "" || input.Currency != "usd" || input.AmountCents <= 0 || input.RefundedCents < 0 || input.RefundedCents > input.AmountCents {
		return result, ErrProjectInvalidInput
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("stripe_payment_intent_id = ?", input.PaymentIntentID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != common.TopUpStatusSuccess || order.StripeCustomerID != input.CustomerID || paymentPaidCents(order) != input.AmountCents || order.StripeChargeID != "" && order.StripeChargeID != input.ChargeID {
			return ErrProjectStateConflict
		}
		result.TopUpID, result.ProjectID, result.OwnerUserID = order.PublicID, order.ProjectID, order.UserId
		if input.RefundedCents <= order.StripeRefundedCents {
			return tx.Unscoped().Select("quota").First(&User{}, order.UserId).Error
		}
		credit := paymentCreditForOrder(order)
		disputed := int64(0)
		if order.DisputeStatus == "lost" {
			disputed = order.StripeDisputeCents
		}
		target := proratedReversal(credit, combinedLossCents(input.RefundedCents, disputed, input.AmountCents), input.AmountCents)
		if target < order.ReversedCreditMicrousd {
			target = order.ReversedCreditMicrousd // A dispute may already have reversed this credit.
		}
		reversed, err := reversePaymentCreditTx(tx, &order, target, "stripe_charge_refund", fmt.Sprintf("%s:%d", input.ChargeID, input.RefundedCents))
		if err != nil {
			return err
		}
		order.StripeChargeID = input.ChargeID
		order.StripeRefundedCents = input.RefundedCents
		if err := tx.Model(&order).Updates(map[string]any{"stripe_charge_id": order.StripeChargeID, "stripe_refunded_cents": order.StripeRefundedCents, "reversed_credit_microusd": order.ReversedCreditMicrousd}).Error; err != nil {
			return err
		}
		var owner User
		if err := tx.Unscoped().Select("quota").First(&owner, order.UserId).Error; err != nil {
			return err
		}
		result.RefundedCents, result.ReversedCredit, result.Balance, result.Changed = input.RefundedCents, reversed, owner.Quota, true
		return queuePaymentRiskNoticeTx(tx, order, input.AdminEmail, fmt.Sprintf("payment-risk:refund:%s:%d", input.ChargeID, input.RefundedCents), "WARN", "Stripe 退款已入账 · "+order.PublicID, []common.MailFact{
			{Label: "充值", Value: order.PublicID},
			{Label: "退款", Value: common.MailUSD(float64(input.RefundedCents) / 100)},
			{Label: "扣回额度", Value: common.MailUSD(reversed.Dollars())},
			{Label: "所有者余额", Value: common.MailUSD(owner.Quota.Dollars())},
		}, nil)
	})
	if err == nil && result.Changed {
		_ = invalidateUserCache(result.OwnerUserID)
		_ = InvalidateUserTokensCache(result.OwnerUserID)
	}
	return result, err
}

type StripeDisputeNotice struct {
	DisputeID       string
	ChargeID        string
	PaymentIntentID string
	CustomerID      string
	DisputeCents    int64
	ChargeCents     int64
	AdminEmail      string
	Reason          string
}

func RecordStripeDispute(input StripeDisputeNotice) (PaymentRiskChange, error) {
	var result PaymentRiskChange
	if input.DisputeID == "" || input.ChargeID == "" || input.PaymentIntentID == "" || input.CustomerID == "" || input.DisputeCents <= 0 || input.ChargeCents <= 0 || input.DisputeCents > input.ChargeCents {
		return result, ErrProjectInvalidInput
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("stripe_payment_intent_id = ?", input.PaymentIntentID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != common.TopUpStatusSuccess || order.StripeCustomerID != input.CustomerID || order.ProjectID <= 0 || paymentPaidCents(order) != input.ChargeCents || order.StripeChargeID != "" && order.StripeChargeID != input.ChargeID || order.StripeDisputeID != "" && order.StripeDisputeID != input.DisputeID {
			return ErrProjectStateConflict
		}
		result.TopUpID, result.ProjectID, result.OwnerUserID, result.DisputeID = order.PublicID, order.ProjectID, order.UserId, input.DisputeID
		if order.DisputeStatus == "open" || order.DisputeStatus == "won" || order.DisputeStatus == "lost" {
			return nil
		}
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, order.ProjectID).Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Updates(map[string]any{"stripe_charge_id": input.ChargeID, "stripe_dispute_id": input.DisputeID, "stripe_dispute_cents": input.DisputeCents, "stripe_charge_cents": input.ChargeCents, "dispute_status": "open"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Project{}).Where("id = ?", order.ProjectID).Update("payment_risk_hold", true).Error; err != nil {
			return err
		}
		result.Changed = true
		link := "https://dashboard.stripe.com/disputes/" + url.PathEscape(input.DisputeID)
		return queuePaymentRiskNoticeTx(tx, order, input.AdminEmail, "payment-risk:dispute-open:"+input.DisputeID, "ALERT", "Stripe 争议：项目已暂停消费", []common.MailFact{
			{Label: "充值", Value: order.PublicID},
			{Label: "争议", Value: input.DisputeID},
			{Label: "金额", Value: common.MailUSD(float64(input.DisputeCents) / 100)},
			{Label: "原因", Value: input.Reason},
		}, &common.MailAction{Label: "在 Stripe 处理", URL: link})
	})
	return result, err
}

// ResolveStripeDispute is an explicit administrator decision after the Stripe
// case is closed. The order lock makes repeated decisions idempotent.
func ResolveStripeDispute(topUpID, disputeID, outcome string, adminEmail ...string) (PaymentRiskChange, error) {
	var result PaymentRiskChange
	if topUpID == "" || disputeID == "" || (outcome != "won" && outcome != "lost") {
		return result, ErrProjectInvalidInput
	}
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var order TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id = ?", topUpID).First(&order).Error; err != nil {
			return err
		}
		if order.StripeDisputeID != disputeID || order.DisputeStatus == "" || order.DisputeStatus != "open" && order.DisputeStatus != outcome {
			return ErrProjectStateConflict
		}
		result.TopUpID, result.ProjectID, result.OwnerUserID, result.DisputeID = order.PublicID, order.ProjectID, order.UserId, disputeID
		if order.DisputeStatus == outcome {
			return nil
		}
		var project Project
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, order.ProjectID).Error; err != nil {
			return err
		}
		if outcome == "lost" {
			if order.StripeChargeCents <= 0 || order.StripeDisputeCents <= 0 || order.StripeDisputeCents > order.StripeChargeCents {
				return ErrProjectStateConflict
			}
			var err error
			target := proratedReversal(paymentCreditForOrder(order), combinedLossCents(order.StripeRefundedCents, order.StripeDisputeCents, order.StripeChargeCents), order.StripeChargeCents)
			if target < order.ReversedCreditMicrousd {
				target = order.ReversedCreditMicrousd
			}
			result.ReversedCredit, err = reversePaymentCreditTx(tx, &order, target, "stripe_dispute", disputeID)
			if err != nil {
				return err
			}
		}
		order.DisputeStatus = outcome
		if err := tx.Model(&order).Updates(map[string]any{"dispute_status": outcome, "reversed_credit_microusd": order.ReversedCreditMicrousd}).Error; err != nil {
			return err
		}
		var openCount int64
		if err := tx.Model(&TopUp{}).Where("project_id = ? AND dispute_status = ?", order.ProjectID, "open").Count(&openCount).Error; err != nil {
			return err
		}
		if err := tx.Model(&Project{}).Where("id = ?", order.ProjectID).Update("payment_risk_hold", openCount > 0).Error; err != nil {
			return err
		}
		var owner User
		if err := tx.Unscoped().Select("quota").First(&owner, order.UserId).Error; err != nil {
			return err
		}
		result.Balance, result.Changed = owner.Quota, true
		email := ""
		if len(adminEmail) > 0 {
			email = adminEmail[0]
		}
		return queuePaymentRiskNoticeTx(tx, order, email, "payment-risk:dispute-resolved:"+disputeID, "NOTICE", "Stripe 争议已结束", []common.MailFact{
			{Label: "充值", Value: order.PublicID},
			{Label: "结果", Value: outcome},
			{Label: "扣回额度", Value: common.MailUSD(result.ReversedCredit.Dollars())},
			{Label: "所有者余额", Value: common.MailUSD(owner.Quota.Dollars())},
		}, nil)
	})
	if err == nil && result.Changed {
		_ = invalidateUserCache(result.OwnerUserID)
		_ = InvalidateUserTokensCache(result.OwnerUserID)
	}
	return result, err
}

func ListOpenStripeDisputes(limit int) ([]TopUp, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid limit")
	}
	var rows []TopUp
	err := CoreDB().Where("dispute_status = ?", "open").Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func ProjectPaymentHeld(projectID int) (bool, error) {
	if projectID <= 0 {
		return false, nil
	}
	var project Project
	if err := CoreDB().Select("payment_risk_hold").First(&project, projectID).Error; err != nil {
		return false, err
	}
	return project.PaymentRiskHold, nil
}

// CheckProjectPaymentAdmission serializes a zero-cost provider admission with
// dispute holds using the same project row lock as paid reservations.
func CheckProjectPaymentAdmission(projectID int) error {
	if projectID <= 0 || CoreDB() == nil {
		return nil
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		return checkProjectPaymentHoldTx(tx, projectID)
	})
}

// Admission paths take the project lock so a dispute hold cannot be committed
// between the check and the new charge or reservation.
func checkProjectPaymentHoldTx(tx *gorm.DB, projectID int) error {
	if projectID <= 0 {
		return nil
	}
	var project Project
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("payment_risk_hold").First(&project, projectID).Error; err != nil {
		return err
	}
	if project.PaymentRiskHold {
		return ErrProjectAccessSuspended
	}
	return nil
}
