package relay

import (
	"github.com/tidwall/gjson"
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

// After a raw override the plan still holds: the client's transport, the
// Messages thinking constraints, and a stateless Responses body.
func TestAfterRawOverrideKeepsThePlan(t *testing.T) {
	plan := func(upstream textgen.ProtocolFamily) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{IsStream: true, ProtocolPlan: &textgen.RelayPlan{
			ClientProtocol:   textgen.Protocol{Family: textgen.ProtocolOpenAIChat},
			UpstreamProtocol: textgen.Protocol{Family: upstream},
		}}
	}
	body, err := afterRawOverride(plan(textgen.ProtocolAnthropic), []byte(`{"stream":false,"temperature":0.2,"top_p":0.5,"max_tokens":100,"thinking":{"type":"enabled","budget_tokens":2048}}`))
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(body, "stream").Bool() != true || gjson.GetBytes(body, "temperature").Float() != 1 || gjson.GetBytes(body, "top_p").Exists() || gjson.GetBytes(body, "max_tokens").Int() < 2048 {
		t.Fatalf("messages body: %s", body)
	}
	if _, err := afterRawOverride(plan(textgen.ProtocolOpenAIResponses), []byte(`{"stream":true,"store":true}`)); err == nil {
		t.Fatal("store=true from an override must refuse a stateless bridge")
	}
}
