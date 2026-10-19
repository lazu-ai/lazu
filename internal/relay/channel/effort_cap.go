package channel

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

const effortBetaRequiredContext = "lazu_effort_beta_required"
const claudeEffortBeta = "effort-2025-11-24"

// Clamp after conversion, suffix expansion and overrides, but before request
// signing, content-length calculation, provider-attempt telemetry or streaming.
// Resolve the outgoing protocol independently of custom endpoint paths.
// Return the stripped wire model so callers can rebuild model-bearing URLs.
func capUpstreamEffort(c *gin.Context, info *relaycommon.RelayInfo, url string, reader io.Reader) (io.Reader, error) {
	cap := common.GetContextKeyString(c, constant.ContextKeyEffortCap)
	c.Set(effortBetaRequiredContext, false)
	if reader == nil {
		return reader, nil
	}
	// Retries must expose only the current attempt's final applied value.
	c.Set(string(constant.ContextKeyEffortRequested), "")
	c.Set(string(constant.ContextKeyEffortApplied), "")
	c.Header("X-Lazu-Effort-Applied", "")
	format := outgoingEffortFormat(info, url)
	if format == "" {
		return reader, nil
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(body) {
		return bytes.NewReader(body), nil
	}
	name := gjson.GetBytes(body, "model").String()
	if name == "" {
		name = info.UpstreamModelName
	}
	// Grok reasoning-only models have no representation below low. Never send
	// an invalid 'none' or silently exceed a member's hard cap.
	if cap != "" && model.SnapEffort(name, cap) == "none" && !model.CanDisableReasoning(name) {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("reasoning effort cap %q cannot be satisfied by model %q: choose another model or raise the cap", cap, name), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	next, requested, applied := model.ClampEffortBody(body, name, cap, format)
	if format == "claude" {
		requiresBeta := gjson.GetBytes(next, "output_config.effort").Exists()
		for _, message := range gjson.GetBytes(next, "messages").Array() {
			requiresBeta = requiresBeta || message.Get("output_config.effort").Exists()
		}
		c.Set(effortBetaRequiredContext, requiresBeta)
	}
	if applied != "" {
		common.SetContextKey(c, constant.ContextKeyEffortRequested, requested)
		common.SetContextKey(c, constant.ContextKeyEffortApplied, applied)
		c.Header("X-Lazu-Effort-Applied", applied)
	}
	return bytes.NewReader(next), nil
}

func outgoingEffortFormat(info *relaycommon.RelayInfo, url string) string {
	if info.ProtocolPlan != nil {
		upstream := info.ProtocolPlan.UpstreamProtocol
		switch upstream.Dialect {
		case "anthropic":
			return "claude"
		case "gemini":
			return "gemini"
		}
		switch upstream.Family {
		case textgen.ProtocolOpenAIChat:
			return "chat"
		case textgen.ProtocolOpenAIResponses:
			return "responses"
		case textgen.ProtocolAnthropic:
			return "claude"
		case textgen.ProtocolGemini:
			return "gemini"
		}
	}
	// Legacy adapters can bridge formats without a plan. Their wire URL takes
	// precedence over the inbound RelayFormat; exact custom URLs fall back to it.
	switch {
	case strings.Contains(url, "/chat/completions"):
		return "chat"
	case strings.Contains(url, "/responses"):
		return "responses"
	case strings.Contains(url, "/messages"):
		return "claude"
	case strings.Contains(url, ":generateContent"), strings.Contains(url, ":streamGenerateContent"):
		return "gemini"
	}
	if info.ChannelMeta != nil {
		switch info.RelayFormat {
		case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini:
			switch info.ApiType {
			case constant.APITypeAnthropic:
				return "claude"
			case constant.APITypeGemini:
				return "gemini"
			case constant.APITypeOpenAI, constant.APITypeXai:
				if info.RelayFormat == types.RelayFormatOpenAIResponses {
					return "responses"
				}
				return "chat"
			}
		}
	}
	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		return "chat"
	case types.RelayFormatOpenAIResponses:
		return "responses"
	case types.RelayFormatClaude:
		return "claude"
	case types.RelayFormatGemini:
		return "gemini"
	}
	return ""
}
