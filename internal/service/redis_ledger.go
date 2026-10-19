package service

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/requestpath"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// AdmissionPausedRetryAfterSeconds is the Retry-After hint while the ledger
// maintenance key pauses all admission (deploy quiesce, recovery, backup).
// That pause lasts minutes, not the milliseconds of a payer fence: a one-second
// hint spends an SDK's whole retry budget (openai-node: 3 attempts in ~5s)
// inside it. SDKs honor hints up to 60s, so two retries span ~a minute.
const AdmissionPausedRetryAfterSeconds = 30

func admissionRetryAfter(err error) string {
	if errors.Is(err, ledger.ErrAdmissionPaused) {
		return strconv.Itoa(AdmissionPausedRetryAfterSeconds)
	}
	return "1"
}

func reserveRedisLedger(c *gin.Context, amount billing.MicroUSD, info *relaycommon.RelayInfo) (admissionError *types.LazuError) {
	return reserveRedisLedgerWithGateway(c, amount, info, requestpath.Current())
}

// reserveRedisLedgerEstimate reserves a worst-case token estimate. A positive
// Key remainder or wallet balance below the estimate is held in full instead
// of locking the caller out; settlement records the actual charge.
func reserveRedisLedgerEstimate(c *gin.Context, amount billing.MicroUSD, info *relaycommon.RelayInfo) (admissionError *types.LazuError) {
	return reserveRedisLedgerAttempt(c, amount, info, requestpath.Current(), true, true)
}

func reserveRedisLedgerWithGateway(c *gin.Context, amount billing.MicroUSD, info *relaycommon.RelayInfo, gateway requestpath.Gateway) (admissionError *types.LazuError) {
	return reserveRedisLedgerAttempt(c, amount, info, gateway, true, false)
}

