package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/stripe/stripe-go/v81"
	"gorm.io/gorm"
)

// Auto-reload is the owner's: only the project owner reads or changes it,
// and only the owner's card is saved and charged. Members never see it.

type autoReloadSettingsRequest struct {
	Enabled       *bool `json:"enabled"`
	ThresholdUSD  int64 `json:"threshold_usd"`
	AmountUSD     int64 `json:"amount_usd"`
	MonthlyCapUSD int64 `json:"monthly_cap_usd"`
}

func (r autoReloadSettingsRequest) settings() model.ProjectAutoReloadSettings {
	return model.ProjectAutoReloadSettings{ThresholdUSD: r.ThresholdUSD, AmountUSD: r.AmountUSD, MonthlyCapUSD: r.MonthlyCapUSD}
}

func (r autoReloadSettingsRequest) empty() bool {
	return r.ThresholdUSD == 0 && r.AmountUSD == 0 && r.MonthlyCapUSD == 0
}

func projectAutoReloadOwner(c *gin.Context) (middleware.ProjectContext, bool) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || pc.Member.Status != model.ProjectMemberActive || pc.Member.Role != model.ProjectRoleOwner {
		projectFailure(c, model.ErrProjectForbidden)
		return pc, false
	}
	return pc, true
}

// autoReloadSetupPendingWindow is how long a card save that has not come
// back is still shown as "waiting for Stripe"; Checkout sessions expire in
// 24 hours.
const autoReloadSetupPendingWindow = 24 * time.Hour

func projectAutoReloadPayload(project model.Project, row model.ProjectAutoReload, configured bool) (gin.H, error) {
	now := time.Now().UTC()
	spent, err := model.ProjectAutoReloadMonthSpentUSD(project.ID, now)
	if err != nil {
		return nil, err
	}
	var card interface{}
	if row.StripePaymentMethodID != "" {
		card = gin.H{"brand": row.CardBrand, "last4": row.CardLast4, "label": row.CardLabel()}
	}
	var lastReload interface{}
	var order model.TopUp
	err = model.CoreDB().Where("project_id = ? AND auto_reload = ? AND status = ?", project.ID, true, common.TopUpStatusSuccess).Order("complete_time DESC, id DESC").First(&order).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil {
		lastReload = gin.H{"id": order.PublicID, "amount_usd": order.Amount, "completed_at": order.CompleteTime}
	}
	return gin.H{
		"configured": configured, "enabled": row.Enabled,
		"threshold_usd": row.ThresholdUSD, "amount_usd": row.AmountUSD, "monthly_cap_usd": row.MonthlyCapUSD,
		"card": card, "setup_pending": row.SetupSessionID != "" && now.Unix()-row.SetupStartedAt < int64(autoReloadSetupPendingWindow/time.Second),
		"paused_reason": row.PausedReason, "paused_at": row.PausedAt, "last_failure": row.LastFailure, "last_failed_at": row.LastFailedAt,
		"last_run_at": row.LastRunAt, "last_charge_at": row.LastChargeAt, "last_reload": lastReload,
		"month_spent_usd": spent, "min_amount_usd": getStripeMinTopup(), "max_amount_usd": getStripeMaxTopup(),
	}, nil
}

func respondProjectAutoReload(c *gin.Context, project model.Project, row model.ProjectAutoReload, configured bool) {
	payload, err := projectAutoReloadPayload(project, row, configured)
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": payload})
}

// GetProjectAutoReload reads the owner's auto-reload. A card save that has
// not reported back yet is checked with Stripe here, so the console shows
// the card as soon as the owner returns even when the webhook is late.
func GetProjectAutoReload(c *gin.Context) {
	pc, ok := projectAutoReloadOwner(c)
	if !ok {
		return
	}
	row, configured, err := model.GetProjectAutoReload(pc.Project.ID)
	if err != nil {
		projectFailure(c, err)
		return
	}
	if row.SetupSessionID != "" && service.StripeSecretUsable() {
		if completed, applied, err := confirmProjectAutoReloadSession(c, pc.Project, row.SetupSessionID); err == nil && applied {
			row = completed
		}
	}
	respondProjectAutoReload(c, pc.Project, row, configured)
}

