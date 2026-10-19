package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	relayrouter "github.com/lazu-ai/lazu/internal/relay/router"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

type ModelRequest struct {
	Model string `json:"model"`
	Group string `json:"group,omitempty"`
}

const (
	manualRoutingOutcomeOverrideKey  = "manual_routing_outcome_override"
	manualRoutingOutcomeStatusKey    = "manual_routing_outcome_status"
	manualRoutingOutcomeErrorCodeKey = "manual_routing_outcome_error_code"
	manualRoutingOutcomeErrorTypeKey = "manual_routing_outcome_error_type"
)

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
		defer service.RecordUnfinishedModelExchange(c)
		var channel *model.Channel
		var routingDecisionID int64
		var pinnedRequestContext *relayrouter.RequestContext
		var pinnedDecision *relayrouter.Decision
		credentialAuthorized := false
		suppressRelayTelemetry := common.GetContextKeyBool(c, constant.ContextKeySuppressRelayTelemetry)
		channelId, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
		modelRequest, shouldSelectChannel, err := getModelRequest(c)
		if err != nil {
			abortRequestBodyError(c, err)
			return
		}
		// Publish the requested model before any rejection in this function can
		// fire: the deep `c.Set("original_model", ...)` below only runs once a
		// channel and its model mapping are resolved, so without this a
		// model_not_allowed / budget rejection raised earlier here would log
		// an empty model_name. original_model is what every existing consumer
		// (c.GetString("original_model")) already reads; request_model is the
		// same value under RequestedModelName's own key, kept so TokenAuth's
		// peekRequestedModel (which runs before Distribute even starts, for
		// rejections raised in TokenAuth itself) has somewhere to publish it.
		common.SetContextKey(c, constant.ContextKeyOriginalModel, modelRequest.Model)
		if modelRequest != nil && modelRequest.Model != "" {
			common.SetContextKey(c, constant.ContextKeyRequestModel, modelRequest.Model)
		}
		if common.IsDecisionModel(modelRequest.Model) && c.Request.URL.Path != "/v1/decisions" {
			abortWithCode(c, types.ErrorCodeUnsupportedDecisionParameter)
			return
		}
		if !authorizeTokenModelAndVendor(c, modelRequest.Model) {
			return
		}
		if ok {
			id, err := strconv.Atoi(channelId.(string))
			if err != nil {
				abortWithCode(c, types.ErrorCodeInvalidChannelId)
				return
			}
			channel, err = model.GetChannelById(id, true)
			if err != nil {
				abortWithCode(c, types.ErrorCodeInvalidChannelId)
				return
			}
			if channel.Status != common.ChannelStatusEnabled {
				abortWithCode(c, types.ErrorCodeChannelDisabled)
				return
			}
			if !isAutoModelRequest(modelRequest.Model) && !model.IsModelRequestable(modelRequest.Model) {
				abortWithCode(c, types.ErrorCodeModelNotFound, types.D{"model": modelRequest.Model})
				return
			}
			finalGroup := laneForChannel(channel)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, finalGroup)
			c.Set("final_group", finalGroup)

			rawBody, _ := common.GetRequestBody(c)
			if rawBody != nil {
				c.Request.Body = io.NopCloser(bytes.NewReader(rawBody))
			}
			pinnedRequestContext = &relayrouter.RequestContext{
				RequestID:       c.GetString(common.RequestIdKey),
				UserID:          common.GetContextKeyInt(c, constant.ContextKeyUserId),
				TokenID:         common.GetContextKeyInt(c, constant.ContextKeyTokenId),
				TokenGroup:      common.GetContextKeyString(c, constant.ContextKeyTokenGroup),
				TokenMetadata:   common.GetContextKeyString(c, constant.ContextKeyTokenMetadata),
				GinContext:      c,
				RequestedModel:  modelRequest.Model,
				ResolvedModel:   modelRequest.Model,
				CandidateGroups: []string{finalGroup},
				DefaultGroup:    finalGroup,
			}
			pinnedRequestContext.Features = routingFeatures(c, pinnedRequestContext, rawBody)
			populateRoutingAttribution(c, pinnedRequestContext)
			pinnedDecision = &relayrouter.Decision{
				ChannelID: channel.Id,
				Model:     modelRequest.Model,
				Provider:  channel.GetTag(),
				Group:     finalGroup,
				Reason:    "token_specific_channel",
				Score:     1,
				Candidates: []relayrouter.Candidate{{
					ChannelID: channel.Id,
					Score:     1,
				}},
				Meta: map[string]any{
					"strategy_name":    "token_specific_channel",
					"strategy_version": "v1",
					"resolved_model":   modelRequest.Model,
				},
				Channel: channel,
			}
		} else {
			// Select a channel for the user.
			if err := enrichTextgenRequestForRouting(c, modelRequest.Model); err != nil {
				abortRequestBodyError(c, err)
				return
			}

			if shouldSelectChannel {
				if modelRequest.Model == "" {
					abortWithCode(c, types.ErrorCodeModelNameRequired)
					return
				}
				userGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
				tokenGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
				// check path is /pg/chat/completions
				if strings.HasPrefix(c.Request.URL.Path, "/pg/chat/completions") {
					playgroundRequest := &dto.PlayGroundRequest{}
					err = common.UnmarshalBodyReusable(c, playgroundRequest)
					if err != nil {
						abortRequestBodyError(c, err)
						return
					}
					if playgroundRequest.Group != "" {
						if playgroundRequest.Group != "default" && playgroundRequest.Group != userGroup {
							abortWithCode(c, types.ErrorCodeGroupAccessDenied)
							return
						}
						userGroup = playgroundRequest.Group
					}
				}
				groupCandidates := []string{model.ChannelLaneDirect}

				rawBody, _ := common.GetRequestBody(c)
				if rawBody != nil {
					c.Request.Body = io.NopCloser(bytes.NewReader(rawBody))
				}
				userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
				reqCtx := &relayrouter.RequestContext{
					RequestID:       c.GetString(common.RequestIdKey),
					UserID:          userID,
					TokenID:         common.GetContextKeyInt(c, constant.ContextKeyTokenId),
					TokenGroup:      tokenGroup,
					TokenMetadata:   common.GetContextKeyString(c, constant.ContextKeyTokenMetadata),
					GinContext:      c,
					RequestedModel:  modelRequest.Model,
					ResolvedModel:   modelRequest.Model,
					CandidateGroups: groupCandidates,
					DefaultGroup:    model.ChannelLaneDirect,
				}
				reqCtx.Features = routingFeatures(c, reqCtx, rawBody)
				populateRoutingAttribution(c, reqCtx)
				selectedRouter := &relayrouter.PriorityRouter{}
				// Build the ordered list of models to try. The primary is
				// whatever the caller asked for (modelRequest.Model);
				// fallbacks are appended from Features.ModelsFallback
				// (OpenRouter-style top-level `models: [...]`). De-duped
				// while preserving order so an explicit primary doesn't
				// get re-tried.
				modelsToTry := []string{modelRequest.Model}
				if reqCtx.Features != nil && len(reqCtx.Features.ModelsFallback) > 0 {
					seen := map[string]struct{}{modelRequest.Model: {}}
					for _, m := range reqCtx.Features.ModelsFallback {
						m = strings.TrimSpace(m)
						if m == "" {
							continue
						}
						if _, dup := seen[m]; dup {
							continue
						}
						seen[m] = struct{}{}
						modelsToTry = append(modelsToTry, m)
					}
				}

				// Iterate the fallback chain. NoChannelError and protocol
				// capability mismatches are scoped to the current model, so a
				// later model can still be routable. Other failures are systemic
				// and abort immediately.
				var decision *relayrouter.Decision
				var lastErr error
				var lastNoChan *relayrouter.NoChannelError
				var lastUnsupported *textgen.UnsupportedFeatureError
				var winningDesiredLane string
				var winningResolvedModel string
				modelsTried := make([]string, 0, len(modelsToTry))
				// Whether any candidate model is in the catalog at all. When
				// none is, the caller asked for a model that does not exist;
				// otherwise the model exists and no route could serve it.
				anyRequestable := false
				tokenVendorGroups := getTokenVendorGroups(c)
				tokenModelLanes := getTokenModelLanes(c)
				for i, m := range modelsToTry {
					if i > 0 && !authorizeTokenModelAndVendor(c, m) {
						return
					}
					resolvedModel := m
					if isAutoModelRequest(m) {
						resolvedModel, err = resolveAutoModel(c, groupCandidates, model.IsModelRequestable)
						if err != nil {
							if errors.As(err, &lastNoChan) {
								// The auto alias exists; nothing could serve it.
								anyRequestable = true
								lastErr = err
								continue
							}
							var unsupported *textgen.UnsupportedFeatureError
							if errors.As(err, &unsupported) {
								lastErr = err
								lastUnsupported = unsupported
								continue
							}
							logger.LogError(c.Request.Context(), fmt.Sprintf("channel select failed: model=%s group=%s err=%s", modelRequest.Model, userGroup, err.Error()))
							abortWithCode(c, types.ErrorCodeChannelSelectFailed, types.D{"model": modelRequest.Model})
							return
						}
						// The alias grants no authority over the concrete selection.
						if !authorizeTokenModelAndVendor(c, resolvedModel) {
							return
						}
					}
					candidates, desiredLane, priceFallback := laneCandidatesForModel(resolvedModel, tokenModelLanes, tokenVendorGroups)
					if forced := c.GetString("force_lane"); forced != "" {
						candidates = []string{forced}
						desiredLane = forced
						priceFallback = false
					}
					if !model.IsModelRequestable(resolvedModel) {
						if desiredLane == model.ChannelLaneAgent {
							abortWithCodeStatus(c, http.StatusNotFound, types.ErrorCodePreferredLaneUnavailable, types.D{"model": resolvedModel})
							return
						}
						lastErr = &relayrouter.NoChannelError{Group: userGroup, Model: resolvedModel}
						continue
					}
					anyRequestable = true
					reqCtx.ResolvedModel = resolvedModel
					if i > 0 {
						// Re-run feature extraction's "resolved model"
						// only; rest of Features is request-derived and
						// doesn't change between fallback iterations.
						reqCtx.RequestedModel = modelRequest.Model
					}
					modelsTried = append(modelsTried, resolvedModel)
					if desiredLane == model.ChannelLaneAgent && len(candidates) == 0 {
						abortUnavailableCheapModel(c, resolvedModel, true)
						return
					}
					reqCtx.CandidateGroups = candidates
					if len(candidates) > 0 {
						reqCtx.DefaultGroup = candidates[0]
					}
					if priceFallback {
						setLaneFallbackHeader(c, desiredLane, model.ChannelLaneDirect)
					}
					setStructuredInputTokenEstimate(c, reqCtx, resolvedModel)
					routeTimer := common.StartHotPathStage(common.HotPathStageRoutingSelect)
					d, err := selectedRouter.Select(c.Request.Context(), reqCtx)
					routeTimer.Finish(err)
					if err == nil {
						decision = d
						winningDesiredLane = desiredLane
						winningResolvedModel = resolvedModel
						break
					}
					if desiredLane == model.ChannelLaneAgent {
						var unsupported *textgen.UnsupportedFeatureError
						if errors.As(err, &unsupported) {
							abortProtocolUnsupported(c, unsupported)
							return
						}
						abortUnavailableCheapModel(c, resolvedModel, false)
						return
					}
					if errors.As(err, &lastNoChan) {
						lastErr = err
						continue
					}
					var unsupported *textgen.UnsupportedFeatureError
					if errors.As(err, &unsupported) {
						lastErr = err
						lastUnsupported = unsupported
						continue
					}
					// Non-NoChannelError → systemic, abort.
					logger.LogError(c.Request.Context(), fmt.Sprintf("channel select failed: model=%s group=%s err=%s", modelRequest.Model, userGroup, err.Error()))
					abortWithCode(c, types.ErrorCodeChannelSelectFailed, types.D{"model": modelRequest.Model})
					return
				}
				if decision == nil {
					if lastUnsupported != nil {
						abortProtocolUnsupported(c, lastUnsupported)
						return
					}
					// Exhausted the chain with NoChannelError.
					if lastErr == nil {
						lastErr = lastNoChan
					}
					_ = lastErr
					if !anyRequestable {
						abortWithCode(c, types.ErrorCodeModelNotFound, types.D{"model": modelRequest.Model})
						return
					}
					abortWithCode(c, types.ErrorCodeNoAvailableChannel, types.D{"model": modelRequest.Model})
					return
				}
				// Stash the chain on context for telemetry — Decision.Meta
				// only reflects the winning iteration; the trail lives here.
				if len(modelsTried) > 1 {
					c.Set("models_tried", modelsTried)
				}
				channel = decision.Channel
				finalGroup := decision.Group
				if finalGroup == "" {
					finalGroup = model.ChannelLaneDirect
				}
				setLaneFallbackHeader(c, winningDesiredLane, finalGroup)
				// Replace modelRequest.Model with the model the router actually
				// resolved (e.g. "auto" → "MiniMax-Text-01", or the surviving
				// entry in a models[] fallback chain). Downstream pricing,
				// token counting and the upstream payload all key off
				// modelRequest.Model — keeping the original "auto" / first
				// fallback name there would crash pricing with
				// pricing_not_configured even though the router already
				// chose a viable channel.
				if resolved, ok := decision.Meta["resolved_model"].(string); ok && resolved != "" {
					modelRequest.Model = resolved
				} else if winningResolvedModel != "" {
					modelRequest.Model = winningResolvedModel
				}
				if !authorizeEphemeralCredential(c, modelRequest.Model, selectedProviderForCredentialScope(modelRequest.Model, channel)) {
					return
				}
				credentialAuthorized = true
				if decisionID := relayrouter.WriteDecisionLog(reqCtx, decision); decisionID > 0 {
					routingDecisionID = decisionID
					c.Set("routing_decision_id", decisionID)
				}
				common.SetContextKey(c, constant.ContextKeyUsingGroup, finalGroup)
				c.Set("final_group", finalGroup)
			}
		}
		if !credentialAuthorized && !authorizeEphemeralCredential(c, modelRequest.Model, selectedProviderForCredentialScope(modelRequest.Model, channel)) {
			return
		}
		if pinnedRequestContext != nil && pinnedDecision != nil {
			if decisionID := relayrouter.WriteDecisionLog(pinnedRequestContext, pinnedDecision); decisionID > 0 {
				routingDecisionID = decisionID
				c.Set("routing_decision_id", decisionID)
			}
		}
		SetupContextForSelectedChannel(c, channel, modelRequest.Model)
		// Report the lane actually selected for every routed request. The
		// fallback header only exists when a preferred lane fell back.
		if finalLane := c.GetString("final_group"); finalLane != "" {
			c.Header("X-Lazu-Lane", finalLane)
		}
		// Pre-call hook for the per-channel metrics store. Independent of
		// routing-experiment telemetry so token-specific-channel requests
		// also contribute to runtime health signals (in-flight, RPM, error
		// rate). See internal/relay/metrics.
		if channel != nil && !suppressRelayTelemetry {
			metrics.Default.OnPreCall(channel.Id)
		}
		// Decisions owns its health metrics, but its billing settlement still
		// needs the consume log and routing snapshot in one durable event.
		if !suppressRelayTelemetry || c.Request.URL.Path == "/v1/decisions" {
			model.MarkRelayResultFlushExpected(c)
			model.SetRelayOutcomeBuilder(c, func() *model.RoutingOutcome { return buildRoutingOutcome(c, routingDecisionID) })
		}
		c.Next()
		if !suppressRelayTelemetry {
			recordChannelMetrics(c, channel)
			recordRoutingOutcome(c, routingDecisionID)
		}
	}
}