func reserveRedisLedgerAttempt(c *gin.Context, amount billing.MicroUSD, info *relaycommon.RelayInfo, gateway requestpath.Gateway, mayReload bool, clampToRemaining bool) (admissionError *types.LazuError) {
	defer func() {
		if admissionError != nil && admissionError.StatusCode == http.StatusServiceUnavailable {
			c.Header("Retry-After", admissionRetryAfter(admissionError.Err))
		}
	}()
	var held *ledger.Input
	if call := common.InternalRelayCallStateFromContext(c.Request.Context()); call != nil && call.Reservation != nil {
		held = call.Reservation
		info.RequestID, info.BudgetSettlementAt = held.RequestID, held.OccurredAt
		amount = billing.MicroUSD(held.Amount)
	}
	if info.RequestID == "" {
		info.RequestID = common.GenerateRequestID()
	}
	if info.BudgetSettlementAt.IsZero() {
		info.BudgetSettlementAt = time.Now().UTC()
	}
	var snapshot *model.IdentitySnapshot
	if value, ok := c.Get(model.IdentitySnapshotContextKey); ok {
		snapshot, _ = value.(*model.IdentitySnapshot)
	}
	if snapshot == nil {
		var err error
		snapshot, err = gateway.Identity(c.Request.Context(), info.TokenKey)
		if err != nil {
			return redisAdmissionError(err, nil)
		}
	}
	if err := snapshot.Validate(time.Now()); err != nil {
		return redisAdmissionError(err, snapshot)
	}
	if err := bindLedgerSnapshotToken(snapshot, info); err != nil {
		return redisAdmissionError(err, snapshot)
	}
	info.ProjectID = snapshot.Principal.ProjectID
	info.PayerUserID = snapshot.Principal.PayerUserID
	info.ActorUserID = snapshot.Principal.ActorUserID
	input := ledger.Input{SessionID: info.SessionID, AgentID: info.AgentID, ReferenceRequestID: info.RequestID, RequestID: info.RequestID, PayerID: info.BillingUserID(), ActorID: info.ActorID(), ProjectID: info.ProjectID, TokenID: snapshot.Token.Id, Unlimited: snapshot.Token.UnlimitedQuota, Amount: int64(amount), PricingVersion: info.PriceData.PricingVersion, Model: info.OriginModelName, OccurredAt: info.BudgetSettlementAt, Budgets: model.LedgerBudgets(snapshot.Budgets, info.BudgetSettlementAt)}
	input.ExpiresAt = time.Now().Add(45 * time.Minute).UnixMilli()
	input.AllowZeroBalance = info.PriceData.FreeModel && amount == 0
	// A held reservation was already admitted; its amount is exact.
	input.ClampToRemaining = clampToRemaining && held == nil && amount > 0
	if held != nil {
		if held.PayerID != input.PayerID || held.TokenID != input.TokenID || held.ProjectID != input.ProjectID || held.ActorID != input.ActorID {
			return redisAdmissionError(model.ErrInvalidAPIKey, snapshot)
		}
		input.Budgets, input.ExpiresAt = append([]ledger.Budget(nil), held.Budgets...), held.ExpiresAt
	}
	walletScope := "gen:user:" + strconv.Itoa(info.BillingUserID())
	tokenScope := "gen:token:" + strconv.Itoa(info.TokenId)
	policy := ""
	input.Generations = make([]ledger.Generation, 0, len(snapshot.Generations))
	for _, g := range snapshot.Generations {
		if g.Key == walletScope {
			input.WalletGeneration = g.Value
		}
		if g.Key == tokenScope {
			input.TokenGeneration = g.Value
		}
		if g.Key == "gen:policy" {
			policy = g.Value
		}
		input.Generations = append(input.Generations, ledger.Generation{Key: g.Key, PendingKey: g.PendingKey, Value: g.Value})
	}
	if info.OriginModelName == "web-search" || info.OriginModelName == "web_search" {
		input.RequestID += ":search:" + strconv.Itoa(info.ChannelId)
	}
	if value, ok := c.Get(ledger.RateLimitsContextKey); ok {
		if rates, ok := value.([]ledger.RateLimit); ok {
			input.Rates = append([]ledger.RateLimit(nil), rates...)
		}
	}
	input.WalletGeneration += ":" + policy
	input.TokenGeneration += ":" + policy
	admitted, err := gateway.ReserveAdmitted(c.Request.Context(), input)
	if err != nil {
		if mayReload && errors.Is(err, ledger.ErrStale) {
			fresh, reloadErr := reloadLedgerIdentity(c, info, gateway, snapshot)
			if reloadErr != nil {
				return redisAdmissionError(reloadErr, fresh)
			}
			if err := fresh.Validate(time.Now()); err != nil {
				return redisAdmissionError(err, fresh)
			}
			// Routing and rate policy were checked earlier in this request. A
			// changed permission requires a new request through that pipeline.
			protectSession := snapshot.IsPlayground() || snapshot.Token.IsInternal() || info.TokenKey == ""
			if !sameLedgerAdmissionPolicy(snapshot, fresh, protectSession) {
				return redisAdmissionError(ledger.ErrStale, fresh)
			}
			c.Set(model.IdentitySnapshotContextKey, fresh)
			return reserveRedisLedgerAttempt(c, amount, info, gateway, false, clampToRemaining)
		}
		var rejection *ledger.Rejection
		if errors.As(err, &rejection) && rejection.RetryAfterSeconds > 0 {
			c.Header("Retry-After", strconv.FormatInt(rejection.RetryAfterSeconds, 10))
		}
		return redisAdmissionError(err, snapshot)
	}
	// Settlement, release and split read the held amount from this copy, so it
	// must match what Redis admitted. The flag only shapes admission.
	input.Amount, input.ClampToRemaining = admitted, false
	info.RedisLedgerInput = &input
	info.FinalPreConsumedQuota = billing.MicroUSD(admitted)
	// The timer is tied to the existing inbound request. A failed process stops
	// extending it; the conditional reaper releases only an expired reservation.
	ctx := c.Request.Context()
	common.SafeCtxGo(context.Background(), "ledger-heartbeat", func() {
		tick := time.NewTicker(5 * time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				heartbeat, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				err := gateway.Extend(heartbeat, input.RequestID, time.Now().Add(45*time.Minute))
				cancel()
				if err != nil {
					common.SysLog("ledger heartbeat: " + err.Error())
				}
			}
		}
	})
	return nil
}