// UpdateProjectAutoReload saves the three numbers and, when the body says
// so, turns reloads on (needs a saved card) or off.
func UpdateProjectAutoReload(c *gin.Context) {
	pc, ok := projectAutoReloadOwner(c)
	if !ok {
		return
	}
	var req autoReloadSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "amount_usd")
		return
	}
	row, err := model.SaveProjectAutoReloadSettings(pc.Project.ID, req.settings(), req.Enabled)
	if err != nil {
		projectFailure(c, err)
		return
	}
	respondProjectAutoReload(c, pc.Project, row, true)
}

// DisableProjectAutoReload turns reloads off; the card stays on file.
func DisableProjectAutoReload(c *gin.Context) {
	pc, ok := projectAutoReloadOwner(c)
	if !ok {
		return
	}
	row, err := model.DisableProjectAutoReload(pc.Project.ID)
	if err != nil {
		projectFailure(c, err)
		return
	}
	respondProjectAutoReload(c, pc.Project, row, true)
}

// StartProjectAutoReloadSetup opens the Stripe page that saves a card. It
// is both "turn on" and "change card": the numbers in the body are stored
// now, the card lands through the webhook or the confirm call, and only
// then do reloads run. Nothing is charged on this page.
func StartProjectAutoReloadSetup(c *gin.Context) {
	pc, ok := projectAutoReloadOwner(c)
	if !ok {
		return
	}
	var req autoReloadSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "amount_usd")
		return
	}
	settings := req.settings()
	if req.empty() {
		existing, configured, err := model.GetProjectAutoReload(pc.Project.ID)
		if err != nil {
			projectFailure(c, err)
			return
		}
		if !configured {
			projectFailure(c, model.ProjectAutoReloadSettings{}.Validate())
			return
		}
		settings = model.ProjectAutoReloadSettings{ThresholdUSD: existing.ThresholdUSD, AmountUSD: existing.AmountUSD, MonthlyCapUSD: existing.MonthlyCapUSD}
	}
	row, err := model.BeginProjectAutoReloadSetup(pc.Project.ID, settings, time.Now())
	if err != nil {
		projectFailure(c, err)
		return
	}
	if !service.StripeSecretUsable() {
		httperr.Code(c, types.ErrorCodePaymentProviderUnavailable)
		return
	}
	api := projectStripeClient()
	customerID, err := ensureProjectStripeCustomer(c.Request.Context(), api, pc.Project)
	if errors.Is(err, errProjectStripeUnavailable) {
		httperr.Code(c, types.ErrorCodePaymentProviderUnavailable)
		return
	}
	if err != nil {
		projectFailure(c, err)
		return
	}
	base := projectPaymentReturnBase()
	wallet := base + "/console/wallet?project_id=" + url.QueryEscape(pc.Project.PublicID)
	metadata := map[string]string{"lazu_project_id": pc.Project.PublicID, service.AutoReloadMetadataKey: "setup"}
	params := &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModeSetup)), Customer: stripe.String(customerID),
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		// Stripe fills the placeholder; it must not be URL-escaped.
		SuccessURL:        stripe.String(wallet + "&auto_reload=saved&session_id={CHECKOUT_SESSION_ID}"),
		CancelURL:         stripe.String(wallet + "&auto_reload=canceled"),
		ClientReferenceID: stripe.String("auto-reload:" + pc.Project.PublicID),
		Metadata:          metadata,
		SetupIntentData:   &stripe.CheckoutSessionSetupIntentDataParams{Metadata: metadata, Description: stripe.String("Lazu auto-reload card")},
	}
	params.Context = c.Request.Context()
	params.SetIdempotencyKey("lazu-auto-reload-setup:" + pc.Project.PublicID + ":" + strconv.FormatInt(row.SetupStartedAt, 10))
	session, err := api.CheckoutSessions.New(params)
	if err != nil || session.URL == "" {
		httperr.Code(c, types.ErrorCodePaymentProviderUnavailable)
		return
	}
	if err := model.AttachProjectAutoReloadSetupSession(pc.Project.ID, session.ID); err != nil {
		projectFailure(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"setup_link": session.URL, "session_id": session.ID}})
}

