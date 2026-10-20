package controller

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/lazu-ai/lazu/internal/httperr"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/relay"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	relayrouter "github.com/lazu-ai/lazu/internal/relay/router"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/packages/domain/providercap"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.LazuError {
	var err *types.LazuError
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		err = relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, info)
	case relayconstant.RelayModeDecisions:
		err = relay.DecisionHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		err = relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses:
		err = relay.ResponsesHelper(c, info)
	default:
		err = relay.TextHelper(c, info)
	}
	return err
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.LazuError {
	var err *types.LazuError
	if strings.Contains(c.Request.URL.Path, "embed") {
		err = relay.GeminiEmbeddingHandler(c, info)
	} else {
		err = relay.GeminiHelper(c, info)
	}
	return err
}

func Relay(c *gin.Context, relayFormat types.RelayFormat) {
	if state := common.InternalRelayCallStateFromContext(c.Request.Context()); state != nil && state.Preflight {
		common.SetContextKey(c, constant.ContextKeyModelLogWritten, true)
	}
	defer service.RecordUnfinishedModelExchange(c)
	defer service.EnqueueProtocolTokenShadow(c)
	common.SetContextKey(c, constant.ContextKeyProviderAttempted, false)

	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	var (
		lazuErr *types.LazuError
		ws      *websocket.Conn
	)

	if relayFormat == types.RelayFormatOpenAIRealtime {
		var err error
		ws, err = upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			helper.WssError(c, ws, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry()).ToOpenAIError())
			return
		}
		defer ws.Close()
	}

	defer func() {
		if lazuErr != nil {
			if relayFormat == types.RelayFormatDecisions {
				lazuErr = withGeneratedDecisionDetails(c, lazuErr)
			}
			writeRelayError(c, relayFormat, ws, lazuErr)
			recordRelayRejection(c, lazuErr)
		}
	}()

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	if err != nil {
		if !errors.As(err, &lazuErr) {
			lazuErr = types.NewError(err, types.ErrorCodeInvalidRequest)
		}
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		lazuErr = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}
	common.SetContextKey(c, constant.ContextKeyRelayInfo, relayInfo)
	relayInfo.InitChannelMeta(c)
	if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); ok && parsed != nil {
		relayInfo.ProtocolRequest = parsed.Semantic
	}

	if shouldApplyOSSURL(c) {
		if err := service.ApplyOSSURLToRequest(c, request, relayInfo.OriginModelName); err != nil {
			lazuErr = types.NewError(err, types.ErrorCodeInvalidRequest)
			return
		}
	}
	if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); ok && parsed != nil && parsed.Client.Family == textgen.ProtocolOpenAIChat && openai_chat.HasOSSURLHint(parsed.Semantic) {
		if wire, wireOK := request.(*dto.GeneralOpenAIRequest); wireOK {
			effective := parsed.Semantic.Clone()
			if syncErr := openai_chat.SyncEnrichedWire(effective, wire); syncErr != nil {
				lazuErr = types.NewError(syncErr, types.ErrorCodeInvalidRequest)
				return
			}
			features, uses := textgen.ExtractFeatures(effective)
			parsedCopy := *parsed
			parsedCopy.Semantic = effective
			parsedCopy.Features = features
			parsedCopy.Uses = uses
			parsed = &parsedCopy
			common.SetContextKey(c, constant.ContextKeyTextgenParsed, parsed)
			relayInfo.ProtocolRequest = effective
		}
	}

	var meta *types.TokenCountMeta
	if relayInfo.ProtocolRequest != nil {
		meta = textgen.TokenCountMeta(relayInfo.ProtocolRequest)
	} else {
		meta = request.GetTokenCountMeta()
	}
	relayInfo.TokenCountMeta = meta

	// Keep the request hot path cheap: structured text/media metadata is enough
	// for TPM admission and pre-consumption. Exact upstream usage remains the
	// settlement source, while CountRequestToken stays available for explicit
	// token-count utilities and shadow checks.
	tokens, err := service.EstimateRequestTPMTokens(meta, relayInfo)
	if err != nil {
		lazuErr = types.NewError(err, types.ErrorCodeTokenizationError)
		return
	}
	recordProtocolAdmissionComparison(c, relayInfo, tokens)
	tpmTokens := service.RequestTPMTokens(tokens, meta)
	internalCall := common.InternalRelayCallStateFromContext(c.Request.Context())
	if internalCall == nil || !internalCall.Preflight {
		if code, err := middleware.CheckRequestTokenRateLimit(c, tpmTokens); err != nil {
			lazuErr = types.NewError(err, code)
			return
		}
	}

	relayInfo.SetPromptTokens(tokens)

	priceData, err := helper.PriceQuoteHelper(c, relayInfo, tokens, meta)
	if err != nil {
		lazuErr = types.NewError(err, types.ErrorCodePricingNotConfigured)
		return
	}
	if internalCall != nil && internalCall.Preflight {
		internalCall.ReservationMicrousd = priceData.QuotaToPreConsume.Int64()
		c.Status(http.StatusNoContent)
		return
	}

	// common.SetContextKey(c, constant.ContextKeyTokenCountMeta, meta)

	if priceData.FreeModel && !model.GatewayLedgerEnabled() {
		lazuErr = service.CheckProjectPaymentAdmission(relayInfo.ProjectID)
		if lazuErr != nil {
			return
		}
		logger.LogInfo(c, fmt.Sprintf("Model %s is free, skipping pre-consume quota", relayInfo.OriginModelName))
	} else {
		lazuErr = service.PreConsumeEstimatedQuota(c, priceData.QuotaToPreConsume, relayInfo)
		if lazuErr != nil {
			return
		}
	}

	defer func() {
		if shouldReturnPreConsumedQuota(relayInfo, lazuErr) {
			service.ReturnPreConsumedQuota(c, relayInfo)
		}
	}()

	// Retries are bounded by the first-byte budget, not just the attempt count:
	// a provider that takes half a minute to report overload must not multiply
	// that latency by the retry count before the client hears anything.
	relayStartedAt := time.Now()
	pick := pickSameChannel
	channelSwitchesUsed := 0
	attemptsMade := 0

	maxRetries := relayMaxRetries
	if relayFormat == types.RelayFormatDecisions {
		maxRetries = 1
	}
	for i := 0; i <= maxRetries; i++ {
		relayInfo.StreamFailedWithoutUsage = false
		// A retry can fail during local preparation even when an earlier attempt
		// reached its provider. Keep the final outcome scoped to this attempt.
		common.SetContextKey(c, constant.ContextKeyProviderAttempted, false)
		// Per-attempt signals: a hint, marker or captured payload left by an
		// earlier attempt must not influence how this one is paced, classified,
		// billed or logged.
		common.SetContextKey(c, constant.ContextKeyUpstreamRetryAfter, 0)
		common.SetContextKey(c, constant.ContextKeyStreamFirstEventFailed, false)
		common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, false)
		// Settlement is a one-shot signal. It is unreachable across attempts
		// today only because every site that raises it also returns a
		// skip-retry error; nothing enforces that, and a leak here bills the
		// customer twice.
		common.SetContextKey(c, constant.ContextKeyProtocolStreamSettle, false)
		// The failed attempt's partial capture would otherwise be reused as the
		// successful retry's exchange by handlers that do not build their own.
		common.SetContextKey(c, constant.ContextKeyModelLogPayload, nil)
		relayInfo.ResetFirstResponse()
		attemptsMade = i + 1
		if i > 0 {
			// Set before the attempt runs: a streaming response flushes its
			// headers on the first byte, so anything added after the loop would
			// never reach the client.
			c.Header("X-Lazu-Retries", strconv.Itoa(i))
		}
		channel, err := getChannel(c, group, originalModel, i, pick)
		if err != nil {
			logger.LogError(c, err.Error())
			lazuErr = relayErrorAfterRetrySelectionFailure(lazuErr, err, i)
			break
		}

		if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); ok && parsed != nil {
			attempt, planErr := textgenruntime.PrepareAttempt(parsed, channel, originalModel)
			if planErr != nil {
				lazuErr = protocolBridgeLazuError(c, planErr)
				break
			}
			relayInfo.ProtocolRequest = attempt.Request
			relayInfo.ProtocolPlan = attempt.Plan
			common.SetContextKey(c, constant.ContextKeyProtocolPlan, attempt.Plan)
			setProtocolAttemptWarning(c, attempt.Plan)
			if attempt.Plan.Native {
				common.IncrementHotPathCounter(common.HotPathCounterProtocolPlanNative)
			} else {
				common.IncrementHotPathCounter(common.HotPathCounterProtocolPlanBridge)
			}
		}

		addUsedChannel(c, channel.Id)
		if channelID := c.GetInt("channel_id"); channelID > 0 {
			c.Header("X-Lazu-Channel-Id", strconv.Itoa(channelID))
		}

		channelType := c.GetInt("channel_type")
		apiType, ok := common.ChannelType2APIType(channelType)
		if relayFormat == types.RelayFormatDecisions || (!ok && channelType == constant.ChannelTypeCustom && relayInfo.ProtocolPlan != nil) {
			apiType = constant.APITypeOpenAI
			ok = true
		}
		if !ok {
			lazuErr = types.NewErrorWithStatusCode(fmt.Errorf("invalid channel type %d", channelType), types.ErrorCodeInvalidApiType, http.StatusServiceUnavailable)
			// An unusable channel is never fixed by sending to it again, so the
			// only way forward is a different one. That still has to obey the
			// lane's rules and the switch budget, which only planSwitchOnly
			// knows — deciding here would let an agent-lane request move to a
			// channel with a different price.
			if !planSwitchOnly(c, lazuErr, i, time.Since(relayStartedAt), &channelSwitchesUsed, &pick) {
				break
			}
			continue
		}
		profile := providercap.GetProfile(apiType)
		if relayInfo.ProtocolPlan == nil && relayFormat != types.RelayFormatDecisions {
			if supported, reason := providercap.IsSupported(apiType, relayFormat); !supported {
				logger.LogWarn(c, fmt.Sprintf("skip channel %d (api_type=%d,family=%s): unsupported relay format %s (%s)", channel.Id, apiType, profile.Family, relayFormat, reason))
				lazuErr = types.NewErrorWithStatusCode(
					fmt.Errorf("channel %d (api_type=%d,family=%s) does not support relay format %s: %s", channel.Id, apiType, profile.Family, relayFormat, reason),
					types.ErrorCodeInvalidApiType,
					http.StatusServiceUnavailable,
				)
				// The channel cannot serve this relay format at all; same rules
				// as the unusable-channel-type case above.
				if !planSwitchOnly(c, lazuErr, i, time.Since(relayStartedAt), &channelSwitchesUsed, &pick) {
					break
				}
				continue
			}
		}

		requestBody, _ := common.GetRequestBody(c)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))

		attemptStartedAt := time.Now().UTC()
		upstreamTimer := common.StartHotPathStage(common.HotPathStageUpstreamRelay)
		switch relayFormat {
		case types.RelayFormatOpenAIRealtime:
			lazuErr = relay.WssHelper(c, relayInfo)
		case types.RelayFormatClaude:
			lazuErr = relay.ClaudeHelper(c, relayInfo)
		case types.RelayFormatGemini:
			lazuErr = geminiRelayHandler(c, relayInfo)
		default:
			lazuErr = relayHandler(c, relayInfo)
		}
		upstreamTimer.Finish(lazuErr)

		if lazuErr == nil {
			return
		}
		if relayInfo.ProtocolPlan != nil && !relayInfo.ProtocolPlan.Native && lazuErr.GetErrorCode() == types.ErrorCodeConvertRequestFailed {
			common.IncrementHotPathCounter(common.HotPathCounterProtocolConversionFailed)
		}

		// Decide this attempt's fate before recording it. An attempt that is
		// about to be retried must not leave a customer-facing error row behind
		// for a request that may still succeed; operators still get the log
		// line, the auto-ban check and the health signal either way.
		plan := planRelayRetry(c, lazuErr, i, time.Since(relayStartedAt), channelSwitchesUsed)
		if plan.Reason != "" {
			logger.LogWarn(c, "skip retry: "+plan.Reason)
		}

		processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), lazuErr, plan.Retry)

		if !plan.Retry {
			break
		}
		pick = plan.Pick
		if pick != pickSameChannel {
			channelSwitchesUsed++
		}

		recordRetriedRoutingAttempt(c, channel.Id, i, attemptStartedAt, lazuErr)
		if err := waitRelayRetry(c.Request.Context(), plan.Delay); err != nil {
			lazuErr = service.UpstreamReadError(c, err)
			break
		}
	}

	if retries := attemptsMade - 1; retries > 0 {
		// The per-channel retry rate and its success rate are what any later
		// tuning of the fixed retry budget has to argue from.
		logger.LogInfo(c, fmt.Sprintf("relay retries: %d (channels %s, elapsed %s)",
			retries,
			strings.Trim(strings.Join(strings.Fields(fmt.Sprint(c.GetStringSlice("use_channel"))), "->"), "[]"),
			time.Since(relayStartedAt).Round(time.Millisecond)))
	}
}

