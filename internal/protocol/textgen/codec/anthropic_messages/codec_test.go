package anthropic_messages

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	"github.com/tidwall/gjson"
)

// The encoder is held to the legacy converter's output (testdata/golden,
// written from RequestOpenAI2ClaudeMessage before it was deleted), after
// the shapes the two are allowed to differ in are normalised away:
//
//   - string message content and text blocks are the same thing;
//   - consecutive same-role messages are one turn to the Messages API;
//   - the legacy "..." placeholder beside real blocks is noise;
//   - a null-valued schema key is no key.
func TestEncodeMatchesLegacyGoldens(t *testing.T) {
	for name, body := range GoldenChatRequests() {
		t.Run(name, func(t *testing.T) {
			semantic, _, err := openai_chat.Decode([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := Encode(semantic, EncodeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			encoded.Thinking, encoded.OutputConfig, encoded.MaxTokens = nil, nil, 0
			golden, err := os.ReadFile(filepath.Join("testdata", "golden", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			got := normalise(t, encoded)
			want := normalise(t, json.RawMessage(golden))
			if !reflect.DeepEqual(got, want) {
				gotRaw, _ := json.MarshalIndent(got, "", "  ")
				wantRaw, _ := json.MarshalIndent(want, "", "  ")
				t.Fatalf("encoder differs from the legacy converter\n got: %s\nwant: %s", gotRaw, wantRaw)
			}
		})
	}
}

func normalise(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	messages, _ := out["messages"].([]any)
	var merged []map[string]any
	for _, item := range messages {
		message, _ := item.(map[string]any)
		blocks := blocksOf(message["content"])
		if n := len(merged); n > 0 && merged[n-1]["role"] == message["role"] {
			merged[n-1]["content"] = append(merged[n-1]["content"].([]any), blocks...)
			continue
		}
		merged = append(merged, map[string]any{"role": message["role"], "content": blocks})
	}
	for _, message := range merged {
		blocks := message["content"].([]any)
		if len(blocks) > 1 {
			kept := blocks[:0:0]
			for _, block := range blocks {
				if b, _ := block.(map[string]any); b["type"] == "text" && b["text"] == placeholderText {
					continue
				}
				kept = append(kept, block)
			}
			message["content"] = kept
		}
		// the legacy converter joined adjacent text with a space; the
		// encoder keeps one block per part, which Anthropic reads alike.
		blocks = message["content"].([]any)
		joined := blocks[:0:0]
		for _, block := range blocks {
			b, _ := block.(map[string]any)
			if n := len(joined); n > 0 && b["type"] == "text" {
				if prev, _ := joined[n-1].(map[string]any); prev["type"] == "text" {
					prev["text"] = prev["text"].(string) + " " + b["text"].(string)
					continue
				}
			}
			joined = append(joined, block)
		}
		message["content"] = joined
	}
	list := make([]any, 0, len(merged))
	for _, message := range merged {
		list = append(list, message)
	}
	out["messages"] = list
	if tools, ok := out["tools"].([]any); ok {
		for _, tool := range tools {
			if schema, ok := tool.(map[string]any)["input_schema"].(map[string]any); ok {
				for key, value := range schema {
					if value == nil {
						delete(schema, key)
					}
				}
			}
		}
	}
	if tools, ok := out["tools"].([]any); ok && len(tools) == 0 {
		delete(out, "tools") // the legacy converter wrote an empty list
	}
	delete(out, "stream")
	return out
}

func blocksOf(content any) []any {
	switch c := content.(type) {
	case string:
		return []any{map[string]any{"type": "text", "text": c}}
	case []any:
		return c
	}
	return nil
}

func TestEncodeThinkingFormatAndImages(t *testing.T) {
	request := &textgen.Request{
		Model:        textgen.ModelRef{Canonical: "claude-haiku-5-5", Upstream: "claude-haiku-5-5-upstream"},
		Instructions: []textgen.Instruction{{Role: "developer", Text: "be brief"}, {Role: "system", Text: ""}},
		Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{
			{Kind: textgen.ContentText, Text: "see"},
			{Kind: textgen.ContentImage, URL: "https://example.com/a.png"},
		}}},
		Output:    textgen.OutputConstraint{Type: "json_schema", Name: "t", Schema: json.RawMessage(`{"type":"object"}`)},
		Reasoning: textgen.ReasoningOptions{Effort: "medium"},
		Metadata:  textgen.MetadataOptions{User: "u1"},
	}
	encoded, err := Encode(request, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if encoded.Model != "claude-haiku-5-5-upstream" {
		t.Fatalf("upstream model name wins: %q", encoded.Model)
	}
	system := encoded.System.([]dto.ClaudeMediaMessage)
	if len(system) != 1 || *system[0].Text != "be brief" {
		t.Fatalf("developer instruction becomes the system prompt, empty ones dropped: %+v", system)
	}
	blocks := encoded.Messages[0].Content.([]dto.ClaudeMediaMessage)
	if len(blocks) != 2 || blocks[1].Type != "image" || blocks[1].Source.Type != "url" || blocks[1].Source.Url != "https://example.com/a.png" {
		t.Fatalf("http image goes as a url source: %+v", blocks)
	}
	if encoded.Thinking == nil || encoded.Thinking.Type != "adaptive" {
		t.Fatalf("effort becomes the generic adaptive shape: %+v", encoded.Thinking)
	}
	var config map[string]any
	_ = json.Unmarshal(encoded.OutputConfig, &config)
	if config["effort"] != "medium" || config["format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("output_config must carry effort and the schema: %s", encoded.OutputConfig)
	}
	if string(encoded.Metadata) != `{"user_id":"u1"}` {
		t.Fatalf("metadata: %s", encoded.Metadata)
	}
	inlined, err := Encode(request, EncodeOptions{InlineImage: func(string) (string, string, error) { return "image/png", "AAAA", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if source := inlined.Messages[0].Content.([]dto.ClaudeMediaMessage)[1].Source; source.Type != "base64" || source.Data != "AAAA" {
		t.Fatalf("a channel that asks gets the image inlined: %+v", source)
	}
	off := &textgen.Request{Model: textgen.ModelRef{Canonical: "m"}, Reasoning: textgen.ReasoningOptions{Effort: "none"}, Conversation: request.Conversation[:1]}
	encodedOff, _ := Encode(off, EncodeOptions{})
	if encodedOff.Thinking == nil || encodedOff.Thinking.Type != "disabled" {
		t.Fatalf("effort none turns thinking off: %+v", encodedOff.Thinking)
	}
}

func TestEncodeReplaysOnlyAnthropicSealedReasoning(t *testing.T) {
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: "m"}, Conversation: []textgen.InputItem{
		{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "q"}}},
		{Kind: textgen.InputReasoning, Role: "assistant", Opaque: []textgen.OpaqueValue{{Dialect: SealedBy, Value: json.RawMessage(`{"type":"thinking","thinking":"t","signature":"sig"}`)}}},
		{Kind: textgen.InputReasoning, Role: "assistant", Opaque: []textgen.OpaqueValue{{Dialect: "openai_responses", Value: json.RawMessage(`{"type":"reasoning","id":"rs_1"}`)}}},
		{Kind: textgen.InputMessage, Role: "assistant", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "a"}}},
		{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "more"}}},
	}}
	encoded, err := Encode(request, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assistant := encoded.Messages[1].Content.([]dto.ClaudeMediaMessage)
	if len(assistant) != 2 || assistant[0].Type != "thinking" || assistant[0].Signature != "sig" || assistant[1].Type != "text" {
		t.Fatalf("Anthropic's own thinking goes back signed, another dialect's is left out: %+v", assistant)
	}
}

