package openai_responses

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
)

func TestGatewayToolReasoningRoundTrip(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			var output []json.RawMessage
			var raw []byte
			if stream {
				bridge := NewChatStreamBridge("roundtrip", "m")
				_, err := bridge.Consume(&dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant", Content: stringPointer("checking"), ReasoningContent: stringPointer("use lookup"), ToolCalls: []dto.ToolCallResponse{toolDelta(0, "call_1", "lookup", `{}`)}}, FinishReason: stringPointer("tool_calls")}}})
				if err != nil {
					t.Fatal(err)
				}
				events, err := bridge.Finalize(nil)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ = json.Marshal(EmitEvent(events[len(events)-1])["response"].(map[string]any)["output"])
			} else {
				message := dto.Message{Role: "assistant", ReasoningContent: "use lookup"}
				message.SetStringContent("checking")
				message.SetToolCalls([]dto.ToolCallRequest{{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{}`}}})
				response := ChatResponseToResponses(&dto.OpenAITextResponse{Id: "roundtrip", Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "tool_calls"}}}, "m")
				raw, _ = json.Marshal(response.Output)
			}
			if err := json.Unmarshal(raw, &output); err != nil {
				t.Fatal(err)
			}
			input := []json.RawMessage{json.RawMessage(`{"role":"user","content":"look it up"}`)}
			input = append(input, output...)
			input = append(input, json.RawMessage(`{"type":"function_call_output","call_id":"call_1","output":"42"}`))
			body, _ := json.Marshal(map[string]any{"model": "m", "store": false, "input": input})
			request, _, err := Decode(body)
			if err != nil {
				t.Fatal(err)
			}
			var features textgen.FeatureSet
			features.Add(textgen.FeatureEndpointChat)
			client := textgen.Protocol{Family: textgen.ProtocolOpenAIResponses, Dialect: "openai"}
			plan, err := textgen.PlanAttempt(client, textgen.CapabilitySnapshot{Dialect: "openai", Features: features}, request, 1, "m")
			if err != nil {
				t.Fatalf("gateway output cannot continue: %v; unknown=%+v; output=%s", err, request.Raw.UnknownFields, raw)
			}
			if plan.Native {
				t.Fatal("expected a Chat bridge")
			}
			wire, err := openai_chat.Encode(request)
			if err != nil {
				t.Fatal(err)
			}
			if len(wire.Messages) != 3 {
				t.Fatalf("history fragmented: %+v", wire.Messages)
			}
			assistant := wire.Messages[1]
			if assistant.ReasoningContent != "use lookup" || assistant.StringContent() != "checking" || len(assistant.ParseToolCalls()) != 1 || assistant.ParseToolCalls()[0].ID != "call_1" {
				t.Fatalf("assistant history changed: %+v", assistant)
			}
			if wire.Messages[2].ToolCallId != "call_1" || wire.Messages[2].StringContent() != "42" {
				t.Fatalf("tool result changed: %+v", wire.Messages[2])
			}
			if !strings.Contains(textgen.TokenCountMeta(request).CombineText, "use lookup") {
				t.Fatal("reasoning history missing from token accounting")
			}
			// A native route must retain the original item, even when bridgeable.
			native, err := Encode(request)
			if err != nil {
				t.Fatal(err)
			}
			var encoded []json.RawMessage
			if err := json.Unmarshal(native.Input, &encoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(wireObject(t, output[0]), wireObject(t, encoded[1])) {
				t.Fatal("native reasoning state changed")
			}
		})
	}
}

func TestGatewayReasoningDoesNotAcceptProviderState(t *testing.T) {
	for name, item := range map[string]string{
		"native summary":    `{"type":"reasoning","id":"rs_native","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"x"}]}`,
		"encrypted":         `{"type":"reasoning","id":"rs_lazu_1","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"x"}],"encrypted_content":"opaque"}`,
		"summary content":   `{"type":"reasoning","id":"rs_lazu_1","status":"completed","summary":[{"type":"summary_text","text":"x"}],"content":[{"type":"reasoning_text","text":"x"}]}`,
		"incomplete":        `{"type":"reasoning","id":"rs_lazu_1","status":"incomplete","summary":[],"content":[{"type":"reasoning_text","text":"x"}]}`,
		"content extension": `{"type":"reasoning","id":"rs_lazu_1","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"x","signature":"opaque"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			request, _, err := Decode([]byte(`{"model":"m","store":false,"input":[` + item + `]}`))
			if err != nil {
				t.Fatal(err)
			}
			features, _ := textgen.ExtractFeatures(request)
			if !features.Has(textgen.FeatureOpaqueReasoningItem) {
				t.Fatal("provider state became bridgeable")
			}
			wire, err := Encode(request)
			if err != nil {
				t.Fatal(err)
			}
			var output []json.RawMessage
			if err := json.Unmarshal(wire.Input, &output); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(wireObject(t, json.RawMessage(item)), wireObject(t, output[0])) {
				t.Fatal("native provider state was not preserved")
			}
		})
	}
}

func TestLegacyGatewayOutputCanContinue(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","store":false,"input":[
		{"type":"reasoning","id":"rs_old_0","status":"completed","content":[{"type":"reasoning_text","text":"think"}]},
		{"type":"message","id":"msg_old_0","status":"completed","role":"assistant","content":[{"type":"output_text","text":"checking","annotations":null}],"quality":"","size":""},
		{"type":"function_call","id":"fc_old_0_0","status":"completed","role":"","content":null,"quality":"","size":"","call_id":"call_1","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_1","output":"42"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	features, _ := textgen.ExtractFeatures(request)
	if features.Has(textgen.FeatureOpaqueReasoningItem) || features.Has(textgen.FeatureUnknownField) {
		t.Fatalf("legacy gateway history rejected: %+v", request.Raw.UnknownFields)
	}
	wire, err := openai_chat.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire.Messages) != 2 || wire.Messages[0].ReasoningContent != "think" || wire.Messages[0].StringContent() != "checking" || len(wire.Messages[0].ParseToolCalls()) != 1 {
		t.Fatalf("legacy history lost: %+v", wire.Messages)
	}
}
