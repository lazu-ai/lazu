package anthropic_messages

import (
	"errors"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
)

func capability(dialect string, features ...textgen.FeatureID) textgen.CapabilitySnapshot {
	snapshot := textgen.CapabilitySnapshot{Dialect: dialect}
	for _, feature := range features {
		snapshot.Features.Add(feature)
	}
	return snapshot
}

var claudeClient = textgen.Protocol{Family: textgen.ProtocolAnthropic, Dialect: "anthropic"}

// Claude Code's own request is served by every kind of channel: native on
// Anthropic, bridged to Chat (DeepSeek, any OpenAI-compatible) or to
// Responses when that is all a channel has.
func TestClaudeCodeRequestPlansOnEveryChannelKind(t *testing.T) {
	request, _, err := Decode(fixtures.ClientRequest(t, "claude_code/tool_image_turn.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		snapshot textgen.CapabilitySnapshot
		native   bool
		upstream textgen.ProtocolFamily
	}{
		{"anthropic", capability("anthropic", textgen.FeatureEndpointMessages), true, textgen.ProtocolAnthropic},
		{"deepseek chat and responses", capability("deepseek", textgen.FeatureEndpointChat, textgen.FeatureEndpointResponses), false, textgen.ProtocolOpenAIChat},
		{"chat compatible", capability("openai_chat_compatible", textgen.FeatureEndpointChat), false, textgen.ProtocolOpenAIChat},
		{"responses only", capability("openai", textgen.FeatureEndpointResponses), false, textgen.ProtocolOpenAIResponses},
	} {
		plan, err := textgen.PlanAttempt(claudeClient, tc.snapshot, request, 1, "m")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if plan.Native != tc.native || plan.UpstreamProtocol.Family != tc.upstream {
			t.Fatalf("%s: native=%t upstream=%s", tc.name, plan.Native, plan.UpstreamProtocol.Family)
		}
	}
}

// What only Anthropic can do is refused by name on a bridge and passes
// natively.
func TestClaudeClientBridgeRefusesAnthropicOnlyFeatures(t *testing.T) {
	chat := capability("openai_chat_compatible", textgen.FeatureEndpointChat)
	messages := capability("anthropic", textgen.FeatureEndpointMessages)
	for name, body := range map[string]string{
		"server tool":        `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"bash_20250124","name":"bash"}]}`,
		"mcp servers":        `{"model":"m","messages":[{"role":"user","content":"hi"}],"mcp_servers":[{"type":"url","url":"https://x"}]}`,
		"context edit":       `{"model":"m","messages":[{"role":"user","content":"hi"}],"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]}}`,
		"speed":              `{"model":"m","messages":[{"role":"user","content":"hi"}],"speed":"fast"}`,
		"web search history": `{"model":"m","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"x"}}]}]}`,
	} {
		request, _, err := Decode([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = textgen.PlanAttempt(claudeClient, chat, request, 1, "m")
		var unsupported *textgen.UnsupportedFeatureError
		if !errors.As(err, &unsupported) || unsupported.Path == "" {
			t.Fatalf("%s: a bridge must refuse by path, got %v", name, err)
		}
		if _, err := textgen.PlanAttempt(claudeClient, messages, request, 1, "m"); err != nil {
			t.Fatalf("%s: native must pass: %v", name, err)
		}
	}
}
