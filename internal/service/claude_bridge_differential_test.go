package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	anthropic_messages "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/anthropic_messages"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
)

// The protocol hub's Claude-to-Chat path (anthropic_messages.Decode, then
// openai_chat.Encode) is held to golden files under testdata/claude_bridge:
// the legacy converter's output on the requests it got right, saved before it
// was deleted (AGENTS.md, Protocol Layer 7). Where the two differ on purpose
// the hub's own test says what it does: tool_choice and thinking carried,
// images in tool results kept as images, is_error said, and an assistant's
// text beside its tool call kept (TestHubKeepsTextBesideAToolCall).
var claudeDifferentialCases = map[string]string{
	"text":         `{"model":"m","max_tokens":200,"temperature":0.3,"top_p":0.9,"messages":[{"role":"user","content":"Say hi."}]}`,
	"system":       `{"model":"m","system":"Be terse.","messages":[{"role":"user","content":"Hi"},{"role":"assistant","content":"Hello."},{"role":"user","content":"Again?"}],"stop_sequences":["END","STOP"]}`,
	"system_parts": `{"model":"m","system":[{"type":"text","text":"Rules."}],"messages":[{"role":"user","content":[{"type":"text","text":"one"},{"type":"text","text":"two"}]}]}`,
	"tool_round":   `{"model":"m","tools":[{"name":"get_weather","description":"Weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"messages":[{"role":"user","content":"Weather in Paris?"},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"Sunny"}]}]}`,
	"image":        `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"What is this?"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}]}`,
}

func TestClaudeToChatMatchesTheGoldenFiles(t *testing.T) {
	for name, body := range claudeDifferentialCases {
		t.Run(name, func(t *testing.T) {
			request, _, err := anthropic_messages.Decode([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Model.Upstream = "m"
			hub, err := openai_chat.Encode(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join("testdata", "claude_bridge", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			if got := normaliseChat(t, hub); !reflect.DeepEqual(got, want) {
				g, _ := json.MarshalIndent(got, "", "  ")
				t.Fatalf("hub differs from %s.json\n got: %s\nwant: %s", name, g, raw)
			}
		})
	}
}

// normaliseChat folds the shapes a Chat upstream reads alike: string content
// vs one text part, max_tokens vs max_completion_tokens, a tool message's
// name (Chat ignores it), adjacent text parts, an empty tools list, empty
// image_url fields.
func normaliseChat(t *testing.T, request *dto.GeneralOpenAIRequest) map[string]any {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if value, ok := out["max_completion_tokens"]; ok {
		out["max_tokens"] = value
		delete(out, "max_completion_tokens")
	}
	if tools, ok := out["tools"].([]any); ok && len(tools) == 0 {
		delete(out, "tools")
	}
	if stop, ok := out["stop"].(string); ok {
		out["stop"] = []any{stop}
	}
	messages, _ := out["messages"].([]any)
	for _, item := range messages {
		message := item.(map[string]any)
		delete(message, "name")
		if parts, ok := message["content"].([]any); ok {
			for _, part := range parts {
				// the legacy DTO wrote empty MimeType and detail
				if image, ok := part.(map[string]any)["image_url"].(map[string]any); ok {
					for key, value := range image {
						if value == "" {
							delete(image, key)
						}
					}
				}
			}
			allText := true
			text := ""
			for _, part := range parts {
				p := part.(map[string]any)
				if p["type"] != "text" {
					allText = false
					break
				}
				if text != "" {
					text += "\n"
				}
				text += p["text"].(string)
			}
			if allText {
				message["content"] = text
			}
		}
		if message["content"] == nil {
			delete(message, "content")
		}
	}
	return out
}

// The legacy converter dropped an assistant's text when the same turn called
// a tool; the hub keeps it.
func TestHubKeepsTextBesideAToolCall(t *testing.T) {
	request, _, err := anthropic_messages.Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"Weather?"},{"role":"assistant","content":[{"type":"text","text":"Checking."},{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"Sunny"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	chat, err := openai_chat.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	assistant := chat.Messages[1]
	if assistant.StringContent() != "Checking." || len(assistant.ParseToolCalls()) != 1 {
		t.Fatalf("assistant turn: %+v", assistant)
	}
}
