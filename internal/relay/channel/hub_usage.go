package channel

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
)

// SettleHubUsage is the usage a protocol-hub attempt is billed for: the
// provider's where it reported one, estimated from the collected text where
// it did not. Both hubs (internal/relay/channel/claude/hub.go,
// internal/relay/channel/openai/claude_client_hub.go) settle through it.
func SettleHubUsage(c *gin.Context, info *relaycommon.RelayInfo, result textgen.Result) *dto.Usage {
	usage := result.Usage
	if usage == nil {
		usage = &dto.Usage{}
	}
	if usage.CompletionTokens == 0 || !result.StopSeen {
		var text strings.Builder
		for _, part := range result.Parts {
			text.WriteString(part.Text)
		}
		fallback := service.ResponseText2Usage(text.String(), info.UpstreamModelName, usage.PromptTokens)
		if usage.PromptTokens == 0 && usage.InputTokenSource != dto.UsageSourceProvider {
			if text.Len() > 0 || service.ValidUsage(usage) {
				service.ApplyLocalPromptUsage(c, info, usage)
			}
		}
		if usage.CompletionTokens == 0 {
			usage.CompletionTokens = fallback.CompletionTokens
			usage.OutputTokenSource = dto.UsageSourceLocalEstimate
		}
	}
	if usage.TotalTokens < usage.PromptTokens+usage.CompletionTokens {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}