// upstreamRetryAfter reads the Retry-After hint the last attempt recorded.
func upstreamRetryAfter(c *gin.Context) time.Duration {
	seconds := common.GetContextKeyInt(c, constant.ContextKeyUpstreamRetryAfter)
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// If a provider attempt already failed, exhausting compatible retry channels
// must not replace that actionable upstream status with get_channel_failed.
func relayErrorAfterRetrySelectionFailure(previous, selection *types.LazuError, retryCount int) *types.LazuError {
	if retryCount > 0 && previous != nil {
		return previous
	}
	return selection
}

func shouldApplyOSSURL(c *gin.Context) bool {
	return !common.GetContextKeyBool(c, constant.ContextKeyOSSURLApplied)
}

// setProtocolAttemptWarning tells the client what the attempt could not keep
// as asked: a stateless bridge, an endpoint bridge for a feature, a
// degradation (textgen/degrade.go). Every warning of the plan, comma
// separated; the request detail keeps them one by one.
func setProtocolAttemptWarning(c *gin.Context, plan *textgen.RelayPlan) {
	c.Writer.Header().Del("X-Lazu-Warning")
	if plan == nil || len(plan.Warnings) == 0 {
		return
	}
	messages := make([]string, 0, len(plan.Warnings))
	for _, warning := range plan.Warnings {
		messages = append(messages, warning.Message)
	}
	c.Header("X-Lazu-Warning", strings.Join(messages, ", "))
}

func recordProtocolAdmissionComparison(c *gin.Context, relayInfo *relaycommon.RelayInfo, admissionTokens int) {
	if c == nil || relayInfo == nil || relayInfo.ProtocolRequest == nil {
		return
	}
	common.SetContextKey(c, constant.ContextKeyProtocolAdmissionTokens, admissionTokens)
	// Keep only immutable metadata on the request. The deferred hook submits it
	// after synchronous settlement, never before the provider request starts.
	meta := relayInfo.TokenCountMeta
	if meta == nil {
		meta = textgen.TokenCountMeta(relayInfo.ProtocolRequest)
	}
	if len(meta.Files) > 0 {
		common.SetContextKey(c, constant.ContextKeyProtocolTokenSkip, "media_would_require_duplicate_fetch")
		return
	}
	c.Set("protocol_token_shadow_meta", meta)
	c.Set("protocol_token_shadow_model", relayInfo.OriginModelName)
	c.Set("protocol_token_shadow_format", relayInfo.RelayFormat)
}

func recordRetriedRoutingAttempt(
	c *gin.Context,
	channelID int,
	attemptIndex int,
	startedAt time.Time,
	lazuErr *types.LazuError,
) {
	if c == nil || lazuErr == nil || channelID <= 0 {
		return
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		return
	}
	var decisionID int64
	value, _ := c.Get("routing_decision_id")
	switch typed := value.(type) {
	case int64:
		decisionID = typed
	case int:
		decisionID = int64(typed)
	}

	now := time.Now().UTC()
	durationMs := int(now.Sub(startedAt).Milliseconds())
	if durationMs < 0 {
		durationMs = 0
	}
	statusCode := lazuErr.StatusCode
	if statusCode <= 0 {
		statusCode = http.StatusBadGateway
	}
	finalGroup := strings.TrimSpace(c.GetString("final_group"))
	if finalGroup == "" {
		finalGroup = common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	}
	finalGroup = model.NormalizeChannelLane(finalGroup)
	providerAttempted := common.GetContextKeyBool(c, constant.ContextKeyProviderAttempted)
	healthEligible, healthFailed := types.ChannelHealthVerdict(
		statusCode, lazuErr.GetErrorCode(), providerAttempted,
	)
	if err := model.QueueRoutingAttempt(c, &model.RoutingAttemptOutcome{
		RequestID: requestID, AttemptIndex: attemptIndex, OutcomeAt: now,
		DecisionID: decisionID, ChannelID: channelID, FinalGroup: finalGroup,
		StatusCode: statusCode, ErrorCode: string(lazuErr.GetErrorCode()),
		ErrorType: string(lazuErr.GetErrorType()), ProviderAttempted: &providerAttempted,
		HealthEligible: &healthEligible, HealthFailed: &healthFailed,
		TotalDurationMs: durationMs,
	}); err != nil {
		logger.LogError(c, "failed to record retried routing attempt: "+err.Error())
	}

	// A failure that a retry papered over is still that channel's failure, and
	// it is the only evidence there will ever be about it: the request-level
	// outcome records the attempt that finally succeeded, on a different
	// channel. Without this the breaker would never trip on the upstream it
	// exists to pull — with retries enabled, being rescued is the normal fate
	// of a failing channel, not an edge case.
	attemptModel := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyRequestModel))
	if attemptModel == "" {
		attemptModel = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
	}
	if healthEligible && attemptModel != "" &&
		model.ShouldRecordRouteHealthEvidence(attemptModel, finalGroup, channelID, !healthFailed) &&
		(!healthFailed || model.ClaimRouteHealthFailure(c, attemptModel, finalGroup, channelID)) {
		evidenceAt := now
		common.SafeGo("route-breaker-attempt-evidence", func() {
			if err := model.RecordRouteHealthEvidence(
				attemptModel, finalGroup, channelID, !healthFailed,
				string(lazuErr.GetErrorCode()), evidenceAt,
			); err != nil {
				common.SysLog("failed to record retried attempt route health: " + err.Error())
			}
		})
	}
}