func reloadLedgerIdentity(c *gin.Context, info *relaycommon.RelayInfo, gateway requestpath.Gateway, prior *model.IdentitySnapshot) (*model.IdentitySnapshot, error) {
	if prior.IsPlayground() {
		return gateway.PlaygroundIdentity(c.Request.Context(), info.UserId)
	}
	if value, ok := common.GetContextKey(c, constant.ContextKeyEphemeralCredential); ok {
		if credential, ok := value.(*model.EphemeralCredential); ok && credential != nil {
			return gateway.VerifiedIdentity(c.Request.Context(), info.TokenId, credential.ID)
		}
	}
	if prior.Token.IsInternal() || info.TokenKey == "" {
		return gateway.VerifiedIdentity(c.Request.Context(), info.TokenId, 0)
	}
	return gateway.Identity(c.Request.Context(), info.TokenKey)
}

func sameLedgerAdmissionPolicy(prior, fresh *model.IdentitySnapshot, protectSession bool) bool {
	if prior == nil || fresh == nil || prior.Token == nil || fresh.Token == nil {
		return false
	}
	left, right := *prior.Token, *fresh.Token
	left.RemainQuota, right.RemainQuota, left.UsedQuota, right.UsedQuota = 0, 0, 0, 0
	left.LastUsedAt, right.LastUsedAt = 0, 0
	// These policies also affect distributor checks outside this function.
	// Their versions must remain equal even when the exported token is equal.
	protected := func(snapshot *model.IdentitySnapshot) map[string]string {
		values := map[string]string{}
		for _, generation := range snapshot.Generations {
			if generation.Key == "gen:policy" || strings.HasPrefix(generation.Key, "gen:ephemeral:") || strings.HasPrefix(generation.Key, "gen:member:") || strings.HasPrefix(generation.Key, "gen:project:") || protectSession && strings.HasPrefix(generation.Key, "gen:user:") {
				values[generation.Key] = generation.Value
			}
		}
		return values
	}
	if !reflect.DeepEqual(protected(prior), protected(fresh)) {
		return false
	}
	return reflect.DeepEqual(left, right) && reflect.DeepEqual(prior.Actor, fresh.Actor) && reflect.DeepEqual(prior.Payer, fresh.Payer) && reflect.DeepEqual(prior.Principal, fresh.Principal)
}

func bindLedgerSnapshotToken(snapshot *model.IdentitySnapshot, info *relaycommon.RelayInfo) error {
	if snapshot == nil || snapshot.Token == nil || info.TokenId != snapshot.Token.Id || info.IsPlayground != snapshot.IsPlayground() {
		return model.ErrInvalidAPIKey
	}
	info.TokenUnlimited = snapshot.Token.UnlimitedQuota
	return nil
}
func redisAdmissionError(err error, snapshot *model.IdentitySnapshot) *types.LazuError {
	code := types.ErrorCodeGatewayTemporarilyUnavailable
	status := http.StatusServiceUnavailable
	var rejection *ledger.Rejection
	if errors.As(err, &rejection) {
		status = http.StatusTooManyRequests
		switch rejection.Code {
		case "balance":
			code = types.ErrorCodeInsufficientQuota
		case "key":
			code = types.ErrorCodeKeyBudgetExceeded
		case "rate":
			code = types.ErrorCodeRequestRateLimitExceeded
			if rejection.RateDecision == "token_rpm_exceeded" || rejection.RateDecision == "token_tpm_exceeded" {
				code = types.ErrorCodeTokenRateLimitExceeded
			} else if rejection.RateDecision == "total_rpm_exceeded" {
				code = types.ErrorCodeTotalRequestRateLimitExceeded
			}
			return types.NewErrorWithStatusCode(err, code, status, types.D{"limit": rejection.RateLimit, "window_minutes": rejection.WindowMinutes}, types.ErrOptionWithSkipRetry())
		case "budget":
			code = types.ErrorCodePlatformBudgetExceeded
			if snapshot != nil {
				for _, b := range snapshot.Budgets {
					if b.ID != rejection.BudgetID {
						continue
					}
					if b.ProjectID == 0 {
						break
					}
					switch b.Scope {
					case model.SpendBudgetScopeToken:
						code = types.ErrorCodeKeyBudgetExceeded
					case model.SpendBudgetScopeUser:
						code = types.ErrorCodeMemberBudgetExceeded
					default:
						code = types.ErrorCodeProjectBudgetExceeded
					}
					break
				}
			}
		default:
			status = http.StatusServiceUnavailable
		}
	}
	if errors.Is(err, model.ErrProjectAccessSuspended) {
		code = types.ErrorCodeProjectAccessSuspended
		status = http.StatusForbidden
	}
	if errors.Is(err, model.ErrTokenBudgetExhausted) {
		code = types.ErrorCodeKeyBudgetExceeded
		status = http.StatusTooManyRequests
	}
	if errors.Is(err, model.ErrInvalidAPIKey) || errors.Is(err, model.ErrTokenExpired) || errors.Is(err, model.ErrTokenDisabled) {
		code, status = types.ErrorCodeInvalidAPIKey, http.StatusUnauthorized
	}
	return types.NewErrorWithStatusCode(err, code, status, types.ErrOptionWithSkipRetry())
}
func settleRedisLedger(info *relaycommon.RelayInfo, actual billing.MicroUSD, release bool) error {
	return settleRedisLedgerEnvelope(info, actual, release, "")
}

