package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
)

// An instance per call avoids changing stripe.Key across concurrent requests.
func projectStripeClient() *client.API {
	api := &client.API{}
	api.Init(setting.StripeApiSecret, nil)
	return api
}
func projectPaymentReturnBase() string {
	if frontend := strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL")), "/"); frontend != "" {
		return frontend
	}
	if setting.StripeReturnBase != "" {
		return setting.StripeReturnBase
	}
	return strings.TrimRight(system_setting.ServerAddress, "/")
}
func requestProjectStripePay(c *gin.Context, req *StripePayRequest) {
	pc, ok := legacyProjectContext(c, req.ProjectID)
	if !ok {
		return
	}
	operationKey := c.GetHeader("Idempotency-Key")
	if operationKey == "" && req.ProjectID == "" && c.Query("project_id") == "" {
		operationKey = common.GenerateRequestID()
	}
	order, err := model.BeginProjectTopUp(pc.Project.ID, pc.Member.UserID, operationKey, req.Amount, model.ProjectCheckoutConfig{PriceID: setting.StripePriceId, ReturnBase: projectPaymentReturnBase(), PromotionCodes: setting.StripePromotionCodesEnabled})
	if err != nil {
		projectFailure(c, err)
		return
	}
	if order.StripeCheckoutURL != "" && order.Status == "pending" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"pay_link": order.StripeCheckoutURL, "top_up_id": order.PublicID, "payment_status": order.Status}})
		return
	}
	if order.Status == "success" {
		// An idempotent replay of an already-paid order is a success.
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"top_up_id": order.PublicID, "payment_status": "paid"}})
		return
	}
	if order.Status != "pending" {
		projectFailure(c, model.ErrProjectStateConflict)
		return
	}
	if !strings.HasPrefix(setting.StripeApiSecret, "sk_") && !strings.HasPrefix(setting.StripeApiSecret, "rk_") {
		projectPaymentUnavailable(c, order)
		return
	}
	api := projectStripeClient()
	customerProject, err := model.BeginProjectStripeCustomer(pc.Project.ID)
	if err != nil {
		projectFailure(c, err)
		return
	}
	customerID := customerProject.StripeCustomerID
	if customerID == "" {
		if time.Now().Unix()-customerProject.StripeCustomerStartedAt > int64((23 * time.Hour).Seconds()) {
			// Search only: an unconfirmed old request is never blindly retried.
			search := &stripe.CustomerSearchParams{SearchParams: stripe.SearchParams{Query: fmt.Sprintf("metadata['lazu_project_id']:'%s'", pc.Project.PublicID)}}
			search.Context = c.Request.Context()
			iter := api.Customers.Search(search)
			for iter.Next() {
				if customerID != "" {
					projectPaymentUnavailable(c, order)
					return
				}
				customerID = iter.Customer().ID
			}
			if iter.Err() != nil || customerID == "" {
				projectPaymentUnavailable(c, order)
				return
			}
			if err := model.SetProjectStripeCustomer(pc.Project.ID, customerID); err != nil {
				projectFailure(c, err)
				return
			}
		}
		if customerID == "" {
			params := &stripe.CustomerParams{Metadata: map[string]string{"lazu_project_id": pc.Project.PublicID}}
			params.Context = c.Request.Context()
			params.SetIdempotencyKey("lazu-project-customer:" + pc.Project.PublicID)
			customer, err := api.Customers.New(params)
			if err != nil {
				projectPaymentUnavailable(c, order)
				return
			}
			customerID = customer.ID
			if err := model.SetProjectStripeCustomer(pc.Project.ID, customerID); err != nil {
				projectFailure(c, err)
				return
			}
		}
	}
	if time.Now().Unix()-order.CreateTime > int64((23 * time.Hour).Seconds()) {
		list := &stripe.CheckoutSessionListParams{Customer: stripe.String(customerID), CreatedRange: &stripe.RangeQueryParams{GreaterThanOrEqual: order.CreateTime}}
		list.Context = c.Request.Context()
		iter := api.CheckoutSessions.List(list)
		for iter.Next() {
			session := iter.CheckoutSession()
			if session.ClientReferenceID != order.TradeNo {
				continue
			}
			if session.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid || session.PaymentStatus == stripe.CheckoutSessionPaymentStatusNoPaymentRequired {
				completeProjectStripeSession(c, *session)
				if !c.IsAborted() {
					c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"top_up_id": order.PublicID, "payment_status": "paid"}})
				}
				return
			}
			if session.Status == stripe.CheckoutSessionStatusExpired {
				if err := model.ExpireProjectTopUp(order.TradeNo, false); err != nil {
					projectPaymentUnavailable(c, order)
					return
				}
				projectFailure(c, model.ErrProjectStateConflict)
				return
			}
			if session.URL != "" {
				if err := model.AttachProjectCheckout(order.Id, customerID, session.ID, session.URL); err != nil {
					projectFailure(c, err)
					return
				}
				c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"pay_link": session.URL, "top_up_id": order.PublicID}})
				return
			}
		}
		projectPaymentUnavailable(c, order)
		return
	}
	priceParams := &stripe.PriceParams{}
	priceParams.Context = c.Request.Context()
	price, err := api.Prices.Get(order.StripePriceID, priceParams)
	if err != nil || price.Currency != stripe.CurrencyUSD || price.UnitAmount != 100 || price.Recurring != nil {
		projectPaymentUnavailable(c, order)
		return
	}
	params := projectCheckoutParams(pc.Project, order, customerID, req.Surface)
	params.Context = c.Request.Context()
	result, err := api.CheckoutSessions.New(params)
	if err != nil {
		projectPaymentUnavailable(c, order)
		return
	}
	if err := model.AttachProjectCheckout(order.Id, customerID, result.ID, result.URL); err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"pay_link": result.URL, "top_up_id": order.PublicID}})
}

