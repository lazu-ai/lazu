package controller

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/paymentintent"
	"github.com/stripe/stripe-go/v81/webhook"
)

const PaymentMethodStripe = "stripe"

var stripeAdaptor = &StripeAdaptor{}

type StripePayRequest struct {
	ProjectID     string `json:"project_id"`
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	// Surface is the product the payer checks out from. A registered surface
	// returns them to its own billing pages instead of the console wallet.
	Surface string `json:"surface"`
}

type StripeAdaptor struct {
}

// writeInvalidTopupAmount reports an amount outside the card top-up range.
func writeInvalidTopupAmount(c *gin.Context) {
	httperr.Code(c, types.ErrorCodeInvalidAmount, types.D{"min_amount": getStripeMinTopup(), "max_amount": getStripeMaxTopup()})
}

// validStripeTopupAmount is the one range check every card top-up passes:
// whole dollars from the operator's minimum to the fixed ceiling.
func validStripeTopupAmount(amount int64) bool {
	return amount >= getStripeMinTopup() && amount <= getStripeMaxTopup()
}

func (*StripeAdaptor) RequestAmount(c *gin.Context, req *StripePayRequest) {
	if !validStripeTopupAmount(req.Amount) {
		writeInvalidTopupAmount(c)
		return
	}
	// Checkout sells $1 units of one Price; the amount is the quantity.
	c.JSON(http.StatusOK, gin.H{"success": true, "data": strconv.FormatInt(req.Amount, 10)})
}

func (*StripeAdaptor) RequestPay(c *gin.Context, req *StripePayRequest) {
	if req.PaymentMethod != PaymentMethodStripe {
		httperr.Invalid(c, "payment_method")
		return
	}
	if !validStripeTopupAmount(req.Amount) {
		writeInvalidTopupAmount(c)
		return
	}

	requestProjectStripePay(c, req)
}

func RequestStripeAmount(c *gin.Context) {
	var req StripePayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "amount")
		return
	}
	stripeAdaptor.RequestAmount(c, &req)
}

func RequestStripePay(c *gin.Context) {
	var req StripePayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "amount")
		return
	}
	stripeAdaptor.RequestPay(c, &req)
}

func StripeWebhook(c *gin.Context) {
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("Failed to read Stripe webhook payload: %v\n", err)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	signature := c.GetHeader("Stripe-Signature")
	endpointSecret := setting.StripeWebhookSecret
	event, err := webhook.ConstructEventWithOptions(payload, signature, endpointSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})

	if err != nil {
		log.Printf("Failed to verify Stripe webhook signature: %v\n", err)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted, stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		sessionCompleted(c, event)
	case stripe.EventTypeCheckoutSessionExpired, stripe.EventTypeCheckoutSessionAsyncPaymentFailed:
		if err := model.ExpireProjectTopUp(event.GetObjectValue("client_reference_id"), event.Type == stripe.EventTypeCheckoutSessionAsyncPaymentFailed); err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
		}
	case stripe.EventTypeInvoiceFinalized, stripe.EventTypeInvoicePaid, stripe.EventTypeInvoiceFinalizationFailed:
		projectInvoiceWebhook(c, event)
	case stripe.EventTypeChargeSucceeded, stripe.EventTypeChargeUpdated:
		projectChargeWebhook(c, event)
	case stripe.EventTypeChargeRefunded:
		projectRefundWebhook(c, event)
	case stripe.EventTypeChargeDisputeCreated:
		projectDisputeWebhook(c, event)
	case stripe.EventTypeCustomerUpdated:
		projectCustomerWebhook(c, event)
	default:
		// Other Stripe events are acknowledged without modifying payment state.
	}

	if !c.IsAborted() {
		c.Status(http.StatusOK)
	}
}

func sessionCompleted(c *gin.Context, event stripe.Event) {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "complete" != status {
		log.Println("Unexpected Stripe Checkout completion status:", status, ",", referenceId)
		return
	}

	var checkoutSession stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &checkoutSession); err != nil {
		log.Printf("Failed to parse Stripe Checkout session: %v\n", err)
	}

	if checkoutSession.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid && checkoutSession.PaymentStatus != stripe.CheckoutSessionPaymentStatusNoPaymentRequired {
		return
	}
	order := model.GetTopUpByTradeNo(referenceId)
	if order == nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if order.ProjectID > 0 && order.InvoiceStatus != "legacy" {
		completeProjectStripeSession(c, checkoutSession)
		return
	}
	if order.Status == common.TopUpStatusSuccess {
		if err := bindLegacyPaymentFromSession(c, checkoutSession); err != nil {
			log.Printf("Failed to link legacy Stripe order %s: %v\n", referenceId, err)
			c.AbortWithStatus(http.StatusServiceUnavailable)
		}
		return
	}
	if err := bindLegacyPaymentFromSession(c, checkoutSession); err != nil {
		log.Printf("Failed to link legacy Stripe order %s: %v\n", referenceId, err)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	callbackPaymentMethod := getStripePaymentMethod(&checkoutSession)
	_, _, err := model.Recharge(referenceId, customerId, common.GetRealClientIP(c), callbackPaymentMethod)
	if err != nil {
		log.Println(err.Error(), referenceId)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	// Orders from before project billing complete here without mail: the
	// receipt, balance and admin notices they used to send are retired, and
	// project top-ups send the single payment mail.

	total, _ := strconv.ParseFloat(event.GetObjectValue("amount_total"), 64)
	currency := strings.ToUpper(event.GetObjectValue("currency"))
	log.Printf("Payment received: %s, %.2f (%s)", referenceId, total/100, currency)
}

func GetChargedAmount(count float64, user model.User) float64 {
	_ = user
	return count
}

// getStripeMinTopup is the operator's minimum (option MinTopUpUSD) in whole
// USD; getStripeMaxTopup the fixed ceiling. Both are what the console shows.
func getStripeMinTopup() int64 {
	return int64(setting.NormalizeMinTopUp(setting.StripeMinTopUp))
}

func getStripeMaxTopup() int64 {
	return int64(setting.StripeMaxTopUp)
}

func getStripePaymentMethod(checkoutSession *stripe.CheckoutSession) string {
	if checkoutSession == nil {
		return PaymentMethodStripe
	}
	if checkoutSession.PaymentIntent != nil {
		if checkoutSession.PaymentIntent.PaymentMethod != nil {
			return string(checkoutSession.PaymentIntent.PaymentMethod.Type)
		}
		if checkoutSession.PaymentIntent.ID != "" {
			stripe.Key = setting.StripeApiSecret
			if pi, err := paymentintent.Get(checkoutSession.PaymentIntent.ID, nil); err == nil {
				if pi.PaymentMethod != nil {
					return string(pi.PaymentMethod.Type)
				}
			}
		}
	}
	if len(checkoutSession.PaymentMethodTypes) > 0 {
		return checkoutSession.PaymentMethodTypes[0]
	}
	return PaymentMethodStripe
}
