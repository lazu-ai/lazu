package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
)

func ReconcileProjectPaymentDocument(ctx context.Context, api *client.API, order model.TopUp) error {
	params := &stripe.CheckoutSessionParams{}
	params.Context = ctx
	session, err := api.CheckoutSessions.Get(order.StripeCheckoutSessionID, params)
	if err != nil {
		return err
	}
	if session.ClientReferenceID != order.TradeNo || session.Customer == nil || session.Customer.ID != order.StripeCustomerID ||
		(session.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid && session.PaymentStatus != stripe.CheckoutSessionPaymentStatusNoPaymentRequired) {
		return errors.New("Stripe Checkout session does not match the paid order")
	}
	var failures []error
	if order.InvoiceStatus != "ready" && session.Invoice != nil {
		invoiceParams := &stripe.InvoiceParams{}
		invoiceParams.Context = ctx
		invoice, invoiceErr := api.Invoices.Get(session.Invoice.ID, invoiceParams)
		if invoiceErr != nil {
			failures = append(failures, invoiceErr)
		} else if invoice.Customer == nil || invoice.Customer.ID != order.StripeCustomerID || invoice.Metadata["lazu_top_up_reference"] != order.TradeNo {
			failures = append(failures, errors.New("Stripe invoice does not match the paid order"))
		} else if invoice.Status != stripe.InvoiceStatusPaid && invoice.LastFinalizationError != nil {
			stripeError := string(invoice.LastFinalizationError.Code)
			if invoice.LastFinalizationError.Msg != "" {
				if stripeError != "" {
					stripeError += ": "
				}
				stripeError += invoice.LastFinalizationError.Msg
			}
			if updateErr := model.UpdateProjectInvoice(model.ProjectInvoiceUpdate{Reference: order.TradeNo, CustomerID: order.StripeCustomerID, InvoiceID: invoice.ID, Failed: true}); updateErr != nil {
				failures = append(failures, updateErr)
			} else if alertErr := model.QueueProjectInvoiceFailureAlert(order.TradeNo, email_setting.GetEmailSetting().AdminEmail, stripeError); alertErr != nil {
				failures = append(failures, alertErr)
			}
			failures = append(failures, fmt.Errorf("Stripe invoice finalization failed: %s", stripeError))
		} else if invoice.Status == stripe.InvoiceStatusPaid && invoice.HostedInvoiceURL != "" && invoice.InvoicePDF != "" && invoice.Number != "" {
			billTo, marshalErr := json.Marshal(map[string]any{"name": invoice.CustomerName, "address": invoice.CustomerAddress, "tax_ids": invoice.CustomerTaxIDs})
			if marshalErr != nil {
				failures = append(failures, marshalErr)
			} else if updateErr := model.UpdateProjectInvoice(model.ProjectInvoiceUpdate{Reference: order.TradeNo, CustomerID: order.StripeCustomerID, InvoiceID: invoice.ID,
				Number: invoice.Number, HostedURL: invoice.HostedInvoiceURL, PDFURL: invoice.InvoicePDF, BillTo: model.JSONValue(billTo), Ready: true}); updateErr != nil {
				failures = append(failures, updateErr)
			}
		}
	}
	if order.StripeReceiptURL == "" && session.PaymentIntent != nil {
		intentParams := &stripe.PaymentIntentParams{}
		intentParams.Context = ctx
		intentParams.AddExpand("latest_charge")
		intent, intentErr := api.PaymentIntents.Get(session.PaymentIntent.ID, intentParams)
		if intentErr != nil {
			failures = append(failures, intentErr)
		} else if intent.Customer == nil || intent.Customer.ID != order.StripeCustomerID || intent.Metadata["lazu_top_up_reference"] != order.TradeNo {
			failures = append(failures, errors.New("Stripe payment intent does not match the paid order"))
		} else if intent.LatestCharge != nil && intent.LatestCharge.ReceiptURL != "" {
			charge := intent.LatestCharge
			if charge.Customer == nil || charge.Customer.ID != order.StripeCustomerID || charge.PaymentIntent == nil || charge.PaymentIntent.ID != intent.ID {
				failures = append(failures, errors.New("Stripe charge does not match the paid order"))
			} else {
				method := ""
				if charge.PaymentMethodDetails != nil {
					method = string(charge.PaymentMethodDetails.Type)
					if card := charge.PaymentMethodDetails.Card; card != nil {
						method = model.ProjectPaymentMethod(method, string(card.Brand), card.Last4)
					}
				}
				if updateErr := model.UpdateProjectPaymentReceipt(order.TradeNo, order.StripeCustomerID, intent.ID, charge.ID, charge.ReceiptURL, method); updateErr != nil {
					failures = append(failures, updateErr)
				}
			}
		}
	}
	return errors.Join(failures...)
}

// ReconcileProjectPaymentDocumentByID is the administrator's unlimited-age repair path.
func ReconcileProjectPaymentDocumentByID(ctx context.Context, id string) error {
	var order model.TopUp
	if err := model.CoreDB().Where("public_id = ? AND project_id > 0 AND status = ?", id, "success").First(&order).Error; err != nil {
		return err
	}
	if order.InvoiceStatus == "legacy" {
		return errors.New("legacy payment has no Stripe invoice to reconcile")
	}
	if !strings.HasPrefix(setting.StripeApiSecret, "sk_") && !strings.HasPrefix(setting.StripeApiSecret, "rk_") {
		return errors.New("Stripe secret is unavailable")
	}
	api := &client.API{}
	api.Init(setting.StripeApiSecret, nil)
	if err := ReconcileProjectPaymentDocument(ctx, api, order); err != nil {
		return err
	}
	if err := model.CoreDB().First(&order, order.Id).Error; err != nil {
		return err
	}
	if order.InvoiceStatus != "ready" || order.StripeInvoicePDF == "" || (order.PaidAmount > 0 && order.StripeReceiptURL == "") {
		return errors.New("Stripe documents are not ready yet")
	}
	returnBase := strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))
	if returnBase == "" {
		returnBase = setting.StripeReturnBase
	}
	return model.QueueProjectPaymentEmailForTopUp(order.Id, returnBase)
}
