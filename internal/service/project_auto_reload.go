package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
)

// Auto-reload charges a saved card off-session when a wallet runs low. The
// Stripe objects flow: a Checkout Session in setup mode saves a card (a
// PaymentMethod on the project's Customer) → the worker opens a pending
// top_ups order and confirms a PaymentIntent with that PaymentMethod,
// off_session → a succeeded intent settles through CompleteProjectTopUp,
// so the first-top-up bonus, referral rewards and the payment mail are the
// same as for a card payment at checkout → a declined intent fails the
// order, pauses reloads and mails the owner once.
//
// Nothing here runs on the request path; the worker and the Stripe webhook
// are the only callers.

const (
	// AutoReloadMetadataKey marks Stripe objects auto-reload created, so the
	// Checkout webhooks leave them alone and vice versa.
	AutoReloadMetadataKey = "lazu_auto_reload"

	// autoReloadInFlightGrace is how long a pending reload is trusted to the
	// webhook before the worker asks Stripe what became of it.
	autoReloadInFlightGrace = 30 * time.Minute

	autoReloadBatch = 200
)

// AutoReloadReturnBase is the console origin payment mails link to.
func AutoReloadReturnBase() string {
	if frontend := strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL")), "/"); frontend != "" {
		return frontend
	}
	if setting.StripeReturnBase != "" {
		return setting.StripeReturnBase
	}
	return common.SiteWebOrigin()
}

// StripeSecretUsable says whether a Stripe call can be made at all.
func StripeSecretUsable() bool {
	return strings.HasPrefix(setting.StripeApiSecret, "sk_") || strings.HasPrefix(setting.StripeApiSecret, "rk_")
}

// RunProjectAutoReload is one worker pass: every due row is read once, and
// a failure on one project never stops the others.
func RunProjectAutoReload(ctx context.Context, api *client.API, now time.Time) error {
	if model.CoreDB() == nil {
		return nil
	}
	rows, err := model.ClaimProjectAutoReloadDue(now, autoReloadBatch)
	if err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := ProcessProjectAutoReload(ctx, api, row, now); err != nil {
			failures = append(failures, fmt.Errorf("auto-reload project %d: %w", row.ProjectID, err))
		}
	}
	return errors.Join(failures...)
}

// ProcessProjectAutoReload decides for one project: settle anything still
// in flight, then charge only when the balance is under the threshold and
// the month's cap leaves room. Every decision that changes state is
// recorded before Stripe is called.
func ProcessProjectAutoReload(ctx context.Context, api *client.API, row model.ProjectAutoReload, now time.Time) error {
	if pending, found, err := model.FindPendingProjectAutoReloadTopUp(row.ProjectID); err != nil {
		return err
	} else if found {
		return reconcileAutoReloadOrder(ctx, api, pending, now)
	}
	var project model.Project
	if err := model.CoreDB().First(&project, row.ProjectID).Error; err != nil {
		return err
	}
	var owner model.User
	if err := model.CoreDB().Select("id", "quota").First(&owner, project.OwnerUserID).Error; err != nil {
		return err
	}
	balance := model.ReadConsoleWalletBalance(ctx, owner.Id, owner.Quota)
	if balance >= row.ThresholdMicrousd() {
		return nil
	}
	amount := model.ClampAutoReloadAmount(row.AmountUSD)
	spent, err := model.ProjectAutoReloadMonthSpentUSD(row.ProjectID, now)
	if err != nil {
		return err
	}
	if spent+amount > row.MonthlyCapUSD {
		if row.PausedReason != model.AutoReloadPauseCapReached {
			return model.SetProjectAutoReloadPause(row.ProjectID, model.AutoReloadPauseCapReached, now)
		}
		return nil
	}
	if row.PausedReason == model.AutoReloadPauseCapReached {
		// A new month, or a smaller amount: the cap has room again.
		if err := model.SetProjectAutoReloadPause(row.ProjectID, "", now); err != nil {
			return err
		}
	}
	if api == nil {
		return errors.New("Stripe client is unavailable for auto-reload")
	}
	order, err := model.BeginProjectAutoReloadTopUp(row.ProjectID, amount, AutoReloadReturnBase(), now)
	if errors.Is(err, model.ErrAutoReloadInFlight) {
		return nil
	}
	if err != nil {
		return err
	}
	return chargeAutoReloadOrder(ctx, api, row, project, order, now)
}

func autoReloadIntentParams(ctx context.Context, row model.ProjectAutoReload, project model.Project, order model.TopUp) *stripe.PaymentIntentParams {
	description := "Lazu API credit (auto-reload)"
	if project.UpgradedAt != nil && project.Name != "" {
		description = "Lazu API credit for " + project.Name + " (auto-reload)"
	}
	params := &stripe.PaymentIntentParams{
		Amount: stripe.Int64(order.Amount * 100), Currency: stripe.String(string(stripe.CurrencyUSD)),
		Customer: stripe.String(project.StripeCustomerID), PaymentMethod: stripe.String(row.StripePaymentMethodID),
		OffSession: stripe.Bool(true), Confirm: stripe.Bool(true), ErrorOnRequiresAction: stripe.Bool(true),
		Description: stripe.String(description),
		Metadata:    map[string]string{"lazu_top_up_reference": order.TradeNo, "lazu_project_id": project.PublicID, AutoReloadMetadataKey: "charge"},
	}
	params.Context = ctx
	params.AddExpand("latest_charge")
	// One intent per order however often the call is retried: a timeout
	// after Stripe charged the card must not charge it again.
	params.SetIdempotencyKey("lazu-auto-reload:" + order.PublicID)
	return params
}

