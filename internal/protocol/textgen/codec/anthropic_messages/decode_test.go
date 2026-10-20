package anthropic_messages

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
	"github.com/tidwall/gjson"
)

// What Claude Code actually sends (a recorded request) decodes with nothing
// unknown: every field it uses is modeled or advisory.
func TestDecodeClaudeCodeRequest(t *testing.T) {
	request, wire, err := Decode(fixtures.ClientRequest(t, "claude_code/tool_image_turn.json"))
	if err != nil {
		t.Fatal(err)
	}
	if wire == nil || wire.Model == "" {
		t.Fatal("wire request missing")
	}
	if len(request.Raw.UnknownFields) != 0 {
		t.Fatalf("unknown fields in a Claude Code request: %v", keys(request.Raw.UnknownFields))
	}
	if len(request.Instructions) != 3 || request.Instructions[1].CacheControl == nil {
		t.Fatalf("system blocks: %+v", request.Instructions)
	}
	if request.Reasoning.Effort != "high" || request.Lifecycle.Stream != true || !request.Sampling.MaxOutput.Set {
		t.Fatalf("reasoning or lifecycle lost: %+v %+v", request.Reasoning, request.Lifecycle)
	}
	// the session agent sessions and routing read, where they read it
	if !strings.Contains(gjson.GetBytes(request.Metadata.Metadata, "user_id").String(), "session-fixture") || request.Metadata.User != "" {
		t.Fatalf("metadata: %s user %q", request.Metadata.Metadata, request.Metadata.User)
	}
	var kinds []string
	var result *textgen.InputItem
	for index, item := range request.Conversation {
		kinds = append(kinds, string(item.Kind)+":"+item.Role)
		if item.Kind == textgen.InputFunctionResult {
			result = &request.Conversation[index]
		}
	}
	if result == nil || len(result.Content) != 1 || result.Content[0].Kind != textgen.ContentImage || !strings.HasPrefix(result.Content[0].URL, "data:image/") {
		t.Fatalf("the tool result's image was lost: %v", kinds)
	}
	systemTurns := 0
	for _, item := range request.Conversation {
		if item.Kind == textgen.InputMessage && item.Role == "system" {
			systemTurns++
		}
	}
	if systemTurns == 0 {
		t.Fatalf("mid-conversation system turns lost: %v", kinds)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

// The Claude Code request bridges to Chat and to Responses: system turns in
// place, the tool result's image after it, thinking with text as Chat's
// reasoning_content, Anthropic's seal nowhere.
func TestClaudeCodeRequestEncodesForChatAndResponses(t *testing.T) {
	request, _, err := Decode(fixtures.ClientRequest(t, "claude_code/tool_image_turn.json"))
	if err != nil {
		t.Fatal(err)
	}
	request.Model.Upstream = "deepseek-chat"
	chat, err := openai_chat.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(chat)
	messages := gjson.GetBytes(raw, "messages").Array()
	var roles []string
	for _, message := range messages {
		roles = append(roles, message.Get("role").String())
	}
	toolAt := -1
	for index, role := range roles {
		if role == "tool" {
			toolAt = index
		}
	}
	if toolAt < 0 || toolAt+1 >= len(messages) || roles[toolAt+1] != "user" || !strings.Contains(messages[toolAt+1].Raw, "image_url") {
		t.Fatalf("the image must follow the tool message as a user message: roles %v", roles)
	}
	if strings.Contains(string(raw), "c2lnbmF0dXJl") || strings.Contains(string(raw), "signature") {
		t.Fatalf("Anthropic's signature reached Chat: %s", raw)
	}
	if !strings.Contains(string(raw), "reasoning_content") {
		t.Fatalf("thinking with text must reach Chat as reasoning_content: %s", raw)
	}
	if gjson.GetBytes(raw, "reasoning_effort").String() != "high" || !strings.Contains(gjson.GetBytes(raw, "metadata.user_id").String(), "session-fixture") || gjson.GetBytes(raw, "user").Exists() {
		t.Fatalf("effort or metadata: %s %s", gjson.GetBytes(raw, "reasoning_effort").Raw, gjson.GetBytes(raw, "metadata").Raw)
	}
	if strings.Contains(string(raw), "cache_control") {
		t.Fatalf("advisory cache markers must not reach Chat: %s", raw)
	}

	responses, err := openai_responses.Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(responses)
	if !strings.Contains(gjson.GetBytes(raw, "input").Raw, `"input_image"`) || strings.Contains(string(raw), "signature") {
		t.Fatalf("Responses input: %s", gjson.GetBytes(raw, "input").Raw)
	}
}

func TestDecodeMapsThinkingToolChoiceAndRefusesWhatItDoesNotModel(t *testing.T) {
	body := `{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"hi"}],
		"thinking":{"type":"enabled","budget_tokens":10000},
		"tool_choice":{"type":"tool","name":"lookup","disable_parallel_tool_use":true},
		"tools":[{"name":"lookup","input_schema":{"type":"object"}},{"type":"bash_20250124","name":"bash"}],
		"mcp_servers":[{"type":"url","url":"https://x"}],
		"context_management":{"edits":[{"type":"clear_tool_uses_20250919"}]}}`
	request, _, err := Decode([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if !request.Reasoning.Budget.Set || request.Reasoning.Budget.Value != 10000 || textgen.ReasoningEffortFor(request.Reasoning) != "medium" {
		t.Fatalf("budget: %+v", request.Reasoning)
	}
	if request.ToolPolicy.Mode != "function" || request.ToolPolicy.FunctionName != "lookup" || !request.ToolPolicy.ParallelToolCalls.Set || request.ToolPolicy.ParallelToolCalls.Value {
		t.Fatalf("tool choice: %+v", request.ToolPolicy)
	}
	if len(request.Tools) != 2 || request.Tools[1].Type != "bash_20250124" {
		t.Fatalf("tools: %+v", request.Tools)
	}
	for _, path := range []string{"mcp_servers", "context_management"} {
		if _, ok := request.Raw.UnknownFields[path]; !ok {
			t.Fatalf("%s must stay unknown (native only): %v", path, keys(request.Raw.UnknownFields))
		}
	}
	disabled, _, err := Decode([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled"}}`))
	if err != nil || disabled.Reasoning.Effort != "none" {
		t.Fatalf("disabled thinking: %+v %v", disabled.Reasoning, err)
	}
}

func TestDecodeToolResultErrorAndZeroArgumentCall(t *testing.T) {
	body := `{"model":"m","messages":[
		{"role":"user","content":"go"},
		{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"now","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":[{"type":"text","text":"boom"}]}]}]}`
	request, _, err := Decode([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	call, result := request.Conversation[1], request.Conversation[2]
	if call.Kind != textgen.InputFunctionCall || call.Arguments != "{}" {
		t.Fatalf("call: %+v", call)
	}
	if result.Kind != textgen.InputFunctionResult || !result.IsError || result.Output != "boom" || textgen.ResultText(result) != "Error: boom" {
		t.Fatalf("result: %+v", result)
	}
}