// ConfirmProjectAutoReloadSetup is called by the console on return from
// Stripe with the session it came back from, so the saved card shows at
// once; the webhook settles the same session when the owner never returns.
func ConfirmProjectAutoReloadSetup(c *gin.Context) {
	pc, ok := projectAutoReloadOwner(c)
	if !ok {
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SessionID == "" {
		httperr.Invalid(c, "session_id")
		return
	}
	if !service.StripeSecretUsable() {
		httperr.Code(c, types.ErrorCodePaymentProviderUnavailable)
		return
	}
	row, _, err := confirmProjectAutoReloadSession(c, pc.Project, req.SessionID)
	if errors.Is(err, errProjectStripeUnavailable) {
		httperr.Code(c, types.ErrorCodePaymentProviderUnavailable)
		return
	}
	if err != nil {
		projectFailure(c, err)
		return
	}
	respondProjectAutoReload(c, pc.Project, row, true)
}

// confirmProjectAutoReloadSession reads a setup session back from Stripe and
// stores its card when it belongs to this project. A session that is not
// complete yet changes nothing and returns the current row.
func confirmProjectAutoReloadSession(c *gin.Context, project model.Project, sessionID string) (model.ProjectAutoReload, bool, error) {
	api := projectStripeClient()
	params := &stripe.CheckoutSessionParams{}
	params.Context = c.Request.Context()
	params.AddExpand("setup_intent.payment_method")
	session, err := api.CheckoutSessions.Get(sessionID, params)
	if err != nil {
		return model.ProjectAutoReload{}, false, errProjectStripeUnavailable
	}
	if session.Metadata["lazu_project_id"] != project.PublicID || session.Customer == nil || session.Customer.ID != project.StripeCustomerID {
		return model.ProjectAutoReload{}, false, model.ErrProjectStateConflict
	}
	row, applied, err := service.CompleteAutoReloadSetupSession(c.Request.Context(), api, session)
	if err != nil {
		return model.ProjectAutoReload{}, false, err
	}
	if !applied {
		current, _, err := model.GetProjectAutoReload(project.ID)
		return current, false, err
	}
	return row, true, nil
}

// projectAutoReloadSetupWebhook stores the card a setup-mode Checkout
// Session saved. Sessions without the auto-reload mark are not ours.
func projectAutoReloadSetupWebhook(c *gin.Context, event stripe.Event) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if session.Metadata[service.AutoReloadMetadataKey] == "" {
		return
	}
	if _, _, err := service.CompleteAutoReloadSetupSession(c.Request.Context(), projectStripeClient(), &session); err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
	}
}

// projectAutoReloadSetupIntentWebhook is the second route a saved card can
// take; it matters when the session event was lost.
func projectAutoReloadSetupIntentWebhook(c *gin.Context, event stripe.Event) {
	var eventIntent stripe.SetupIntent
	if err := json.Unmarshal(event.Data.Raw, &eventIntent); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if eventIntent.Metadata[service.AutoReloadMetadataKey] == "" || eventIntent.Customer == nil {
		return
	}
	var project model.Project
	if err := model.CoreDB().Where("public_id = ?", eventIntent.Metadata["lazu_project_id"]).First(&project).Error; err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if project.StripeCustomerID != eventIntent.Customer.ID {
		return
	}
	params := &stripe.SetupIntentParams{}
	params.Context = c.Request.Context()
	params.AddExpand("payment_method")
	intent, err := projectStripeClient().SetupIntents.Get(eventIntent.ID, params)
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if _, _, err := service.CompleteAutoReloadSetupIntent(project, eventIntent.Customer.ID, "", intent); err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
	}
}

// projectAutoReloadIntentWebhook settles a reload the worker did not get to
// finish: a succeeded intent credits the wallet, a failed one pauses
// reloads. Checkout's intents carry the same reference key and are skipped;
// their session event settles them.
func projectAutoReloadIntentWebhook(c *gin.Context, event stripe.Event) {
	var eventIntent stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &eventIntent); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if eventIntent.Metadata[service.AutoReloadMetadataKey] == "" || eventIntent.Metadata["lazu_top_up_reference"] == "" {
		return
	}
	// Read the current intent rather than the event's copy: the receipt and
	// card come with the charge, and a late event must not undo a newer state.
	params := &stripe.PaymentIntentParams{}
	params.Context = c.Request.Context()
	params.AddExpand("latest_charge")
	intent, err := projectStripeClient().PaymentIntents.Get(eventIntent.ID, params)
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	var order model.TopUp
	if err := model.CoreDB().Where("trade_no = ?", intent.Metadata["lazu_top_up_reference"]).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if order.StripePaymentIntentID == "" {
		if err := model.RecordProjectAutoReloadIntent(order.Id, intent.ID); err != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
	}
	credited, err := service.SettleAutoReloadIntent(intent, time.Now().UTC())
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