// enrichTextgenRequestForRouting applies gateway-owned media hints after
// token/model authorization but before capability selection. The controller
// later enriches its relay DTO from the same request-scoped download cache;
// routing therefore sees the exact modalities that will reach the upstream.
func enrichTextgenRequestForRouting(c *gin.Context, modelName string) error {
	parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
	if !ok || parsed == nil || parsed.Semantic == nil || parsed.Client.Family != textgen.ProtocolOpenAIChat || !openai_chat.HasOSSURLHint(parsed.Semantic) {
		return nil
	}
	wire, ok := parsed.Wire.(*dto.GeneralOpenAIRequest)
	if !ok || wire == nil {
		return fmt.Errorf("parsed Chat request wire is missing")
	}
	if err := service.ApplyOSSURLToRequest(c, wire, modelName); err != nil {
		return err
	}
	effective := parsed.Semantic.Clone()
	if err := openai_chat.SyncEnrichedWire(effective, wire); err != nil {
		return err
	}
	common.SetContextKey(c, constant.ContextKeyOSSURLApplied, true)
	features, uses := textgen.ExtractFeatures(effective)
	parsedCopy := *parsed
	parsedCopy.Semantic = effective
	parsedCopy.Features = features
	parsedCopy.Uses = uses
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &parsedCopy)
	return nil
}

