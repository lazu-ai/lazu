package openai_responses

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

// Reconstruct the wire content by the indexes a client actually receives, then
// compare both item.done and the terminal envelope with that reconstruction.
func TestStreamContentReconstruction(t *testing.T) {
	for name, deltas := range map[string][]dto.ChatCompletionsStreamResponseChoiceDelta{
		"reasoning then text":      {{ReasoningContent: stringPointer("think")}, {Content: stringPointer("answer")}},
		"reasoning and empty text": {{Role: "assistant", ReasoningContent: stringPointer("think"), Content: stringPointer("")}, {Content: stringPointer("answer")}},
		"refusal then text":        {{Refusal: stringPointer("cannot")}, {Content: stringPointer(" explain")}},
		"text then refusal":        {{Content: stringPointer("answer")}, {Refusal: stringPointer("cannot")}},
		"reasoning and tool":       {{ReasoningContent: stringPointer("think"), ToolCalls: []dto.ToolCallResponse{toolDelta(0, "call_1", "lookup", `{}`)}}},
	} {
		t.Run(name, func(t *testing.T) {
			emitter := NewStreamEmitter("id", "m", nil)
			chunks := make([]*dto.ChatCompletionsStreamResponse, 0, len(deltas))
			for _, delta := range deltas {
				chunks = append(chunks, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{Delta: delta, FinishReason: stringPointer("stop")}}})
			}
			payloads := chatStream(t, emitter, nil, true, chunks...)
			parts := map[int][]map[string]interface{}{}
			for _, payload := range payloads {
				wire := wireObject(t, payload)
				if item, ok := wire["item"].(map[string]interface{}); ok && item["type"] == "reasoning" {
					assertReasoningSchema(t, item)
				}
				itemIndex := int(number(wire["output_index"]))
				switch wire["type"] {
				case "response.content_part.added":
					if int(number(wire["content_index"])) != len(parts[itemIndex]) {
						t.Fatalf("content index %v is not local to output %d: %+v", wire["content_index"], itemIndex, parts)
					}
					parts[itemIndex] = append(parts[itemIndex], wire["part"].(map[string]interface{}))
				case "response.output_text.delta", "response.reasoning_text.delta", "response.refusal.delta":
					part := parts[itemIndex][int(number(wire["content_index"]))]
					field := "text"
					if wire["type"] == "response.refusal.delta" {
						field = "refusal"
					}
					part[field] = part[field].(string) + wire["delta"].(string)
				case "response.output_item.done":
					item := wire["item"].(map[string]interface{})
					if item["type"] == "function_call" {
						continue
					}
					if !reflect.DeepEqual(wireObject(t, map[string]interface{}{"content": parts[itemIndex]})["content"], item["content"]) {
						t.Fatalf("item.done disagrees with deltas: %+v vs %+v", parts[itemIndex], item)
					}
				}
			}
			terminal := wireObject(t, payloads[len(payloads)-1])["response"].(map[string]interface{})["output"].([]interface{})
			for index, value := range terminal {
				item := value.(map[string]interface{})
				if item["type"] == "reasoning" {
					assertReasoningSchema(t, item)
				}
				if item["type"] == "function_call" {
					continue
				}
				if !reflect.DeepEqual(wireObject(t, map[string]interface{}{"content": parts[index]})["content"], item["content"]) {
					t.Fatalf("terminal disagrees with deltas: %+v vs %+v", parts[index], item)
				}
			}
			if name == "reasoning and empty text" && terminal[0].(map[string]interface{})["type"] != "reasoning" {
				t.Fatal("empty text displaced reasoning")
			}
		})
	}
}

func number(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

func wireObject(t *testing.T, value interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertReasoningSchema(t *testing.T, item map[string]interface{}) {
	t.Helper()
	if item["type"] != "reasoning" {
		t.Fatalf("wrong reasoning type: %+v", item)
	}
	if _, ok := item["id"].(string); !ok {
		t.Fatalf("missing id: %+v", item)
	}
	if _, ok := item["summary"].([]interface{}); !ok {
		t.Fatalf("summary must be an array, including when empty: %+v", item)
	}
	for _, field := range []string{"role", "quality", "size"} {
		if _, ok := item[field]; ok {
			t.Fatalf("reasoning contains message/image field %s", field)
		}
	}
}

func TestNonStreamReasoningSchema(t *testing.T) {
	message := dto.Message{Role: "assistant", ReasoningContent: "think"}
	message.SetStringContent("answer")
	response := ChatResponseToResponses(&dto.OpenAITextResponse{Id: "chatcmpl_test", Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "stop"}}}, "m")
	assertReasoningSchema(t, wireObject(t, response)["output"].([]interface{})[0].(map[string]interface{}))
}