// projectPaymentUnavailable answers a checkout the provider could not open
// yet. The order stays; a retry with the same Idempotency-Key resumes it.
func projectPaymentUnavailable(c *gin.Context, order model.TopUp) {
	httperr.Code(c, types.ErrorCodePaymentProviderUnavailable, types.D{"top_up_id": order.PublicID})
}

// projectCheckoutReturnURLs is where Checkout sends the payer back: the
// console's payment pages, or another surface's when they paid there. Nothing
// about the surface is stored on the order; a retry of the same order must
// come from the same surface, since Stripe replays the session by its
// idempotency key.
func projectCheckoutReturnURLs(project model.Project, order model.TopUp, surface string) (string, string) {
	base := strings.TrimRight(order.StripeReturnBase, "/")
	if found, ok := model.LookupUserHomeSurface(surface); ok && found.CheckoutSuccessPath != "" {
		return base + found.CheckoutSuccessPath + "?top_up_id=" + url.QueryEscape(order.PublicID),
			base + found.CheckoutCancelPath
	}
	return base + "/payment/success?project_id=" + project.PublicID + "&top_up_id=" + order.PublicID,
		base + "/console/wallet?project_id=" + project.PublicID
}

func projectCheckoutParams(project model.Project, order model.TopUp, customerID string, surface string) *stripe.CheckoutSessionParams {
	metadata := map[string]string{"lazu_top_up_reference": order.TradeNo, "lazu_project_id": project.PublicID}
	shared := project.UpgradedAt != nil // Existing pending orders predate the snapshot column.
	if order.StripeProjectShared != nil {
		shared = *order.StripeProjectShared
	}
	projectName := order.StripeProjectName
	if order.StripeProjectShared == nil && projectName == "" {
		projectName = project.Name
	}
	invoiceDescription := "Lazu API credit"
	if shared && projectName != "" {
		invoiceDescription = "Lazu API credit for " + projectName
	}
	successURL, cancelURL := projectCheckoutReturnURLs(project, order, surface)
	params := &stripe.CheckoutSessionParams{
		ClientReferenceID: stripe.String(order.TradeNo), Customer: stripe.String(customerID), Mode: stripe.String("payment"),
		SuccessURL:               stripe.String(successURL),
		CancelURL:                stripe.String(cancelURL),
		LineItems:                []*stripe.CheckoutSessionLineItemParams{{Price: stripe.String(order.StripePriceID), Quantity: stripe.Int64(order.Amount)}},
		AllowPromotionCodes:      stripe.Bool(order.StripePromotionCodes),
		BillingAddressCollection: stripe.String("auto"), Metadata: metadata,
		InvoiceCreation:   &stripe.CheckoutSessionInvoiceCreationParams{Enabled: stripe.Bool(true), InvoiceData: &stripe.CheckoutSessionInvoiceCreationInvoiceDataParams{Metadata: metadata, Description: stripe.String(invoiceDescription)}},
		PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{Metadata: metadata},
		CustomerUpdate:    &stripe.CheckoutSessionCustomerUpdateParams{Name: stripe.String("never"), Address: stripe.String("never")},
	}
	if order.PaidByUserID == project.OwnerUserID {
		params.CustomerUpdate.Name = stripe.String("auto")
		params.CustomerUpdate.Address = stripe.String("auto")
		params.TaxIDCollection = &stripe.CheckoutSessionTaxIDCollectionParams{Enabled: stripe.Bool(true)}
	}
	params.SetIdempotencyKey("lazu-project-checkout:" + order.PublicID)
	return params
}
func ProjectStripePortal(c *gin.Context) {
	pc, ok := legacyProjectContext(c, "")
	if !ok {
		return
	}
	if pc.Member.Status != model.ProjectMemberActive || pc.Member.Role != model.ProjectRoleOwner {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	if pc.Project.StripeCustomerID == "" {
		// Distinct from a real state conflict: the project simply has no
		// Stripe customer yet because the owner has never paid. The console
		// shows a "set up billing" affordance instead of a generic error.
		httperr.Code(c, types.ErrorCodeNoBillingCustomer)
		return
	}
	api := projectStripeClient()
	configParams := projectBillingPortalConfiguration()
	configParams.Context = c.Request.Context()
	configuration, err := api.BillingPortalConfigurations.New(configParams)
	if err != nil {
		httperr.Write(c, types.WrapErr(err, types.ErrorCodePaymentProviderUnavailable))
		return
	}
	params := &stripe.BillingPortalSessionParams{Configuration: stripe.String(configuration.ID), Customer: stripe.String(pc.Project.StripeCustomerID), ReturnURL: stripe.String(projectPaymentReturnBase() + "/console/wallet?project_id=" + pc.Project.PublicID)}
	params.Context = c.Request.Context()
	result, err := api.BillingPortalSessions.New(params)
	if err != nil {
		httperr.Write(c, types.WrapErr(err, types.ErrorCodePaymentProviderUnavailable))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"url": result.URL}})
}
func projectInvoiceWebhook(c *gin.Context, event stripe.Event) {
	var invoice stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	reference := invoice.Metadata["lazu_top_up_reference"]
	if reference == "" {
		return
	}
	if invoice.Customer == nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	billTo, err := json.Marshal(gin.H{"name": invoice.CustomerName, "address": invoice.CustomerAddress, "tax_ids": invoice.CustomerTaxIDs})
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	err = model.UpdateProjectInvoice(model.ProjectInvoiceUpdate{Reference: reference, CustomerID: invoice.Customer.ID, InvoiceID: invoice.ID, Number: invoice.Number, HostedURL: invoice.HostedInvoiceURL, PDFURL: invoice.InvoicePDF,
		BillTo: model.JSONValue(billTo), Ready: event.Type == stripe.EventTypeInvoicePaid && invoice.Status == stripe.InvoiceStatusPaid && invoice.HostedInvoiceURL != "" && invoice.InvoicePDF != "", Failed: event.Type == stripe.EventTypeInvoiceFinalizationFailed})
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if event.Type == stripe.EventTypeInvoiceFinalizationFailed {
		stripeError := ""
		if invoice.LastFinalizationError != nil {
			stripeError = string(invoice.LastFinalizationError.Code)
			if invoice.LastFinalizationError.Msg != "" {
				if stripeError != "" {
					stripeError += ": "
				}
				stripeError += invoice.LastFinalizationError.Msg
			}
		}
		if err := model.QueueProjectInvoiceFailureAlert(reference, email_setting.GetEmailSetting().AdminEmail, stripeError); err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
	}
	wakeProjectNotifications()
}

