package service

import (
	"github.com/lazu-ai/lazu/internal/types"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"

	"github.com/gin-gonic/gin"
)

func appendRequestPath(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other map[string]interface{}) {
	if other == nil {
		return
	}
	if ctx != nil && ctx.Request != nil && ctx.Request.URL != nil {
		if path := ctx.Request.URL.Path; path != "" {
			other["request_path"] = path
			return
		}
	}
	if relayInfo != nil && relayInfo.RequestURLPath != "" {
		path := relayInfo.RequestURLPath
		if idx := strings.Index(path, "?"); idx != -1 {
			path = path[:idx]
		}
		other["request_path"] = path
	}
}

// appendEffortCapInfo records the reasoning-effort cap that applied to the
// request and, when the gateway lowered the effort, what was asked and what
// ran. "effort_clamped" is what the console counts as 被降.
func appendEffortCapInfo(ctx *gin.Context, other map[string]interface{}) {
	if ctx == nil || other == nil {
		return
	}
	cap := common.GetContextKeyString(ctx, constant.ContextKeyEffortCap)
	if cap == "" {
		return
	}
	other["effort_cap"] = cap
	other["effort_cap_source"] = common.GetContextKeyString(ctx, constant.ContextKeyEffortCapSource)
	if applied := common.GetContextKeyString(ctx, constant.ContextKeyEffortApplied); applied != "" {
		other["effort_clamped"] = true
		other["effort_requested"] = common.GetContextKeyString(ctx, constant.ContextKeyEffortRequested)
		other["effort_applied"] = applied
	}
}

func GenerateTextOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, cacheTokens int, perCallPriceUSD float64) map[string]interface{} {
	other := make(map[string]interface{})
	other["cache_tokens"] = cacheTokens
	if providerFamily := UsageProviderFamilyForRelay(relayInfo); providerFamily != usagepkg.ProviderFamilyUnknown {
		other["provider_family"] = string(providerFamily)
	}
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		other["provider_api_type"] = relayInfo.ApiType
	}
	other["per_call_price_usd"] = perCallPriceUSD
	// frt is the first frame of a stream (often a metadata frame), ttft_ms
	// the first event carrying output. A request that never streamed has
	// neither, rather than a negative sentinel.
	if relayInfo.IsStream && relayInfo.HasSendResponse() {
		other["frt"] = float64(relayInfo.FirstResponseTime.UnixMilli() - relayInfo.StartTime.UnixMilli())
		if ttft := relayInfo.OutputTTFT(relayInfo.StartTime); ttft > 0 {
			other["ttft_ms"] = ttft.Milliseconds()
		}
		if relayInfo.MaxStallMs > 0 {
			other["max_stall_ms"] = relayInfo.MaxStallMs
		}
	}
	if relayInfo.FinishReason != "" {
		other["finish_reason"] = relayInfo.FinishReason
	}
	if relayInfo.ReasoningEffort != "" {
		other["reasoning_effort"] = relayInfo.ReasoningEffort
	}
	appendEffortCapInfo(ctx, other)
	if ctx != nil {
		if requested := common.GetContextKeyString(ctx, constant.ContextKeyLaneRequested); requested != "" {
			other["lane_requested"] = requested
		}
	}
	if relayInfo.IsModelMapped {
		other["is_model_mapped"] = true
	}
	// The model the upstream was actually asked for, mapped or not.
	if relayInfo.ChannelMeta != nil && relayInfo.UpstreamModelName != "" {
		other["upstream_model_name"] = relayInfo.UpstreamModelName
	}
	if relayInfo.ProtocolPlan != nil {
		plan := relayInfo.ProtocolPlan
		other["protocol_client"] = string(plan.ClientProtocol.Family)
		other["protocol_upstream"] = string(plan.UpstreamProtocol.Family)
		other["protocol_dialect"] = plan.UpstreamProtocol.Dialect
		other["protocol_native"] = plan.Native
		other["protocol_capability_revision"] = plan.CapabilityRev
		other["protocol_features"] = plan.Features.Names()
		if len(plan.Warnings) > 0 {
			warnings := make([]string, 0, len(plan.Warnings))
			for _, warning := range plan.Warnings {
				warnings = append(warnings, warning.Message)
			}
			other["protocol_warnings"] = warnings
		}
	}
	if admissionTokens, ok := common.GetContextKeyType[int](ctx, constant.ContextKeyProtocolAdmissionTokens); ok {
		other["protocol_admission_prompt_tokens"] = admissionTokens
		other["protocol_token_comparison"] = "admission_vs_local_v1"
	}
	if irTokens, ok := common.GetContextKeyType[int](ctx, constant.ContextKeyProtocolLocalTokens); ok {
		other["protocol_local_prompt_tokens"] = irTokens
		other["protocol_admission_token_delta"] = common.GetContextKeyInt(ctx, constant.ContextKeyProtocolTokenDelta)
	}
	if skipped := common.GetContextKeyString(ctx, constant.ContextKeyProtocolTokenSkip); skipped != "" {
		other["protocol_prompt_token_shadow_skip"] = skipped
	}

	adminInfo := make(map[string]interface{})
	useChannels := ctx.GetStringSlice("use_channel")
	adminInfo["use_channel"] = useChannels
	// attempts counts every try, channels how many distinct channels they
	// went to: three attempts on one channel are retries, not a fallback.
	if len(useChannels) > 1 {
		other["attempts"] = len(useChannels)
		other["channels_tried"] = DistinctChannelCount(useChannels)
	}
	isMultiKey := common.GetContextKeyBool(ctx, constant.ContextKeyChannelIsMultiKey)
	if isMultiKey {
		adminInfo["is_multi_key"] = true
		adminInfo["multi_key_index"] = common.GetContextKeyInt(ctx, constant.ContextKeyChannelMultiKeyIndex)
	}
	other["admin_info"] = adminInfo
	appendRequestPath(ctx, relayInfo, other)
	return other
}

func GenerateWssOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage, perCallPriceUSD float64) map[string]interface{} {
	info := GenerateTextOtherInfo(ctx, relayInfo, 0, perCallPriceUSD)
	info["ws"] = true
	if usage == nil {
		return info
	}
	info["cache_tokens"] = usage.InputTokenDetails.CachedTokens
	info["audio_input"] = usage.InputTokenDetails.AudioTokens
	info["audio_output"] = usage.OutputTokenDetails.AudioTokens
	info["text_input"] = usage.InputTokenDetails.TextTokens
	info["text_output"] = usage.OutputTokenDetails.TextTokens
	return info
}

func GenerateAudioOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, perCallPriceUSD float64) map[string]interface{} {
	info := GenerateTextOtherInfo(ctx, relayInfo, 0, perCallPriceUSD)
	info["audio"] = true
	info["audio_input"] = usage.PromptTokensDetails.AudioTokens
	info["audio_output"] = usage.CompletionTokenDetails.AudioTokens
	info["text_input"] = usage.PromptTokensDetails.TextTokens
	info["text_output"] = usage.CompletionTokenDetails.TextTokens
	return info
}

func UsageProviderFamilyForRelay(relayInfo *relaycommon.RelayInfo) usagepkg.ProviderFamily {
	if relayInfo == nil {
		return usagepkg.ProviderFamilyOpenAICompatible
	}
	if relayInfo.ChannelMeta != nil && relayInfo.RelayFormat == types.RelayFormatDecisions {
		switch relayInfo.ChannelSetting.DecisionDialect {
		case "typesafe":
			return usagepkg.ProviderFamilyTypeSafe
		case "openrouter":
			return usagepkg.ProviderFamilyOpenRouter
		case "cloudflare_gateway", "cloudflare_workers_ai":
			return usagepkg.ProviderFamilyCloudflare
		}
	}
	hint := usagepkg.ProviderHint{
		ModelName:         relayInfo.OriginModelName,
		UpstreamModelName: relayInfo.UpstreamModelName,
	}
	if relayInfo.ChannelMeta != nil {
		hint.APIType = relayInfo.ApiType
		hint.ChannelType = relayInfo.ChannelType
		hint.BaseURL = relayInfo.ChannelBaseUrl
	}
	return usagepkg.ProviderFamilyFromHint(hint)
}

func GenerateClaudeOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo,
	cacheTokens int, cacheCreationTokens int, cacheCreation5mTokens int, cacheCreation1hTokens int,
	perCallPriceUSD float64) map[string]interface{} {
	info := GenerateTextOtherInfo(ctx, relayInfo, cacheTokens, perCallPriceUSD)
	info["claude"] = true
	info["cache_creation_tokens"] = cacheCreationTokens
	info["cache_write_tokens"] = cacheCreationTokens
	if cacheCreation5mTokens > 0 {
		info["cache_creation_5m_tokens"] = cacheCreation5mTokens
		info["cache_write_5m_tokens"] = cacheCreation5mTokens
	}
	if cacheCreation1hTokens > 0 {
		info["cache_creation_1h_tokens"] = cacheCreation1hTokens
		info["cache_write_1h_tokens"] = cacheCreation1hTokens
	}
	return info
}

// DistinctChannelCount is how many different channels a request's attempts
// went to.
func DistinctChannelCount(useChannels []string) int {
	seen := make(map[string]struct{}, len(useChannels))
	for _, id := range useChannels {
		seen[id] = struct{}{}
	}
	return len(seen)
}

// ReportedReasoningTokens is the reasoning token count the upstream
// reported, from whichever usage shape carried it.
func ReportedReasoningTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	reasoning := usage.CompletionTokenDetails.ReasoningTokens
	if usage.OutputTokensDetails != nil && usage.OutputTokensDetails.ReasoningTokens > reasoning {
		reasoning = usage.OutputTokensDetails.ReasoningTokens
	}
	return reasoning
}
