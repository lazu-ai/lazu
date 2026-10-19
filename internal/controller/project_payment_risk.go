package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/stripe/stripe-go/v81"
	"gorm.io/gorm"
)

func projectRefundWebhook(c *gin.Context, event stripe.Event) {
	var charge stripe.Charge
	if err := json.Unmarshal(event.Data.Raw, &charge); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if charge.PaymentIntent == nil || charge.Customer == nil {
		return
	}
	matched, err := bindLegacyPaymentForCharge(c, charge)
	if err != nil {
		common.SysError(fmt.Sprintf("Stripe refund legacy order lookup failed for charge %s: %v", charge.ID, err))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if !matched {
		return
	}
	change, err := model.ApplyStripeChargeRefund(model.StripeChargeRefund{
		ChargeID: charge.ID, PaymentIntentID: charge.PaymentIntent.ID,
		CustomerID: charge.Customer.ID, Currency: string(charge.Currency),
		AmountCents: charge.Amount, RefundedCents: charge.AmountRefunded,
		AdminEmail: email_setting.GetEmailSetting().AdminEmail,
	})
	if err != nil {
		common.SysError(fmt.Sprintf("Stripe refund could not be reconciled for charge %s: %v", charge.ID, err))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if change.Changed {
		wakeProjectNotifications()
	}
}

func projectDisputeWebhook(c *gin.Context, event stripe.Event) {
	var dispute stripe.Dispute
	if err := json.Unmarshal(event.Data.Raw, &dispute); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if dispute.Charge == nil || dispute.Charge.ID == "" {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	params := &stripe.ChargeParams{}
	params.Context = c.Request.Context()
	charge, err := projectStripeClient().Charges.Get(dispute.Charge.ID, params)
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if charge.PaymentIntent == nil || charge.Customer == nil {
		return
	}
	matched, err := bindLegacyPaymentForCharge(c, *charge)
	if err != nil {
		common.SysError(fmt.Sprintf("Stripe dispute legacy order lookup failed for charge %s: %v", charge.ID, err))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if !matched {
		return
	}
	change, err := model.RecordStripeDispute(model.StripeDisputeNotice{
		DisputeID: dispute.ID, ChargeID: charge.ID,
		PaymentIntentID: charge.PaymentIntent.ID, CustomerID: charge.Customer.ID,
		DisputeCents: dispute.Amount, ChargeCents: charge.Amount,
		AdminEmail: email_setting.GetEmailSetting().AdminEmail,
		Reason:     string(dispute.Reason),
	})
	if err != nil {
		common.SysError(fmt.Sprintf("Stripe dispute could not be reconciled for dispute %s: %v", dispute.ID, err))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if change.Changed {
		wakeProjectNotifications()
	}
}

// A charge on the same Stripe account may belong to another product. Only a
// known top-up is allowed into wallet risk accounting.
func bindLegacyPaymentForCharge(c *gin.Context, charge stripe.Charge) (bool, error) {
	var count int64
	if err := model.CoreDB().Model(&model.TopUp{}).Where("stripe_payment_intent_id = ?", charge.PaymentIntent.ID).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	params := &stripe.CheckoutSessionListParams{PaymentIntent: stripe.String(charge.PaymentIntent.ID)}
	params.Context = c.Request.Context()
	iter := projectStripeClient().CheckoutSessions.List(params)
	for iter.Next() {
		session := iter.CheckoutSession()
		if session.ClientReferenceID == "" {
			continue
		}
		if err := model.CoreDB().Model(&model.TopUp{}).Where("trade_no = ?", session.ClientReferenceID).Count(&count).Error; err != nil {
			return false, err
		}
		if count == 0 {
			continue
		}
		if session.PaymentIntent == nil || session.PaymentIntent.ID != charge.PaymentIntent.ID || session.Customer == nil || session.Customer.ID != charge.Customer.ID ||
			session.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid || session.AmountTotal != charge.Amount || session.Currency != charge.Currency {
			return false, model.ErrProjectStateConflict
		}
		if err := model.BindLegacyStripePayment(model.LegacyStripePayment{
			Reference: session.ClientReferenceID, SessionID: session.ID, PaymentIntentID: charge.PaymentIntent.ID,
			ChargeID: charge.ID, CustomerID: charge.Customer.ID, Currency: string(charge.Currency), SubtotalCents: session.AmountSubtotal, AmountCents: charge.Amount,
		}); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := iter.Err(); err != nil {
		return false, err
	}
	paramsForIntent := &stripe.PaymentIntentParams{}
	paramsForIntent.Context = c.Request.Context()
	intent, err := projectStripeClient().PaymentIntents.Get(charge.PaymentIntent.ID, paramsForIntent)
	if err != nil {
		return false, err
	}
	if reference := intent.Metadata["lazu_top_up_reference"]; reference != "" {
		if err := model.CoreDB().Model(&model.TopUp{}).Where("trade_no = ?", reference).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return false, model.ErrProjectStateConflict
		}
	}
	return false, nil
}

func bindLegacyPaymentFromSession(c *gin.Context, session stripe.CheckoutSession) error {
	if session.PaymentStatus == stripe.CheckoutSessionPaymentStatusNoPaymentRequired {
		return nil
	}
	if session.PaymentIntent == nil || session.Customer == nil || session.AmountTotal <= 0 {
		return model.ErrProjectStateConflict
	}
	params := &stripe.PaymentIntentParams{}
	params.Context = c.Request.Context()
	params.AddExpand("latest_charge")
	intent, err := projectStripeClient().PaymentIntents.Get(session.PaymentIntent.ID, params)
	if err != nil {
		return err
	}
	charge := intent.LatestCharge
	if intent.Customer == nil || intent.Customer.ID != session.Customer.ID || charge == nil || charge.Customer == nil || charge.Customer.ID != session.Customer.ID ||
		charge.PaymentIntent == nil || charge.PaymentIntent.ID != intent.ID || charge.Amount != session.AmountTotal || charge.Currency != session.Currency {
		return model.ErrProjectStateConflict
	}
	return model.BindLegacyStripePayment(model.LegacyStripePayment{
		Reference: session.ClientReferenceID, SessionID: session.ID, PaymentIntentID: intent.ID,
		ChargeID: charge.ID, CustomerID: session.Customer.ID, Currency: string(session.Currency), SubtotalCents: session.AmountSubtotal, AmountCents: charge.Amount,
	})
}

// AdminListPaymentDisputes is the manual review queue for open Stripe cases.
func AdminListPaymentDisputes(c *gin.Context) {
	rows, err := model.ListOpenStripeDisputes(100)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		items = append(items, gin.H{"top_up_id": row.PublicID, "dispute_id": row.StripeDisputeID, "project_id": row.ProjectID, "owner_user_id": row.UserId, "paid_amount": row.PaidAmount, "disputed_amount": float64(row.StripeDisputeCents) / 100})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items}})
}

func AdminResolvePaymentDispute(c *gin.Context) {
	var request struct {
		DisputeID string `json:"dispute_id" binding:"required"`
		Outcome   string `json:"outcome" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || (request.Outcome != "won" && request.Outcome != "lost") {
		httperr.Invalid(c, "outcome")
		return
	}
	params := &stripe.DisputeParams{}
	params.Context = c.Request.Context()
	dispute, err := projectStripeClient().Disputes.Get(request.DisputeID, params)
	if err != nil {
		httperr.Write(c, types.WrapErr(err, types.ErrorCodePaymentProviderUnavailable))
		return
	}
	if string(dispute.Status) != request.Outcome {
		httperr.Code(c, types.ErrorCodeDisputeStillOpen, types.D{"dispute_id": request.DisputeID, "status": string(dispute.Status)})
		return
	}
	change, err := model.ResolveStripeDispute(c.Param("topup_id"), request.DisputeID, request.Outcome, email_setting.GetEmailSetting().AdminEmail)
	if err != nil {
		httperr.Write(c, disputeResolutionError(err))
		return
	}
	if change.Changed {
		model.WriteAdminAuditLog(c, "payment.dispute.resolve", "top_up", change.TopUpID, change.TopUpID,
			gin.H{"dispute_id": request.DisputeID, "status": "open"},
			gin.H{"dispute_id": request.DisputeID, "status": request.Outcome, "credit_reversed_microusd": change.ReversedCredit, "balance_microusd": change.Balance})
		wakeProjectNotifications()
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"top_up_id": change.TopUpID, "outcome": request.Outcome}})
}

func AdminReconcilePaymentDocument(c *gin.Context) {
	if err := service.ReconcileProjectPaymentDocumentByID(c.Request.Context(), c.Param("topup_id")); err != nil {
		httperr.Write(c, types.WrapErr(fmt.Errorf("manual payment document reconciliation: %w", err), types.ErrorCodePaymentDocumentReconcile))
		return
	}
	wakeProjectNotifications()
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// disputeResolutionError keeps database failures internal and reports every
// rejected resolution (unknown top-up, other dispute, already settled) as one
// operator-facing conflict.
func disputeResolutionError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, model.ErrProjectInvalidInput) || errors.Is(err, model.ErrProjectStateConflict) {
		return types.Err(types.ErrorCodeDisputeResolutionFailed)
	}
	return err
}