// SettleRedisLedgerWithFacts commits the already captured consume/routing facts
// with financial settlement in one Lua operation. Durable spool replay carries
// the same envelope, so marking it published never loses a successful charge.
func SettleRedisLedgerWithFacts(c *gin.Context, info *relaycommon.RelayInfo, actual billing.MicroUSD) error {
	totalTimer := common.StartRelayPhase(c, common.RelayPhaseSettle)
	defer totalTimer.Finish()
	encodeTimer := common.StartRelayPhase(c, common.RelayPhaseSettleEncode)
	envelope, complete, envelopeErr := model.BuildRelayResultEnvelope(c)
	encodeTimer.Finish()
	if envelopeErr != nil {
		// Financial durability remains independent of optional rich telemetry.
		return errors.Join(envelopeErr, settleRedisLedger(info, actual, false))
	}
	var timing *common.RelayTiming
	if common.HotPathMetricsEnabled && c != nil {
		timing = common.RelayTimingFromContext(c)
	}
	if err := settleRedisLedgerEnvelope(info, actual, false, envelope, timing); err != nil {
		return err
	}
	if complete {
		model.MarkRelayResultPublished(c)
	}
	return nil
}

func settleRedisLedgerEnvelope(info *relaycommon.RelayInfo, actual billing.MicroUSD, release bool, envelope string, timings ...*common.RelayTiming) error {
	if info.RedisLedgerInput == nil {
		return errors.New("accepted request has no immutable ledger input")
	}
	engine := model.GatewayLedger()
	input := *info.RedisLedgerInput
	if !release && actual < 0 {
		return errors.New("negative final settlement")
	}
	input.PricingVersion = info.PriceData.PricingVersion
	ctx, cancel := context.WithTimeout(common.WithRequestRedisMetrics(context.Background()), 2*time.Second)
	defer cancel()
	if common.HotPathMetricsEnabled && len(timings) > 0 && timings[0] != nil {
		ctx = common.WithRedisDispatchObserver(ctx, func(duration time.Duration) {
			timings[0].AddPhase(common.RelayPhaseSettleEval, duration)
		})
	}
	kind := "settle"
	var err error
	if release {
		kind = "release"
		err = requestpath.Current().Release(ctx, input)
	} else {
		if envelope != "" {
			err = requestpath.Current().SettleWithTelemetry(ctx, input, int64(actual), envelope)
		} else {
			err = requestpath.Current().Settle(ctx, input, int64(actual))
		}
	}
	if err != nil {
		if spoolErr := engine.PersistSettlementWithTelemetry(input, int64(actual), kind, envelope); spoolErr != nil {
			return errors.Join(err, spoolErr)
		}
	}
	info.SettledChargeMicrousd = actual
	info.LedgerSettlementFinalized = true
	return nil
}