func completeProjectStripeSession(c *gin.Context, session stripe.CheckoutSession) {
	if session.Customer == nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	intentID := ""
	method, receiptURL, chargeID := "", "", ""
	if session.PaymentIntent != nil {
		intentID = session.PaymentIntent.ID
		var order model.TopUp
		if err := model.CoreDB().Where("trade_no = ?", session.ClientReferenceID).First(&order).Error; err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		if order.PaymentMethod == "" || order.PaymentMethod == "stripe" {
			params := &stripe.PaymentIntentParams{}
			params.Context = c.Request.Context()
			params.AddExpand("latest_charge")
			intent, err := projectStripeClient().PaymentIntents.Get(intentID, params)
			if err != nil || intent.Customer == nil || intent.Customer.ID != session.Customer.ID || intent.Metadata["lazu_top_up_reference"] != session.ClientReferenceID || intent.LatestCharge == nil || intent.LatestCharge.Customer == nil || intent.LatestCharge.Customer.ID != session.Customer.ID || intent.LatestCharge.PaymentIntent == nil || intent.LatestCharge.PaymentIntent.ID != intentID {
				c.AbortWithStatus(http.StatusServiceUnavailable)
				return
			}
			method = projectChargePaymentMethod(intent.LatestCharge)
			receiptURL = intent.LatestCharge.ReceiptURL
			chargeID = intent.LatestCharge.ID
		}
	}
	order, credited, err := model.CompleteProjectTopUp(model.ProjectPaymentReceipt{Reference: session.ClientReferenceID, CustomerID: session.Customer.ID, SessionID: session.ID, PaymentIntentID: intentID,
		PaidMicrousd: billing.MicroUSD(session.AmountTotal * 10000), Currency: string(session.Currency), Method: method, ReceiptURL: receiptURL, ChargeID: chargeID})
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	wakeProjectNotifications()
	if credited {
		if err := scheduleProjectPaymentFallback(order.PublicID); err != nil {
			common.SysError("failed to schedule payment email wake; durable outbox will retry")
		}
	}
}
func projectChargeWebhook(c *gin.Context, event stripe.Event) {
	var charge stripe.Charge
	if err := json.Unmarshal(event.Data.Raw, &charge); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	reference := charge.Metadata["lazu_top_up_reference"]
	if reference == "" || charge.ReceiptURL == "" {
		return
	}
	if charge.Customer == nil || charge.PaymentIntent == nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	method := projectChargePaymentMethod(&charge)
	if err := model.UpdateProjectPaymentReceipt(reference, charge.Customer.ID, charge.PaymentIntent.ID, charge.ID, charge.ReceiptURL, method); err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	wakeProjectNotifications()
}