func TestPairToolBlocksMendsTheExchange(t *testing.T) {
	text := func(s string) dto.ClaudeMediaMessage { return dto.ClaudeMediaMessage{Type: "text", Text: &s} }
	messages := []dto.ClaudeMessage{
		{Role: "user", Content: []dto.ClaudeMediaMessage{text("q")}},
		{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "tool_use", Id: "a", Name: "f", Input: map[string]any{}}, {Type: "tool_use", Id: "b", Name: "f", Input: map[string]any{}}}},
		{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "tool_result", ToolUseId: "a", Content: "ok"}, {Type: "tool_result", ToolUseId: "a", Content: "again"}, {Type: "tool_result", ToolUseId: "zzz", Content: "orphan"}, text("next")}},
		{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "tool_use", Id: "c", Name: "f", Input: map[string]any{}}}},
	}
	mended := pairToolBlocks(messages)
	user := mended[2].Content.([]dto.ClaudeMediaMessage)
	ids := []string{}
	for _, block := range user {
		if block.Type == "tool_result" {
			ids = append(ids, block.ToolUseId)
		}
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("expected results for a and a synthetic one for b, got %v (%+v)", ids, user)
	}
	if user[len(user)-1].Type != "text" || *user[len(user)-1].Text != "next" {
		t.Fatalf("other blocks keep their place: %+v", user)
	}
	last := mended[len(mended)-1].Content.([]dto.ClaudeMediaMessage)
	if mended[len(mended)-1].Role != "user" || len(last) != 1 || last[0].ToolUseId != "c" {
		t.Fatalf("a trailing unanswered call gets a user message of its own: %+v", mended[len(mended)-1])
	}
}

func TestDecodeMessageWhole(t *testing.T) {
	body := `{"id":"msg_1","model":"claude-haiku-5-5","content":[{"type":"thinking","thinking":"hm","signature":"s"},{"type":"text","text":"Hi"},{"type":"tool_use","id":"toolu_1","name":"f","input":{"x":1}}],"stop_reason":"tool_use","usage":{"input_tokens":3,"output_tokens":4}}`
	events, err := DecodeMessage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var col textgen.Collector
	col.Add(events...)
	result := col.Result()
	if len(result.Parts) != 3 || result.Parts[0].Kind != textgen.PartReasoning || result.Parts[0].Signature != "s" || result.Parts[2].Kind != textgen.PartTool || result.Parts[2].Text != `{"x":1}` || result.Stop != textgen.StopTool || result.Usage.TotalTokens != 7 {
		t.Fatalf("decoded whole message wrong: %+v", result)
	}
}

func TestEncodeWritesAnthropicOptions(t *testing.T) {
	marker := json.RawMessage(`{"type":"ephemeral"}`)
	request := &textgen.Request{
		Model:        textgen.ModelRef{Canonical: "claude-haiku-5-5"},
		Instructions: []textgen.Instruction{{Role: "system", Text: "policy", CacheControl: marker}},
		Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hi", CacheControl: marker}}}},
		Tools:        []textgen.ToolDefinition{{Type: "function", Name: "lookup", CacheControl: marker}},
		Vendor:       textgen.VendorOptions{CacheControl: marker, Speed: json.RawMessage(`"fast"`)},
	}
	encoded, err := Encode(request, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(encoded)
	for _, path := range []string{"system.0.cache_control.type", "messages.0.content.0.cache_control.type", "tools.0.cache_control.type", "cache_control.type"} {
		if gjson.GetBytes(raw, path).Str != "ephemeral" {
			t.Fatalf("%s missing: %s", path, raw)
		}
	}
	if gjson.GetBytes(raw, "speed").Str != "fast" {
		t.Fatalf("speed missing: %s", raw)
	}
}