func chargeAutoReloadOrder(ctx context.Context, api *client.API, row model.ProjectAutoReload, project model.Project, order model.TopUp, now time.Time) error {
	intent, err := api.PaymentIntents.New(autoReloadIntentParams(ctx, row, project, order))
	if err != nil {
		var stripeErr *stripe.Error
		if errors.As(err, &stripeErr) {
			if stripeErr.PaymentIntent != nil && stripeErr.PaymentIntent.ID != "" {
				if recordErr := model.RecordProjectAutoReloadIntent(order.Id, stripeErr.PaymentIntent.ID); recordErr != nil {
					return recordErr
				}
			}
			if code, declined := autoReloadDecline(stripeErr); declined {
				_, failErr := model.FailProjectAutoReloadTopUp(order.TradeNo, code, stripeErr.Msg, now)
				return failErr
			}
		}
		// Anything else (network, Stripe 5xx, a misconfigured key) leaves the
		// order pending; the next pass asks Stripe what happened to it.
		return err
	}
	if err := model.RecordProjectAutoReloadIntent(order.Id, intent.ID); err != nil {
		return err
	}
	_, err = SettleAutoReloadIntent(intent, now)
	return err
}

// autoReloadDecline says whether a Stripe error is the card's answer (so the
// owner must act) rather than Lazu's or Stripe's problem (so a retry may
// succeed). The code the mail explains is the bank's decline code when
// there is one.
func autoReloadDecline(err *stripe.Error) (string, bool) {
	if err == nil {
		return "", false
	}
	code := string(err.Code)
	if err.DeclineCode != "" {
		code = string(err.DeclineCode)
	}
	switch {
	case err.Type == stripe.ErrorTypeCard:
		return code, true
	case err.Code == stripe.ErrorCodeAuthenticationRequired, err.Code == stripe.ErrorCodeCardDeclined, err.Code == stripe.ErrorCodeExpiredCard,
		err.Code == stripe.ErrorCodeIncorrectCVC, err.Code == stripe.ErrorCodeInsufficientFunds, err.Code == stripe.ErrorCodePaymentIntentAuthenticationFailure,
		err.Code == stripe.ErrorCodePaymentMethodUnactivated, err.Code == stripe.ErrorCodeResourceMissing && err.Param == "payment_method":
		return code, true
	}
	return code, false
}

// SettleAutoReloadIntent brings an order in line with its PaymentIntent.
// A succeeded intent credits the wallet; a final failure pauses reloads;
// anything in between is left for a later event. Intents that are not
// auto-reload's (Checkout's carry the same reference key) are ignored, and
// the result says whether the wallet was credited by this call.
func SettleAutoReloadIntent(intent *stripe.PaymentIntent, now time.Time) (bool, error) {
	if intent == nil || intent.Metadata[AutoReloadMetadataKey] == "" {
		return false, nil
	}
	reference := intent.Metadata["lazu_top_up_reference"]
	if reference == "" {
		return false, nil
	}
	var order model.TopUp
	if err := model.CoreDB().Where("trade_no = ?", reference).First(&order).Error; err != nil {
		return false, err
	}
	if !order.AutoReload {
		return false, nil
	}
	if intent.Customer == nil || intent.Customer.ID != order.StripeCustomerID {
		return false, model.ErrProjectStateConflict
	}
	switch intent.Status {
	case stripe.PaymentIntentStatusSucceeded:
		if intent.Amount > order.Amount*100 || strings.ToLower(string(intent.Currency)) != "usd" {
			return false, model.ErrProjectStateConflict
		}
		receipt := model.ProjectPaymentReceipt{Reference: reference, CustomerID: intent.Customer.ID, PaymentIntentID: intent.ID,
			PaidMicrousd: billing.MicroUSD(intent.Amount * 10000), Currency: "USD"}
		if charge := intent.LatestCharge; charge != nil && charge.PaymentIntent != nil && charge.PaymentIntent.ID == intent.ID {
			receipt.ChargeID, receipt.ReceiptURL = charge.ID, charge.ReceiptURL
			if charge.PaymentMethodDetails != nil && charge.PaymentMethodDetails.Card != nil {
				receipt.Method = model.ProjectPaymentMethod("card", string(charge.PaymentMethodDetails.Card.Brand), charge.PaymentMethodDetails.Card.Last4)
			}
		}
		_, credited, err := model.CompleteProjectTopUp(receipt)
		return credited, err
	case stripe.PaymentIntentStatusCanceled, stripe.PaymentIntentStatusRequiresPaymentMethod:
		code, message := "card_declined", ""
		if intent.LastPaymentError != nil {
			code, _ = autoReloadDecline(intent.LastPaymentError)
			message = intent.LastPaymentError.Msg
		}
		_, err := model.FailProjectAutoReloadTopUp(reference, code, message, now)
		return false, err
	}
	return false, nil
}

