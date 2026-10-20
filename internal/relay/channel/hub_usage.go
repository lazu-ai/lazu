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
// toolCallFramingTokens is what a function call costs beyond its name and
// arguments when the provider did not count it.
const toolCallFramingTokens = 7

func SettleHubUsage(c *gin.Context, info *relaycommon.RelayInfo, result textgen.Result) *dto.Usage {
	usage := result.Usage
	if usage == nil {
		usage = &dto.Usage{}
	}
	if usage.CompletionTokens == 0 || !result.StopSeen {
		// a call is counted as its name and arguments plus the framing
		// every call carries, as the native paths count it
		var text strings.Builder
		calls := 0
		for _, part := range result.Parts {
			if part.Kind == textgen.PartTool {
				calls++
				text.WriteString(part.Name)
			}
			text.WriteString(part.Text)
		}
		fallback := service.ResponseText2Usage(text.String(), info.UpstreamModelName, usage.PromptTokens)
		fallback.CompletionTokens += calls * toolCallFramingTokens
		if usage.PromptTokens == 0 && usage.InputTokenSource != dto.UsageSourceProvider {
			// a reply that ended well consumed its prompt even when it
			// said nothing
			if text.Len() > 0 || service.ValidUsage(usage) || result.StopSeen {
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