func routingFeatures(c *gin.Context, reqCtx *relayrouter.RequestContext, rawBody []byte) *relayrouter.Features {
	ctx := context.Background()
	if c != nil {
		ctx = c.Request.Context()
		if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); ok && parsed != nil && parsed.Semantic != nil {
			return relayrouter.ExtractSemanticFeatures(ctx, reqCtx, parsed.Semantic)
		}
	}
	return relayrouter.ExtractFeatures(ctx, reqCtx, rawBody)
}

// setStructuredInputTokenEstimate refreshes the planner's context-window
// text input estimate for the currently resolved model. Media admission reserves
// are deliberately conservative quota reservations, not measured context usage.
// Including them here can reject valid multi-image requests before the provider
// sees them. The relay controller still reserves media tokens for TPM and billing;
// the provider validates the complete multimodal context.
func setStructuredInputTokenEstimate(c *gin.Context, reqCtx *relayrouter.RequestContext, modelName string) {
	if c == nil || reqCtx == nil {
		return
	}
	parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
	if !ok || parsed == nil || parsed.Semantic == nil {
		return
	}
	format := types.RelayFormatOpenAI
	if parsed.Client.Family == textgen.ProtocolOpenAIResponses {
		format = types.RelayFormatOpenAIResponses
	}
	meta := textgen.TokenCountMeta(parsed.Semantic)
	meta.Files = nil
	estimate, err := service.EstimateRequestTPMTokens(meta, &relaycommon.RelayInfo{
		OriginModelName: modelName,
		RelayFormat:     format,
	})
	if err != nil {
		return
	}
	parsedCopy := *parsed
	parsedCopy.InputTokens = estimate
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &parsedCopy)
}

func abortProtocolUnsupported(c *gin.Context, unsupported *textgen.UnsupportedFeatureError) {
	common.IncrementHotPathCounter(common.HotPathCounterProtocolUnsupported)
	details := types.D{}
	if unsupported != nil {
		details["reason"] = unsupported.Error()
		details["feature"] = unsupported.Feature.String()
		if unsupported.Path != "" {
			details["field"] = unsupported.Path
		}
		common.IncrementHotPathCounter(common.HotPathCounterProtocolUnsupportedPrefix + unsupported.Feature.String())
	}
	abortWithCode(c, types.ErrorCodeProtocolBridgeUnsupported, details)
}

func populateRoutingAttribution(c *gin.Context, reqCtx *relayrouter.RequestContext) {
	if c == nil || reqCtx == nil {
		return
	}
	// Group the request into its task: the agent's own session id when it
	// sends one, otherwise the Key and day. See agent_session.go.
	reqCtx.SessionID = applyAgentSession(c, reqCtx.Features, reqCtx.TokenID).ID

	clientInfo := common.ClientInfo{Framework: common.GetContextKeyString(c, constant.ContextKeyClientFramework)}
	reqCtx.AgentID = resolveAgentID(c, clientInfo)
	common.SetContextKey(c, constant.ContextKeyAgentID, reqCtx.AgentID)
	common.SetContextKey(c, constant.ContextKeyRealClientIP, common.GetRealClientIP(c))
	// Cloudflare provides a cheap geo signal for dashboards and abuse review.
	// Take it only from a trusted proxy and only in Cloudflare's own shape
	// (two letters or digits, e.g. "US", "T1"); anything else is a header the
	// caller wrote.
	if country := cloudflareCountry(c); country != "" {
		common.SetContextKey(c, constant.ContextKeyClientCountry, country)
	}
}