// reconcileAutoReloadOrder asks Stripe about a reload that has been pending
// longer than the webhook should take. An order Stripe never heard of is
// expired so the next pass may try again; one it knows is settled.
func reconcileAutoReloadOrder(ctx context.Context, api *client.API, order model.TopUp, now time.Time) error {
	if now.Unix()-order.CreateTime < int64(autoReloadInFlightGrace/time.Second) {
		return nil
	}
	if api == nil {
		return errors.New("Stripe client is unavailable for auto-reload reconciliation")
	}
	if order.StripePaymentIntentID != "" {
		params := &stripe.PaymentIntentParams{}
		params.Context = ctx
		params.AddExpand("latest_charge")
		intent, err := api.PaymentIntents.Get(order.StripePaymentIntentID, params)
		if err != nil {
			return err
		}
		_, err = SettleAutoReloadIntent(intent, now)
		return err
	}
	search := &stripe.PaymentIntentSearchParams{SearchParams: stripe.SearchParams{Query: fmt.Sprintf("metadata['lazu_top_up_reference']:'%s'", order.TradeNo)}}
	search.Context = ctx
	search.AddExpand("data.latest_charge")
	iter := api.PaymentIntents.Search(search)
	var found *stripe.PaymentIntent
	for iter.Next() {
		if found != nil {
			return fmt.Errorf("auto-reload order %s has more than one PaymentIntent", order.PublicID)
		}
		found = iter.PaymentIntent()
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if found == nil {
		return model.ExpireProjectTopUp(order.TradeNo, false)
	}
	if err := model.RecordProjectAutoReloadIntent(order.Id, found.ID); err != nil {
		return err
	}
	_, err := SettleAutoReloadIntent(found, now)
	return err
}

// CompleteAutoReloadSetupSession stores the card a setup-mode Checkout
// Session saved. The webhook and the console's confirm call both end here;
// the second arrival changes nothing.
func CompleteAutoReloadSetupSession(ctx context.Context, api *client.API, session *stripe.CheckoutSession) (model.ProjectAutoReload, bool, error) {
	if session == nil || session.Mode != stripe.CheckoutSessionModeSetup || session.Metadata[AutoReloadMetadataKey] == "" {
		return model.ProjectAutoReload{}, false, nil
	}
	if session.Status != stripe.CheckoutSessionStatusComplete || session.Customer == nil || session.SetupIntent == nil {
		return model.ProjectAutoReload{}, false, nil
	}
	var project model.Project
	if err := model.CoreDB().Where("public_id = ?", session.Metadata["lazu_project_id"]).First(&project).Error; err != nil {
		return model.ProjectAutoReload{}, false, err
	}
	setupIntent := session.SetupIntent
	if setupIntent.PaymentMethod == nil || setupIntent.PaymentMethod.Card == nil {
		if api == nil {
			return model.ProjectAutoReload{}, false, errors.New("Stripe client is unavailable to read the saved card")
		}
		params := &stripe.SetupIntentParams{}
		params.Context = ctx
		params.AddExpand("payment_method")
		refreshed, err := api.SetupIntents.Get(setupIntent.ID, params)
		if err != nil {
			return model.ProjectAutoReload{}, false, err
		}
		setupIntent = refreshed
	}
	return CompleteAutoReloadSetupIntent(project, session.Customer.ID, session.ID, setupIntent)
}

// CompleteAutoReloadSetupIntent is the last step of a card save, shared by
// the session path and the setup_intent.succeeded event. Only a card the
// project's own Customer holds is accepted.
func CompleteAutoReloadSetupIntent(project model.Project, customerID, sessionID string, setupIntent *stripe.SetupIntent) (model.ProjectAutoReload, bool, error) {
	if setupIntent == nil || setupIntent.Status != stripe.SetupIntentStatusSucceeded || setupIntent.PaymentMethod == nil {
		return model.ProjectAutoReload{}, false, nil
	}
	if setupIntent.Customer != nil && setupIntent.Customer.ID != customerID {
		return model.ProjectAutoReload{}, false, model.ErrProjectStateConflict
	}
	method := setupIntent.PaymentMethod
	if method.Type != stripe.PaymentMethodTypeCard || method.Card == nil {
		return model.ProjectAutoReload{}, false, model.ErrProjectInvalidInput
	}
	return model.CompleteProjectAutoReloadSetup(model.ProjectAutoReloadCard{ProjectID: project.ID, CustomerID: customerID, SessionID: sessionID,
		PaymentMethodID: method.ID, Brand: string(method.Card.Brand), Last4: method.Card.Last4})
}
