package relay

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// A Claude model on the Text Completions API is never a Messages bridge:
// its adaptor posts a prompt to /v1/complete.
func TestPlanBridgesExcludesLegacyCompletionModels(t *testing.T) {
	info := func(model string) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			ProtocolPlan: &textgen.RelayPlan{
				ClientProtocol:   textgen.Protocol{Family: textgen.ProtocolOpenAIChat},
				UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolAnthropic},
			},
			ProtocolRequest: &textgen.Request{},
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: model},
		}
	}
	for _, model := range []string{"claude-2.1", "claude-instant-1.2"} {
		if planBridges(info(model)) {
			t.Fatalf("%s must not be bridged to Messages", model)
		}
	}
	if !planBridges(info("claude-haiku-5-5")) {
		t.Fatal("a Messages model is bridged")
	}
}