func cloudflareCountry(c *gin.Context) string {
	if !common.RequestFromTrustedProxy(c) {
		return ""
	}
	country := strings.ToUpper(strings.TrimSpace(c.GetHeader("CF-IPCountry")))
	if len(country) != 2 {
		return ""
	}
	for _, r := range country {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return country
}

func SuppressRelayTelemetry() gin.HandlerFunc {
	return func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeySuppressRelayTelemetry, true)
		c.Next()
	}
}

// RecordManualRelayPreCall starts channel-health accounting for relay paths
// that perform the upstream request outside the standard Relay handler.
func RecordManualRelayPreCall(channelID int) {
	if channelID > 0 {
		metrics.Default.OnPreCall(channelID)
	}
}

// RecordManualRelayPostCall completes channel-health accounting for one
// manually executed upstream attempt.
func RecordManualRelayPostCall(channelID int, startedAt time.Time, statusCode int, errorCode string) {
	if channelID <= 0 {
		return
	}
	latency := time.Since(startedAt)
	if latency < 0 {
		latency = 0
	}
	metrics.Default.OnPostCall(channelID, latency, 0, categorizeForMetrics(statusCode, errorCode))
}

// SetManualRoutingOutcome records the provider-facing result that should feed
// routing experiments even if later local persistence changes the HTTP result.
func SetManualRoutingOutcome(c *gin.Context, statusCode int, errorCode string) {
	if c == nil {
		return
	}
	c.Set(manualRoutingOutcomeOverrideKey, true)
	c.Set(manualRoutingOutcomeStatusKey, statusCode)
	c.Set(manualRoutingOutcomeErrorCodeKey, errorCode)
	c.Set(manualRoutingOutcomeErrorTypeKey, "")
}

// RecordManualRoutingOutcome records the final request outcome for paths that
// suppress the distributor's automatic post-call telemetry.
func RecordManualRoutingOutcome(c *gin.Context) {
	if c == nil {
		return
	}
	value, ok := c.Get("routing_decision_id")
	if !ok {
		return
	}
	var decisionID int64
	switch typed := value.(type) {
	case int64:
		decisionID = typed
	case int:
		decisionID = int64(typed)
	}
	recordRoutingOutcome(c, decisionID)
}

// RecordManualRoutingOutcomeSync makes the current manual verdict durable
// before returning. It is used by asynchronous provider workflows before they
// expose a job to polling workers.
func RecordManualRoutingOutcomeSync(c *gin.Context) error {
	if c == nil {
		return fmt.Errorf("routing outcome context is required")
	}
	value, ok := c.Get("routing_decision_id")
	if !ok {
		return fmt.Errorf("routing decision is not available")
	}
	var decisionID int64
	switch typed := value.(type) {
	case int64:
		decisionID = typed
	case int:
		decisionID = int64(typed)
	}
	outcome := buildRoutingOutcome(c, decisionID)
	if outcome == nil {
		return fmt.Errorf("routing outcome is not available")
	}
	return model.PersistManualRoutingOutcome(c, outcome)
}

func authorizeEphemeralCredential(c *gin.Context, modelName string, provider string) bool {
	credential, ok := common.GetContextKeyType[*model.EphemeralCredential](c, constant.ContextKeyEphemeralCredential)
	if !ok || credential == nil {
		return true
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	sessionID := applyAgentSession(c, nil, tokenID).ID
	agentID := common.GetContextKeyString(c, constant.ContextKeyAgentID)
	if agentID == "" {
		clientInfo := common.ParseClient(c.GetHeader("User-Agent"))
		agentID = resolveAgentID(c, clientInfo)
		if agentID != "" {
			common.SetContextKey(c, constant.ContextKeyAgentID, agentID)
		}
	}
	now := time.Now().UTC()
	audit := model.EphemeralCredentialAudit{
		CredentialID:       credential.ID,
		CredentialPublicID: credential.PublicID,
		EventType:          model.EphemeralCredentialAuditUse,
		UserID:             common.GetContextKeyInt(c, constant.ContextKeyUserId),
		TokenID:            tokenID,
		SessionID:          sessionID,
		AgentID:            agentID,
		RequestID:          c.GetString(common.RequestIdKey),
		ModelName:          modelName,
		Provider:           provider,
		CreatedAt:          now.Unix(),
	}
	// The official wire model and the Decisions billing identifier name the
	// same endpoint-scoped operation. Keep all other credential restrictions.
	scopedCredential := credential
	if publicModel := decisionAuthorizationModel(c, modelName); publicModel != modelName && slices.Contains(credential.Models, publicModel) {
		copy := *credential
		copy.Models = append(slices.Clone(credential.Models), modelName)
		scopedCredential = &copy
	}
	if err := model.ValidateEphemeralCredentialRequest(scopedCredential, modelName, provider, sessionID); err != nil {
		audit.EventType = model.EphemeralCredentialAuditDenied
		audit.Reason = err.Error()
		model.RecordEphemeralCredentialAudit(audit)
		abortWithCode(c, types.ErrorCodeEphemeralCredentialDenied, types.D{"reason": err.Error()})
		return false
	}
	model.MarkEphemeralCredentialUsed(credential.ID, now)
	model.RecordEphemeralCredentialAudit(audit)
	return true
}

func selectedProviderForCredentialScope(modelName string, channel *model.Channel) string {
	if channel != nil {
		if name := strings.TrimSpace(channel.GetTag()); name != "" {
			return name
		}
		if name := strings.TrimSpace(constant.GetChannelTypeName(channel.Type)); name != "" && !strings.EqualFold(name, "Unknown") {
			return name
		}
		if name := strings.TrimSpace(channel.Name); name != "" {
			return name
		}
	}
	if _, vendorName := model.GetModelVendorMeta(modelName); vendorName != "" {
		return vendorName
	}
	return ""
}

func isAutoModelRequest(modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case "auto", "smart", "best", "lazu/auto":
		return true
	default:
		return false
	}
}

// resolveAutoModel translates the accepted auto aliases into a concrete,
// requestable model before baseline priority routing. It deliberately keeps
// the policy small: candidate groups are checked in order, then enabled
// channels by priority/weight, then models in the channel's declared order.
func resolveAutoModel(c *gin.Context, groups []string, isRequestable func(string) bool) (string, error) {
	lastGroup := model.ChannelLaneDirect
	for _, group := range groups {
		if group == "" {
			continue
		}
		lastGroup = group
		channels, selectedGroup, err := model.CacheGetAllChannelsInGroup(c, group)
		if err != nil {
			return "", err
		}
		if selectedGroup != "" {
			lastGroup = selectedGroup
		}
		sort.SliceStable(channels, func(i, j int) bool {
			if channels[i] == nil {
				return false
			}
			if channels[j] == nil {
				return true
			}
			if channels[i].GetPriority() != channels[j].GetPriority() {
				return channels[i].GetPriority() > channels[j].GetPriority()
			}
			if channels[i].GetWeight() != channels[j].GetWeight() {
				return channels[i].GetWeight() > channels[j].GetWeight()
			}
			return channels[i].Id < channels[j].Id
		})
		for _, channel := range channels {
			if channel == nil {
				continue
			}
			for _, candidate := range channel.GetModels() {
				candidate = strings.TrimSpace(candidate)
				if candidate != "" && isRequestable(candidate) {
					return candidate, nil
				}
			}
		}
	}
	return "", &relayrouter.NoChannelError{Group: lastGroup, Model: "auto"}
}

