package openai_responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestSimpleTextInputCompatibility(t *testing.T) {
	cases := []struct {
		name, input string
		common      bool
	}{
		{"text", `[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]`, true},
		{"default_role", `[{"content":[{"type":"input_text","text":""}]}]`, true},
		{"empty_content", `[{"type":"message","role":"assistant","content":[]}]`, true},
		{"empty_strings", `[{"type":"","role":"","content":[{"type":"input_text","text":""}]}]`, true},
		{"all_roles", `[{"role":"system","content":[{"type":"input_text","text":"sys"}]},{"role":"developer","content":[]},{"role":"user","content":[{"type":"input_text","text":"ask"}]},{"role":"assistant","content":[{"type":"input_text","text":"say"}]},{"role":"tool","content":[{"type":"input_text","text":"result"}]}]`, true},
		{"unicode_keys_values", `[{"\u0074ype":"message","\u0072ole":"user","\u0063ontent":[{"\u0074ype":"input_text","\u0074ext":"\u4e16\u754c\n\"a\\b\"\/\ud83d\ude03"}]}]`, true},
		{"multiple_parts", `[{"role":"user","content":[{"type":"input_text","text":"first"},{"text":"second","type":"input_text"}]}]`, true},
		{"duplicate_role_null", `[{"role":"assistant","role":null,"content":[{"type":"input_text","text":"x"}]}]`, false},
		{"duplicate_role_string", `[{"role":"user","role":"assistant","content":[]}]`, false},
		{"escaped_duplicate_role", `[{"role":"assistant","\u0072ole":null,"content":[]}]`, false},
		{"duplicate_content_null", `[{"content":[{"type":"input_text","text":"x"}],"content":null}]`, false},
		{"duplicate_text_null", `[{"role":"user","content":[{"type":"input_text","text":"first","text":null}]}]`, false},
		{"escaped_duplicate_text", `[{"content":[{"type":"input_text","text":"first","\u0074ext":"last"}]}]`, false},
		{"duplicate_type_null", `[{"type":"function_call","type":null,"content":[]}]`, false},
		{"null_item_type", `[{"type":null,"content":[]}]`, false},
		{"null_role", `[{"role":null,"content":[]}]`, false},
		{"number_role", `[{"role":4,"content":[]}]`, false},
		{"null_text", `[{"content":[{"type":"input_text","text":null}]}]`, false},
		{"missing_text", `[{"content":[{"type":"input_text"}]}]`, false},
		{"number_text", `[{"content":[{"type":"input_text","text":42}]}]`, false},
		{"null_part_type", `[{"content":[{"type":null,"text":"x"}]}]`, false},
		{"missing_part_type", `[{"content":[{"text":"x"}]}]`, false},
		{"missing_content", `[{"role":"user"}]`, false},
		{"null_content", `[{"role":"user","content":null}]`, false},
		{"string_content", `[{"role":"user","content":"x"}]`, false},
		{"casefolded_role", `[{"ROLE":"assistant","content":[]}]`, false},
		{"casefolded_text", `[{"content":[{"type":"input_text","TEXT":"upper"}]}]`, false},
		{"unknown_item_field", `[{"content":[],"provider_metadata":{"future":true}}]`, false},
		{"null_unknown_item_field", `[{"content":[],"provider_metadata":null}]`, false},
		{"unknown_part_field", `[{"content":[{"type":"input_text","text":"x","cache_control":{"type":"ephemeral"}}]}]`, false},
		{"message_status_id", `[{"type":"message","role":"assistant","status":"incomplete","id":"msg_1","content":[{"type":"output_text","text":"x","annotations":[]}]}]`, false},
		{"unknown_role", `[{"role":"future","content":[]}]`, false},
		{"unknown_item", `[{"type":"future","payload":{"v":1}}]`, false},
		{"image", `[{"content":[{"type":"input_image","image_url":"https://example.invalid/x"}]}]`, false},
		{"mixed_media", `[{"content":[{"type":"input_text","text":"x"},{"type":"input_file","file_id":"file_1"},{"type":"input_audio","data":"YWJj","format":"mp3"}]}]`, false},
		{"function_call", `[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}]`, false},
		{"function_output", `[{"type":"function_call_output","call_id":"call_1","output":{"value":1}}]`, false},
		{"reference", `[{"type":"item_reference","id":"item_1"}]`, false},
		{"reasoning", `[{"type":"reasoning","summary":[{"type":"summary_text","text":"think"}]}]`, false},
		{"root_null", `null`, false},
		{"root_string", `"hello"`, false},
		{"root_number", `42`, false},
		{"root_object", `{}`, false},
		{"empty_input", `[]`, false},
		{"non_object_item", `["x"]`, false},
		{"null_item", `[null]`, false},
		{"malformed", `[{"content":[]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, common := decodeSimpleTextInput(json.RawMessage(tc.input))
			if common != tc.common {
				t.Fatalf("common=%v, want %v", common, tc.common)
			}
			// Compare complete IR and DTO (including retained metadata, tools,
			// raw fields and error strings) to the decoder before this optimization.
			body := []byte(`{"model":"gpt-mock","stream":true,"instructions":"Keep details","metadata":{"trace":"synthetic","n":9007199254740993},"future_top":{"v":9007199254740993},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false}],"input":` + tc.input + `}`)
			original := bytes.Clone(body)
			want, wantWire, wantErr := decodeBeforeSimpleText(body)
			got, gotWire, gotErr := Decode(body)
			if errorText(gotErr) != errorText(wantErr) {
				t.Fatalf("error=%q, want %q", errorText(gotErr), errorText(wantErr))
			}
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotWire, wantWire) {
				t.Fatal("complete IR or retained wire differs from the previous decoder")
			}
			if !bytes.Equal(body, original) {
				t.Fatal("decode mutated the original request bytes")
			}
		})
	}
}

func TestSimpleTextInputCommonValues(t *testing.T) {
	input := json.RawMessage(`[{"\u0072ole":"user","\u0063ontent":[{"\u0074ype":"input_text","\u0074ext":"\u4e16\u754c\n\"a\\b\"\/\ud83d\ude03"}]},{"type":"message","role":"assistant","content":[]}]`)
	items, ok := decodeSimpleTextInput(input)
	if !ok || len(items) != 2 {
		t.Fatal("common messages not decoded")
	}
	if items[0].Role != "user" || items[0].Kind != textgen.InputMessage || len(items[0].Content) != 1 || items[0].Content[0].Kind != textgen.ContentText || items[0].Content[0].Text != "世界\n\"a\\b\"/😃" {
		t.Fatalf("escaped common text changed: %+v", items[0])
	}
	if items[1].Role != "assistant" || items[1].Content == nil || len(items[1].Content) != 0 {
		t.Fatal("empty content must preserve the original non-nil empty array")
	}
}

func TestSimpleTextInputFallbackKeepsNullAndDuplicateSemantics(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"role":"assistant","role":null,"content":[{"type":"input_text","text":"first","text":null}]},{"content":null}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Conversation) != 2 || request.Conversation[0].Role != "user" || request.Conversation[0].Content[0].Text != "" || request.Conversation[1].Content[0].Text != "" {
		t.Fatalf("last-null fallback semantics changed: %+v", request.Conversation)
	}
}

func TestSimpleTextInputFastPathRetainsEnvelopeExtensions(t *testing.T) {
	request, _, err := Decode([]byte(`{"model":"m","input":[{"content":[{"type":"input_text","text":"hello"}]}],"metadata":{"trace":"synthetic"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false,"future_tool":true}],"future_top":{"n":9007199254740993},"store":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Conversation) != 1 || request.Conversation[0].Content[0].Text != "hello" {
		t.Fatal("common text missing")
	}
	if string(request.Metadata.Metadata) != `{"trace":"synthetic"}` || len(request.Tools) != 1 || !request.Tools[0].Strict.Set || request.Tools[0].Strict.Value {
		t.Fatal("metadata/tool strict=false changed")
	}
	if _, ok := request.Raw.UnknownFields["tools[0].future_tool"]; !ok {
		t.Fatal("unknown tool field lost")
	}
	if string(request.Raw.UnknownFields["future_top"]) != `{"n":9007199254740993}` {
		t.Fatal("unknown top-level field or integer precision lost")
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// These reference functions preserve the input/Decode algorithm before the
// common-text optimization. Keeping the old raw-message decoder as a test-only
// oracle proves compatibility for shapes that encoding/json handles differently
// in map fields versus typed fields (notably duplicate nulls and key casing).

func decodeBeforeSimpleText(body []byte) (*textgen.Request, *dto.OpenAIResponsesRequest, error) {
	var wire dto.OpenAIResponsesRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(wire.Model) == "" {
		return nil, nil, fmt.Errorf("model is required")
	}
	if len(wire.Input) == 0 {
		return nil, nil, fmt.Errorf("input is required")
	}
	raw, err := textgen.RetainTopLevelFields(body)
	if err != nil {
		return nil, nil, err
	}
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: wire.Model}, Raw: textgen.RawRetention{Protocol: textgen.ProtocolOpenAIResponses, TopLevel: raw, UnknownFields: unknownFields(raw)}}
	request.Lifecycle.Stream = wire.Stream
	if wire.StreamOptions != nil {
		request.Lifecycle.StreamIncludeUsage = textgen.Some(wire.StreamOptions.IncludeUsage)
	}
	markUnmodeledStreamOptions(raw, request)
	decodeOptions(raw, &wire, request)
	if err := decodeInstructions(wire.Instructions, request); err != nil {
		return nil, nil, err
	}
	if err := decodeInputBeforeSimpleText(wire.Input, request); err != nil {
		return nil, nil, err
	}
	if err := decodeTools(&wire, request); err != nil {
		return nil, nil, err
	}
	if err := decodeOutput(wire.Text, request); err != nil {
		return nil, nil, err
	}
	if wire.Reasoning != nil {
		request.Reasoning.Effort = wire.Reasoning.Effort
		request.Reasoning.Summary = wire.Reasoning.Summary
	}
	request.Metadata.Metadata = append(json.RawMessage(nil), wire.Metadata...)
	request.Metadata.User = wire.User
	request.Metadata.SafetyIdentifier = wire.SafetyIdentifier
	request.Metadata.ServiceTier = wire.ServiceTier
	request.Metadata.PromptCacheKey = append(json.RawMessage(nil), wire.PromptCacheKey...)
	request.Lifecycle.PreviousResponseID = wire.PreviousResponseID
	request.Lifecycle.Conversation = append(json.RawMessage(nil), wire.Conversation...)
	request.Lifecycle.PromptTemplate = append(json.RawMessage(nil), wire.Prompt...)
	if len(wire.Include) > 0 {
		_ = json.Unmarshal(wire.Include, &request.Lifecycle.Include)
	}
	return request, &wire, nil
}