var upgrader = websocket.Upgrader{
	// /v1/realtime is the only WebSocket relay route. The upstream adaptor adds
	// provider-specific beta protocols after this client-facing handshake.
	Subprotocols: []string{"realtime"},
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

// getChannel resolves the channel for one attempt. The first attempt uses the
// channel the distributor already selected; a retry follows pick. Re-running the
// channel setup on a same-channel retry also advances a multi-key channel to its
// next key, so the retry does not replay a key the provider just rejected.
func getChannel(c *gin.Context, group, originalModel string, retryCount int, pick channelPickMode) (*model.Channel, *types.LazuError) {
	if retryCount == 0 {
		channel, err := model.CacheGetChannel(c.GetInt("channel_id"))
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		return channel, nil
	}
	if pick == pickSameChannel {
		return sameChannelForRetry(c, originalModel)
	}
	usedCounts := make(map[int]int)
	for _, used := range c.GetStringSlice("use_channel") {
		if id, parseErr := strconv.Atoi(used); parseErr == nil {
			usedCounts[id]++
		}
	}
	excluded := make(map[int]struct{}, len(usedCounts))
	for id, attempts := range usedCounts {
		channel, cacheErr := model.CacheGetChannel(id)
		if cacheErr != nil || attempts >= channelRetryCapacity(channel) {
			excluded[id] = struct{}{}
		}
	}
	var channel *model.Channel
	var selectGroup string
	var err error
	if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); (ok && parsed != nil) || c.Request.URL.Path == "/v1/decisions" {
		channel, selectGroup, err = relayrouter.SelectProtocolCompatibleChannel(c, group, originalModel, excluded)
	} else {
		// Preserve the legacy retry-index semantics for video, image, audio,
		// embeddings, Claude and Gemini routes that do not use textgen IR.
		channel, selectGroup, err = model.CacheGetRandomSatisfiedChannel(c, group, originalModel, retryCount)
	}
	if err != nil || channel == nil {
		// No alternative is free. Unless the failure was the channel's own
		// (a rejected key, a withdrawn model — which the same channel would
		// just repeat), spending a remaining attempt on it beats giving up.
		if pick == pickDifferentChannel {
			logger.LogWarn(c, fmt.Sprintf("no alternative channel for model %s, retrying the current one", originalModel))
			return sameChannelForRetry(c, originalModel)
		}
		if err != nil {
			return nil, types.NewError(fmt.Errorf("failed to resolve retry channel for group %s and model %s: %s", selectGroup, originalModel, err.Error()), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		return nil, types.NewError(fmt.Errorf("no retry channel available for group %s and model %s", selectGroup, originalModel), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	lazuErr := middleware.SetupContextForSelectedChannel(c, channel, originalModel)
	if lazuErr != nil {
		return nil, lazuErr
	}
	return channel, nil
}

// sameChannelForRetry re-selects the channel the request is already on. The
// context setup is re-run so a multi-key channel advances to its next key.
func sameChannelForRetry(c *gin.Context, originalModel string) (*model.Channel, *types.LazuError) {
	channel, err := model.CacheGetChannel(c.GetInt("channel_id"))
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if lazuErr := middleware.SetupContextForSelectedChannel(c, channel, originalModel); lazuErr != nil {
		return nil, lazuErr
	}
	return channel, nil
}

// channelRetryCapacity is the number of attempts a channel can receive before
// routing must exclude it. Multi-key channels stay eligible long enough for
// SetupContextForSelectedChannel to rotate through their enabled keys. Random
// selection cannot guarantee a distinct credential on retry, so it receives no
// additional capacity unless key-index tracking is added to the request state.
func channelRetryCapacity(channel *model.Channel) int {
	if channel == nil || !channel.ChannelInfo.IsMultiKey || channel.ChannelInfo.MultiKeyMode != constant.MultiKeyModePolling {
		return 1
	}
	return channel.EnabledKeyCount()
}

func protocolBridgeLazuError(c *gin.Context, err error) *types.LazuError {
	common.IncrementHotPathCounter(common.HotPathCounterProtocolUnsupported)
	details := map[string]any{"reason": err.Error()}
	var unsupported *textgen.UnsupportedFeatureError
	if errors.As(err, &unsupported) {
		details["feature"] = unsupported.Feature.String()
		if unsupported.Path != "" {
			details["field"] = unsupported.Path
		}
		common.IncrementHotPathCounter(common.HotPathCounterProtocolUnsupportedPrefix + unsupported.Feature.String())
	}
	return types.NewErrorWithStatusCode(err, types.ErrorCodeProtocolBridgeUnsupported, http.StatusBadRequest,
		types.ErrOptionWithDetails(details), types.ErrOptionWithSkipRetry())
}

func shouldRetry(c *gin.Context, openaiErr *types.LazuError, retryTimes int) bool {
	if c != nil && c.Request != nil && c.Request.Context().Err() != nil {
		return false
	}
	if openaiErr == nil {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	// A pinned channel is an explicit "use exactly this, once" — an admin test
	// wants the raw upstream result, not a retried one.
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	// Note: the agent lane is NOT excluded here. "Never silently switch
	// channels" is a billing rule about which channel serves the request, and a
	// same-channel retry bills at the same price the customer picked. Channel
	// switching stays gated at the call site.
	if relayInfo, ok := common.GetContextKeyType[*relaycommon.RelayInfo](c, constant.ContextKeyRelayInfo); ok {
		// Once response bytes are already sent to client, do not retry to avoid duplicate side effects.
		//
		// HasSendResponse is driven by the stream scanner, which timestamps the
		// first event it reads from the upstream — that is a time-to-first-token
		// measurement, not proof that anything was forwarded downstream. A stream
		// whose first event was the provider's own failure trips it without the
		// client having seen a byte. Only the handler knows the difference, so it
		// says so explicitly and that verdict wins here.
		if relayInfo != nil && relayInfo.HasSendResponse() &&
			!common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) {
			logger.LogWarn(c, "skip retry: response already started")
			return false
		}
	}

	decision := classifyRelayRetryDecision(openaiErr)
	return decision.AllowRetry
}

// processChannelError records a failed attempt. willRetry says the request has
// another attempt coming: the operator-facing signals (log line, auto-ban check,
// channel health) still fire, but the customer-facing error row is withheld,
// because a request that recovers on its next attempt is not a failure to the
// person who made it. If every attempt fails, the last one — which by definition
// is not retried — writes that single row.
func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.LazuError, willRetry bool) {
	logger.LogError(c, fmt.Sprintf("relay error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, err.Error()))
	// 不要使用context获取渠道信息，异步处理时可能会出现渠道信息不一致的情况
	// do not use context to get channel info, there may be inconsistent channel info when processing asynchronously
	if service.ShouldDisableChannel(channelError.ChannelId, err) && channelError.AutoBan {
		common.SafeCtxGo(c.Request.Context(), "disable-channel-after-relay-error", func() {
			service.DisableChannel(channelError, err.Error())
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		channelId := c.GetInt("channel_id")
		username := c.GetString("username")

		// The real client IP from our trusted proxy chain; c.ClientIP()
		// returns the proxy peer address behind that chain.
		clientIp := common.GetRealClientIP(c)

		other := make(map[string]interface{})
		if c.Request != nil && c.Request.URL != nil {
			other["request_path"] = c.Request.URL.Path
		}
		other["request_id"] = c.GetString(common.RequestIdKey)
		other["duration_ms"] = service.RequestDurationMS(c)
		other["usage_unavailable"] = true
		other["any_provider_attempted"] = common.GetContextKeyBool(c, constant.ContextKeyAnyProviderAttempted)
		other["error_type"] = err.GetErrorType()
		other["error_code"] = err.GetErrorCode()
		other["status_code"] = err.StatusCode
		other["channel_id"] = channelId
		other["channel_name"] = c.GetString("channel_name")
		other["channel_type"] = c.GetInt("channel_type")
		adminInfo := make(map[string]interface{})
		adminInfo["use_channel"] = c.GetStringSlice("use_channel")
		isMultiKey := common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey)
		if isMultiKey {
			adminInfo["is_multi_key"] = true
			adminInfo["multi_key_index"] = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
		}
		other["admin_info"] = adminInfo
		other["will_retry"] = willRetry
		// The console row is the customer's view of their own request, so it
		// only describes a request that actually ended in failure. The file
		// entry below is the operator's, and keeps every attempt — that per
		// attempt trail is what upstream failures have to be diagnosed from.
		if !willRetry {
			model.RecordErrorLog(c, userId, channelId, modelName, tokenName, err.MaskSensitiveError(), tokenId, int(service.RequestDurationMS(c)/1000), common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
		}

		// 同时写入错误日志文件（复用上面获取的 clientIp，避免重复数据库查询）
		service.RecordErrorLogToFile(service.ErrorLogEntry{
			UserId:    userId,
			Username:  username,
			ChannelId: channelId,
			ModelName: modelName,
			TokenName: tokenName,
			TokenId:   tokenId,
			Content:   err.MaskSensitiveError(),
			UseTime:   int(service.RequestDurationMS(c) / 1000),
			IsStream:  common.GetContextKeyBool(c, constant.ContextKeyIsStream),
			Group:     userGroup,
			Ip:        clientIp,
			Other:     other,
		})
		// 标记已记录，避免在 defer 中重复记录. A retried attempt deliberately
		// leaves the DB row unwritten, so the deferred writer must stay free to
		// record the request's final outcome.
		if !willRetry {
			c.Set("error_logged_to_file", true)
		}
	}

}

func RelayNotImplemented(c *gin.Context) {
	httperr.WriteOpenAI(c, types.Err(types.ErrorCodeNotImplemented))
}

func RelayNotFound(c *gin.Context) {
	httperr.WriteOpenAI(c, types.Err(types.ErrorCodeEndpointNotSupported, types.D{"path": c.Request.Method + " " + c.Request.URL.Path}))
}