func getTokenVendorGroups(c *gin.Context) model.TokenVendorGroupMap {
	if groups, ok := common.GetContextKeyType[model.TokenVendorGroupMap](c, constant.ContextKeyTokenVendorGroups); ok {
		return groups.Normalize()
	}
	return model.TokenVendorGroupMap{}
}

func getTokenModelLanes(c *gin.Context) model.TokenModelLaneMap {
	if lanes, ok := common.GetContextKeyType[model.TokenModelLaneMap](c, constant.ContextKeyTokenModelLanes); ok {
		return lanes.Normalize()
	}
	return model.TokenModelLaneMap{}
}

func laneForChannel(channel *model.Channel) string {
	if channel == nil {
		return model.ChannelLaneDirect
	}
	for _, group := range channel.GetGroups() {
		lane := model.NormalizeChannelLane(group)
		if model.IsValidChannelLane(lane) {
			return lane
		}
	}
	return model.ChannelLaneDirect
}

func laneCandidatesForModel(modelName string, tokenModelLanes model.TokenModelLaneMap, tokenVendorGroups model.TokenVendorGroupMap) ([]string, string, bool) {
	return model.LaneCandidatesForModel(modelName, tokenModelLanes, tokenVendorGroups)
}

// setLaneFallbackHeader tells the caller their requested lane was not used. Any
// lane can fall back now, not only 便宜, so the header names the pair rather
// than hard-coding one transition.
func setLaneFallbackHeader(c *gin.Context, desiredLane, finalLane string) {
	if c == nil {
		return
	}
	desired := model.NormalizeChannelLane(desiredLane)
	final := model.NormalizeChannelLane(finalLane)
	if desired == final || desired == model.ChannelLaneDirect {
		return
	}
	if final != model.ChannelLaneDirect {
		return
	}
	c.Header("X-Lazu-Lane-Fallback", desired+"->"+final)
	common.SetContextKey(c, constant.ContextKeyLaneRequested, desired)
}

// recordChannelMetrics is the post-call hook for the per-channel metrics
// store. Unlike recordRoutingOutcome it fires for *every* relayed request
// (including token-pinned-channel calls that skip the router), so the
// in-flight gauge and the rolling error window track real upstream load
// rather than only experiment traffic.
func recordChannelMetrics(c *gin.Context, channel *model.Channel) {
	if c == nil || channel == nil || channel.Id <= 0 {
		return
	}
	now := time.Now()
	latency := time.Duration(0)
	if start := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime); !start.IsZero() {
		if d := now.Sub(start); d > 0 {
			latency = d
		}
	}
	status := c.Writer.Status()
	errCode := common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode)
	cat := metrics.CategoryClientFault
	if common.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) {
		cat = categorizeForMetrics(status, errCode)
	}
	// Token accounting is not surfaced into gin.Context today, so TPM
	// bucket stays empty for now. RPM still works (incremented in the
	// pre-call hook). Wiring real token counts is tracked alongside the
	// routing_outcome.token_usage backfill — see internal/worker/routing_quality_backfill.go.
	metrics.Default.OnPostCall(channel.Id, latency, 0, cat)
}

func resolveAgentID(c *gin.Context, clientInfo common.ClientInfo) string {
	if c != nil {
		for _, header := range []string{"X-Agent-Id", "X-Lazu-Agent-Id"} {
			if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
				if len(value) > 64 {
					value = value[:64]
				}
				return value
			}
		}
	}
	if clientInfo.Framework != "" {
		if len(clientInfo.Framework) > 64 {
			return clientInfo.Framework[:64]
		}
		return clientInfo.Framework
	}
	return ""
}

// categorizeForMetrics maps the request's outcome (HTTP status + emitted
// error code) into one of the metrics.ErrorCategory buckets so the store
// knows which signals to feed into the rolling error window.
//
//   - clean 2xx → OK; a recorded post-header stream error → ChannelFault
//   - errors tagged as content-policy / context-window via the error
//     catalog → not the channel's fault, skipped from the health window
//   - 429 → RateLimited (counts against the channel; can trip cooldown)
//   - other 4xx (400/401/403/404/422) → ClientFault, not counted
//   - 5xx / network / unknown → ChannelFault, counted
//
// This mirrors Kong's "client errors don't trigger failover" rule.
func categorizeForMetrics(statusCode int, errorCode string) metrics.ErrorCategory {
	if errorCode != "" {
		if types.ErrorCode(errorCode) == types.ErrorCodeConvertRequestFailed {
			return metrics.CategoryClientFault
		}
		switch types.GetFallbackCategory(types.ErrorCode(errorCode)) {
		case types.FallbackContentPolicy:
			return metrics.CategoryContentPolicy
		case types.FallbackContextWindow:
			return metrics.CategoryContextWindow
		}
		if types.ErrorCode(errorCode) == types.ErrorCodeClientCanceled || types.ErrorCode(errorCode) == types.ErrorCodeDownstreamWriteFailed {
			return metrics.CategoryClientFault
		}
		if statusCode >= 200 && statusCode < 300 {
			return metrics.CategoryChannelFault
		}
	}
	if statusCode >= 200 && statusCode < 300 {
		return metrics.CategoryOK
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		return metrics.CategoryRateLimited
	case statusCode >= 500:
		return metrics.CategoryChannelFault
	case statusCode >= 400:
		return metrics.CategoryClientFault
	}
	// Unknown / network / connection errors that never produced an HTTP
	// status: treat as channel fault so they decay the rolling reliability
	// score rather than disappearing silently.
	return metrics.CategoryChannelFault
}

func recordRoutingOutcome(c *gin.Context, decisionID int64) {
	timer := common.StartRelayPhase(c, common.RelayPhaseRoutingFinish)
	defer timer.Finish()
	outcome := buildRoutingOutcome(c, decisionID)
	recordRouteBreakerEvidence(c, outcome)
	if model.FlushRelayResultEvent(c, outcome) {
		return
	}
	if outcome != nil {
		_ = model.RecordRoutingOutcome(outcome)
	}
}