func decodeInputBeforeSimpleText(raw json.RawMessage, request *textgen.Request) error {
	var text string
	if !startsJSONArray(raw) && json.Unmarshal(raw, &text) == nil {
		request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: text}}})
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("input must be a string or item array")
	}
	for index, itemRaw := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			return fmt.Errorf("input[%d] must be an object", index)
		}
		var itemType string
		_ = json.Unmarshal(item["type"], &itemType)
		markUnknownInputItemFields(request, index, itemType, item)
		switch itemType {
		case "function_call":
			var call struct {
				CallID    string `json:"call_id"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Status    string `json:"status"`
			}
			if err := json.Unmarshal(itemRaw, &call); err != nil {
				return err
			}
			if call.CallID == "" {
				call.CallID = call.ID
			}
			if call.Status != "" && !strings.EqualFold(call.Status, "completed") {
				markUnknown(request, fmt.Sprintf("input[%d].status", index), call.Status)
			}
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputFunctionCall, Role: "assistant", CallID: call.CallID, FunctionName: call.Name, Arguments: call.Arguments})
		case "function_call_output":
			var result struct {
				CallID string `json:"call_id"`
				Output any    `json:"output"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(itemRaw, &result); err != nil {
				return err
			}
			if result.Status != "" && !strings.EqualFold(result.Status, "completed") {
				markUnknown(request, fmt.Sprintf("input[%d].status", index), result.Status)
			}
			output, stringOutput := result.Output.(string)
			if !stringOutput {
				// Chat tool messages cannot preserve Responses structured content
				// parts. Retain native decoding, but reject a lossy bridge.
				markUnknown(request, fmt.Sprintf("input[%d].output", index), result.Output)
			}
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputFunctionResult, Role: "tool", CallID: result.CallID, Output: output})
		case "reasoning":
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputReasoning, PlainReasoning: decodeGatewayReasoning(item), Opaque: []textgen.OpaqueValue{{Dialect: "openai", Value: append(json.RawMessage(nil), itemRaw...)}}})
		case "item_reference":
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputReference, Opaque: []textgen.OpaqueValue{{Dialect: "openai", Value: append(json.RawMessage(nil), itemRaw...)}}})
		default:
			if itemType != "" && itemType != "message" {
				markUnknown(request, fmt.Sprintf("input[%d].type", index), itemRaw)
				request.Conversation = append(request.Conversation, opaqueInputItem(itemType, itemRaw))
				continue
			}
			var status string
			_ = json.Unmarshal(item["status"], &status)
			if status != "" && !strings.EqualFold(status, "completed") {
				markUnknown(request, fmt.Sprintf("input[%d].status", index), status)
			}
			var role string
			_ = json.Unmarshal(item["role"], &role)
			if role == "" {
				role = "user"
			}
			switch role {
			case "system", "developer", "user", "assistant", "tool":
			default:
				markUnknown(request, fmt.Sprintf("input[%d].role", index), role)
			}
			parts, err := decodeContent(item["content"], index, request)
			if err != nil {
				markUnknown(request, fmt.Sprintf("input[%d].content", index), itemRaw)
				request.Conversation = append(request.Conversation, opaqueInputItem("message", itemRaw))
				continue
			}
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputMessage, Role: role, Content: parts})
		}
	}
	if len(request.Conversation) == 0 {
		return fmt.Errorf("input is empty")
	}
	return nil
}