func projectChargePaymentMethod(charge *stripe.Charge) string {
	if charge.PaymentMethodDetails == nil {
		return ""
	}
	details := charge.PaymentMethodDetails
	if details.Card != nil {
		return model.ProjectPaymentMethod(string(details.Type), string(details.Card.Brand), details.Card.Last4)
	}
	return string(details.Type)
}
func projectCustomerWebhook(c *gin.Context, event stripe.Event) {
	var eventCustomer stripe.Customer
	if err := json.Unmarshal(event.Data.Raw, &eventCustomer); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	var count int64
	if err := model.CoreDB().Model(&model.Project{}).Where("stripe_customer_id = ?", eventCustomer.ID).Count(&count).Error; err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if count == 0 {
		return
	}
	// Retrieve current Stripe state rather than copying potentially stale events.
	params := &stripe.CustomerParams{}
	params.Context = c.Request.Context()
	params.AddExpand("tax_ids")
	customer, err := projectStripeClient().Customers.Get(eventCustomer.ID, params)
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	taxIDs := []gin.H{}
	if customer.TaxIDs != nil {
		for _, tax := range customer.TaxIDs.Data {
			taxIDs = append(taxIDs, gin.H{"type": tax.Type, "value": tax.Value})
		}
	}
	details, err := json.Marshal(gin.H{"name": customer.Name, "address": customer.Address, "tax_ids": taxIDs})
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if err := model.CoreDB().Model(&model.Project{}).Where("stripe_customer_id = ?", customer.ID).Update("billing_details", model.JSONValue(details)).Error; err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
	}
}

func projectBillingPortalConfiguration() *stripe.BillingPortalConfigurationParams {
	params := &stripe.BillingPortalConfigurationParams{
		Features: &stripe.BillingPortalConfigurationFeaturesParams{
			CustomerUpdate:      &stripe.BillingPortalConfigurationFeaturesCustomerUpdateParams{Enabled: stripe.Bool(true), AllowedUpdates: stripe.StringSlice([]string{"name", "address", "tax_id"})},
			InvoiceHistory:      &stripe.BillingPortalConfigurationFeaturesInvoiceHistoryParams{Enabled: stripe.Bool(true)},
			PaymentMethodUpdate: &stripe.BillingPortalConfigurationFeaturesPaymentMethodUpdateParams{Enabled: stripe.Bool(false)},
			SubscriptionCancel:  &stripe.BillingPortalConfigurationFeaturesSubscriptionCancelParams{Enabled: stripe.Bool(false)},
			SubscriptionUpdate:  &stripe.BillingPortalConfigurationFeaturesSubscriptionUpdateParams{Enabled: stripe.Bool(false)},
		}, LoginPage: &stripe.BillingPortalConfigurationLoginPageParams{Enabled: stripe.Bool(false)},
	}
	params.SetIdempotencyKey("lazu-project-billing-portal-v1")
	return params
}