// recordRouteBreakerEvidence folds this request's verdict into the route's
// breaker, so a failing upstream stops being chosen instead of failing once
// per user until someone notices.
//
// It reuses the health verdict already computed for the outcome row rather
// than re-deciding what counts as a fault: the breaker and the health page
// must never disagree about whether a request was the channel's fault. Only
// eligible evidence moves it — a malformed request or a content-policy refusal
// says nothing about the upstream, and letting those trip a breaker would let
// one user's bad payloads pull a healthy source out of everyone's rotation.
func recordRouteBreakerEvidence(c *gin.Context, outcome *model.RoutingOutcome) {
	if c == nil || outcome == nil || outcome.ChannelID == nil || *outcome.ChannelID <= 0 {
		return
	}
	if outcome.HealthEligible == nil || !*outcome.HealthEligible {
		return
	}
	modelName := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyRequestModel))
	if modelName == "" {
		modelName = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
	}
	if modelName == "" {
		return
	}
	failed := outcome.HealthFailed != nil && *outcome.HealthFailed
	channelID := *outcome.ChannelID
	lane := outcome.FinalGroup
	if !model.ShouldRecordRouteHealthEvidence(modelName, lane, channelID, !failed) {
		// The common case by far: a healthy route answering normally. It has
		// nothing to record, so it must not cost a goroutine or a row lock.
		return
	}
	// The retried attempts of this same request may already have spoken for
	// this route; the breaker counts requests, not attempts.
	if failed && !model.ClaimRouteHealthFailure(c, modelName, lane, channelID) {
		return
	}
	reason := outcome.ErrorCode
	if reason == "" {
		reason = outcome.ErrorType
	}
	at := outcome.OutcomeAt
	// The relay has already answered the caller; a breaker write must never be
	// on the path that returns their bytes.
	common.SafeGo("route-breaker-evidence", func() {
		if err := model.RecordRouteHealthEvidence(modelName, lane, channelID, !failed, reason, at); err != nil {
			common.SysLog("failed to record route health evidence: " + err.Error())
		}
	})
}

func buildRoutingOutcome(c *gin.Context, decisionID int64) *model.RoutingOutcome {
	if c == nil {
		return nil
	}

	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		return nil
	}
	now := time.Now().UTC()
	statusCode := c.Writer.Status()
	errorCode := common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode)
	errorType := common.GetContextKeyString(c, constant.ContextKeyRequestErrorType)
	if c.GetBool(manualRoutingOutcomeOverrideKey) {
		statusCode = c.GetInt(manualRoutingOutcomeStatusKey)
		errorCode = c.GetString(manualRoutingOutcomeErrorCodeKey)
		errorType = c.GetString(manualRoutingOutcomeErrorTypeKey)
	}
	providerAttempted := common.GetContextKeyBool(c, constant.ContextKeyProviderAttempted)
	healthEligible, healthFailed := types.ChannelHealthVerdict(
		statusCode, types.ErrorCode(errorCode), providerAttempted,
	)
	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	finalGroup := model.NormalizeChannelLane(c.GetString("final_group"))
	if finalGroup == "" {
		finalGroup = model.NormalizeChannelLane(common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
	}
	usedChannels := c.GetStringSlice("use_channel")
	isRetry := len(usedChannels) > 1
	retryCount := 0
	if isRetry {
		retryCount = len(usedChannels) - 1
	}
	totalDurationMs := 0
	if start := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime); !start.IsZero() {
		totalDurationMs = int(now.Sub(start).Milliseconds())
		if totalDurationMs < 0 {
			totalDurationMs = 0
		}
	}
	outcome := &model.RoutingOutcome{
		RequestID:         requestID,
		OutcomeAt:         now,
		DecisionID:        decisionID,
		UserID:            intPtrOrNil(common.GetContextKeyInt(c, constant.ContextKeyUserId)),
		ProjectID:         intPtrOrNil(common.GetContextKeyInt(c, constant.ContextKeyProjectID)),
		ModelName:         common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
		ChannelID:         intPtrOrNil(channelID),
		ChannelName:       common.GetContextKeyString(c, constant.ContextKeyChannelName),
		FinalGroup:        finalGroup,
		StatusCode:        intPtrOrNil(statusCode),
		ErrorCode:         errorCode,
		ErrorType:         errorType,
		ProviderAttempted: boolPtr(providerAttempted),
		HealthEligible:    boolPtr(healthEligible),
		HealthFailed:      boolPtr(healthFailed),
		IsRetry:           &isRetry,
		RetryCount:        &retryCount,
		TotalDurationMs:   &totalDurationMs,
		WasStreaming:      boolPtr(common.GetContextKeyBool(c, constant.ContextKeyIsStream)),
		// A connection can close normally after the response has been read but
		// before accounting completes. Only a recorded streaming failure is
		// evidence of interruption; the context's final state is not.
		StreamAborted: boolPtr(common.GetContextKeyBool(c, constant.ContextKeyIsStream) &&
			c.Writer.Written() && strings.HasPrefix(strings.ToLower(c.Writer.Header().Get("Content-Type")), "text/event-stream") && errorCode != ""),
	}
	if relayInfo, ok := common.GetContextKeyType[*relaycommon.RelayInfo](c, constant.ContextKeyRelayInfo); ok && relayInfo != nil {
		if outcome.WasStreaming != nil && *outcome.WasStreaming && relayInfo.HasSendResponse() {
			// Time to the first event carrying output, not to the first frame:
			// a stream that opens with a role chunk or response.created would
			// otherwise report its metadata latency as first-token time. A
			// stream that produced no output at all has no first token.
			if ttft := relayInfo.OutputTTFT(relayInfo.StartTime); ttft > 0 {
				ttftMs := int(ttft.Milliseconds())
				outcome.TTFTMs = &ttftMs
				// Zero, not NULL, when output streamed but none of it was an
				// answer — a reply that only reasoned. NULL is kept for rows
				// written before the answer time existed.
				answerMs := int(relayInfo.AnswerTTFT(relayInfo.StartTime).Milliseconds())
				outcome.AnswerTTFTMs = &answerMs
			}
			// A reply of one chunk has no gap to report, and a zero here would
			// read as "measured, and perfect" rather than "nothing to measure",
			// so it is left NULL. intPtrOrNil already drops zero for us.
			outcome.MaxStallMs = intPtrOrNil(relayInfo.MaxStallMs)
		}
		outcome.FinishReason = relayInfo.FinishReason
	}
	applyRoutingOutcomeUsageSnapshot(c, outcome)
	if v, ok := c.Get("reported_reasoning_tokens"); ok {
		if n, ok := v.(int); ok {
			outcome.ReasoningTokens = &n
		}
	}
	return outcome
}

func applyRoutingOutcomeUsageSnapshot(c *gin.Context, outcome *model.RoutingOutcome) {
	if c == nil || outcome == nil {
		return
	}
	value, ok := common.GetContextKey(c, constant.ContextKeyRoutingOutcomeUsage)
	if !ok {
		return
	}
	var snapshot model.RoutingOutcomeUsageSnapshot
	switch typed := value.(type) {
	case model.RoutingOutcomeUsageSnapshot:
		snapshot = typed
	case *model.RoutingOutcomeUsageSnapshot:
		if typed == nil {
			return
		}
		snapshot = *typed
	default:
		return
	}
	outcome.ActualInputTokens = intPtrOrNil(snapshot.ActualInputTokens)
	outcome.CachedInputTokens = intPtrOrNil(snapshot.CachedInputTokens)
	outcome.CacheWriteTokens = intPtrOrNil(snapshot.CacheWriteTokens)
	outcome.CacheWrite5mTokens = intPtrOrNil(snapshot.CacheWrite5mTokens)
	outcome.CacheWrite1hTokens = intPtrOrNil(snapshot.CacheWrite1hTokens)
	outcome.CacheMissTokens = intPtrOrNil(snapshot.CacheMissTokens)
	outcome.ActualOutputTokens = intPtrOrNil(snapshot.ActualOutputTokens)
	outcome.ReasoningTokens = intPtrOrNil(snapshot.ReasoningTokens)
	outcome.AudioInputTokens = intPtrOrNil(snapshot.AudioInputTokens)
	outcome.AudioOutputTokens = intPtrOrNil(snapshot.AudioOutputTokens)
	outcome.ImageInputTokens = intPtrOrNil(snapshot.ImageInputTokens)
	outcome.ImageOutputTokens = intPtrOrNil(snapshot.ImageOutputTokens)
	if snapshot.HasChargedCost {
		outcome.ChargedCostUSD = &snapshot.ChargedCostUSD
	}
	if snapshot.HasUpstreamCost {
		outcome.UpstreamCostUSD = &snapshot.UpstreamCostUSD
	}
	if snapshot.HasGrossMargin {
		outcome.GrossMarginUSD = &snapshot.GrossMarginUSD
	}
}

func intPtrOrNil(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func authorizeTokenModelAndVendor(c *gin.Context, modelName string) bool {
	publicModel := decisionAuthorizationModel(c, modelName)
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
		if !ok {
			abortWithCode(c, types.ErrorCodeTokenNoModelAccess)
			return false
		}
		tokenModelLimit, ok := s.(map[string]bool)
		if !ok {
			tokenModelLimit = map[string]bool{}
		}
		scopeVendors, _ := common.GetContextKeyType[map[int]struct{}](c, constant.ContextKeyTokenModelScopeVendors)
		allowed := model.TokenModelScopeAllows(tokenModelLimit, scopeVendors, modelName)
		if !allowed && publicModel != modelName {
			allowed = model.TokenModelScopeAllows(tokenModelLimit, scopeVendors, publicModel)
		}
		if !allowed {
			abortWithCode(c, types.ErrorCodeTokenModelDenied, types.D{"model": modelName})
			return false
		}
	}

	if !memberModelAllows(c, modelName) && (publicModel == modelName || !memberModelAllows(c, publicModel)) {
		abortWithCode(c, types.ErrorCodeTokenModelDenied, types.D{"model": modelName})
		return false
	}

	return authorizeTokenVendor(c, modelName)
}

// Only a validated native Decisions request may resolve the public OpenAI
// model to its separate billing identifier. Chat cannot inherit this grant.
func decisionAuthorizationModel(c *gin.Context, modelName string) string {
	if c.Request == nil || c.Request.URL.Path != "/v1/decisions" || modelName != dto.OpenAIDecisionModel {
		return modelName
	}
	value, _ := c.Get("decision_request")
	request, ok := value.(*dto.DecisionRequest)
	if !ok || request == nil || request.OpenAI == nil || request.Model != modelName {
		return modelName
	}
	return "gpt-6-luna"
}

func authorizeTokenVendor(c *gin.Context, modelName string) bool {
	if common.GetContextKeyBool(c, constant.ContextKeyTokenVendorLimitEnabled) {
		if vendorLimits, ok := common.GetContextKeyType[model.IntSlice](c, constant.ContextKeyTokenVendorLimits); ok {
			vendorID, vendorName := model.GetModelVendorMeta(modelName)
			for _, blockedVendor := range vendorLimits {
				if blockedVendor == vendorID {
					name := vendorName
					if name == "" {
						name = fmt.Sprintf("ID=%d", vendorID)
					}
					abortWithCode(c, types.ErrorCodeTokenVendorDenied, types.D{"vendor": name})
					return false
				}
			}
		}
	}

	return true
}

// peekRequestedModel does a best-effort, non-destructive read of the model a
// relay request named, for TokenAuth to publish before Distribute runs (see
// its call site). It covers the common cases — a JSON body's "model" field
// and the two path-encoded forms — and silently gives up on anything else;
// getModelRequest (below) remains the source of truth once Distribute runs
// and is not skipped when this already found a value, since it also carries
// route-specific overrides (form fields, playground defaults) this does not.
func peekRequestedModel(c *gin.Context) {
	if c.Request == nil {
		return
	}
	if common.GetContextKeyString(c, constant.ContextKeyRequestModel) != "" {
		return
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") || strings.HasPrefix(c.Request.URL.Path, "/v1/models/") {
		if modelName := extractModelNameFromGeminiPath(c.Request.URL.Path); modelName != "" {
			common.SetContextKey(c, constant.ContextKeyRequestModel, modelName)
		}
		return
	}
	if strings.HasSuffix(c.Request.URL.Path, "embeddings") {
		if modelName := c.Param("model"); modelName != "" {
			common.SetContextKey(c, constant.ContextKeyRequestModel, modelName)
			return
		}
	}
	if c.Request == nil || c.Request.Body == nil ||
		strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		return
	}
	body, err := common.GetRequestBody(c)
	if err != nil {
		abortRequestBodyError(c, err)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	// encoding/json accepts case-insensitive names and chooses the last
	// duplicate. Authorization must see the same model as every later DTO,
	// including media/search routes that do not use the textgen codec.
	var modelValue gjson.Result
	modelFields := 0
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		if strings.EqualFold(key.Str, "model") {
			modelFields++
			modelValue = value
		}
		return modelFields < 2
	})
	if modelFields > 1 {
		abortRequestBodyError(c, errors.New("duplicate model field"))
		return
	}
	if modelValue.Type == gjson.String && modelValue.Str != "" {
		common.SetContextKey(c, constant.ContextKeyRequestModel, modelValue.Str)
	}
}

func getModelRequest(c *gin.Context) (*ModelRequest, bool, error) {
	if c.Request.URL.Path == "/v1/decisions" {
		if value, ok := c.Get("decision_request"); ok {
			if request, ok := value.(*dto.DecisionRequest); ok && request != nil {
				return &ModelRequest{Model: request.Model}, true, nil
			}
		}
	}
	if parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed); ok && parsed != nil && parsed.Semantic != nil {
		return &ModelRequest{Model: parsed.Semantic.Model.Canonical}, true, nil
	}
	if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		// Match Gin's default form memory threshold, but propagate read/limit
		// errors instead of allowing PostForm to hide them as a missing model.
		if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
			return nil, false, fmt.Errorf("invalid multipart request: %w", err)
		}
	}
	var modelRequest ModelRequest
	shouldSelectChannel := true
	var err error
	if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") || strings.HasPrefix(c.Request.URL.Path, "/v1/models/") {
		// Gemini API 路径处理: /v1beta/models/gemini-2.0-flash:generateContent
		relayMode := relayconstant.RelayModeGemini
		modelName := extractModelNameFromGeminiPath(c.Request.URL.Path)
		if modelName != "" {
			modelRequest.Model = modelName
		}
		c.Set("relay_mode", relayMode)
	} else if !strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") && !strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		err = common.UnmarshalBodyReusable(c, &modelRequest)
	}
	if err != nil {
		return nil, false, fmt.Errorf("invalid request: %w", err)
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/realtime") {
		modelRequest.Model = c.Query("model")
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/moderations") {
		if modelRequest.Model == "" {
			modelRequest.Model = "text-moderation-stable"
		}
	}
	if strings.HasSuffix(c.Request.URL.Path, "embeddings") {
		if modelRequest.Model == "" {
			modelRequest.Model = c.Param("model")
		}
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/images/generations") {
		modelRequest.Model = strings.TrimSpace(modelRequest.Model)
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1/images/edits") {
		contentType := c.ContentType()
		if slices.Contains([]string{gin.MIMEPOSTForm, gin.MIMEMultipartPOSTForm}, contentType) {
			modelRequest.Model = c.PostForm("model")
		}
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/audio") {
		relayMode := relayconstant.RelayModeAudioSpeech
		if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/speech") {
			modelRequest.Model = strings.TrimSpace(modelRequest.Model)
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/translations") {
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, c.PostForm("model"))
			relayMode = relayconstant.RelayModeAudioTranslation
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") {
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, c.PostForm("model"))
			relayMode = relayconstant.RelayModeAudioTranscription
		}
		c.Set("relay_mode", relayMode)
	}
	if strings.HasPrefix(c.Request.URL.Path, "/pg/chat/completions") {
		// playground chat completions
		err = common.UnmarshalBodyReusable(c, &modelRequest)
		if err != nil {
			return nil, false, fmt.Errorf("invalid request: %w", err)
		}
		common.SetContextKey(c, constant.ContextKeyTokenGroup, modelRequest.Group)
	}
	return &modelRequest, shouldSelectChannel, nil
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) *types.LazuError {
	c.Set("original_model", modelName) // for retry
	if channel == nil {
		return types.NewError(errors.New("channel is nil"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(c, constant.ContextKeyChannelName, channel.Name)
	common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
	common.SetContextKey(c, constant.ContextKeyChannelCreateTime, channel.CreatedTime)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, channel.GetSetting())
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, channel.GetOtherSettings())
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, channel.GetParamOverride())
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, channel.GetHeaderOverride())
	if nil != channel.OpenAIOrganization && *channel.OpenAIOrganization != "" {
		common.SetContextKey(c, constant.ContextKeyChannelOrganization, *channel.OpenAIOrganization)
	}
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, channel.GetAutoBan())
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, channel.GetModelMapping())
	common.SetContextKey(c, constant.ContextKeyChannelStatusCodeMapping, channel.GetStatusCodeMapping())

	key, index, lazuErr := channel.GetNextEnabledKey()
	if lazuErr != nil {
		return lazuErr
	}
	if channel.ChannelInfo.IsMultiKey {
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
		common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
	} else {
		// 必须设置为 false，否则在重试到单个 key 的时候会导致日志显示错误
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, false)
	}
	// c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())

	switch channel.Type {
	case constant.ChannelTypeGemini:
		common.SetContextKey(c, constant.ContextKeyChannelAPIVersion, channel.Other)
	}
	return nil
}

// extractModelNameFromGeminiPath 从 Gemini API URL 路径中提取模型名
// 输入格式: /v1beta/models/gemini-2.0-flash:generateContent
// 输出: gemini-2.0-flash
func extractModelNameFromGeminiPath(path string) string {
	// 查找 "/models/" 的位置
	modelsPrefix := "/models/"
	modelsIndex := strings.Index(path, modelsPrefix)
	if modelsIndex == -1 {
		return ""
	}

	// 从 "/models/" 之后开始提取
	startIndex := modelsIndex + len(modelsPrefix)
	if startIndex >= len(path) {
		return ""
	}

	// 查找 ":" 的位置，模型名在 ":" 之前
	colonIndex := strings.Index(path[startIndex:], ":")
	if colonIndex == -1 {
		// 如果没有找到 ":"，返回从 "/models/" 到路径结尾的部分
		return path[startIndex:]
	}

	// 返回模型名部分
	return path[startIndex : startIndex+colonIndex]
}

// Only queried on rejected requests. A configured but unavailable channel is
// transient; an unoffered model is not. Missing prices on enabled offerings
// are operator configuration errors, never a reason to charge another lane.
func abortUnavailableCheapModel(c *gin.Context, modelName string, missingPrice bool) {
	var configured, enabled int64
	if common.MemoryCacheEnabled || (model.CoreDB() != nil && model.CoreDB().Dialector.Name() == "postgres") {
		var ready bool
		configured, enabled, ready = model.CachedLaneOffering(modelName, model.ChannelLaneAgent)
		if !ready {
			c.Header("Retry-After", "1")
			abortWithCodeStatus(c, http.StatusServiceUnavailable, types.ErrorCodeGatewayTemporarilyUnavailable)
			return
		}
	} else {
		// Embedded SQLite development preserves its direct SQL selector.
		err := model.CoreDB().Model(&model.Ability{}).Where(map[string]interface{}{"model": modelName, "group": model.ChannelLaneAgent}).Count(&configured).Error
		if err != nil {
			abortWithCode(c, types.ErrorCodeQueryDataError)
			return
		}
		if missingPrice {
			if err := model.CoreDB().Model(&model.Ability{}).Where(map[string]interface{}{"model": modelName, "group": model.ChannelLaneAgent, "enabled": true}).Count(&enabled).Error; err != nil {
				abortWithCode(c, types.ErrorCodeQueryDataError)
				return
			}
		}
	}
	if configured == 0 {
		abortWithCodeStatus(c, http.StatusNotFound, types.ErrorCodePreferredLaneUnavailable, types.D{"model": modelName})
		return
	}
	if missingPrice && model.IsValidChannelLane(model.ChannelLaneAgent) {
		if enabled > 0 {
			c.Header("x-should-retry", "false")
			abortWithCode(c, types.ErrorCodePricingNotConfigured, types.D{"model": modelName})
			return
		}
	}
	abortWithCodeStatus(c, http.StatusServiceUnavailable, types.ErrorCodePreferredLaneUnavailable, types.D{"model": modelName})
}
