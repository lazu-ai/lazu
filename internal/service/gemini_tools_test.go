package service

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestGeminiGetToolsSnakeCase(t *testing.T) {
	payload := []byte(`{
		"contents": [{"role":"user","parts":[{"text":"hi"}]}],
		"tools": [{
			"function_declarations": [{
				"name": "get_weather",
				"description": "Get weather",
				"parameters": {
					"type": "object",
					"properties": {"location": {"type": "string"}},
					"required": ["location"]
				}
			}]
		}]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	tools := req.GetTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	declarations, ok := tools[0].FunctionDeclarations.([]dto.FunctionRequest)
	if !ok {
		t.Fatalf("expected FunctionDeclarations to be []dto.FunctionRequest")
	}
	if len(declarations) != 1 {
		t.Fatalf("expected 1 declaration, got %d", len(declarations))
	}
	if declarations[0].Name != "get_weather" {
		t.Fatalf("expected function name get_weather, got %q", declarations[0].Name)
	}
}

func TestGeminiToOpenAIRequestTools(t *testing.T) {
	payload := []byte(`{
		"contents": [{"role":"user","parts":[{"text":"hi"}]}],
		"tools": [{
			"function_declarations": [{
				"name": "get_weather",
				"description": "Get weather",
				"parameters": {
					"type": "object",
					"properties": {"location": {"type": "string"}},
					"required": ["location"]
				}
			}]
		}]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
	}
	openaiReq, err := GeminiToOpenAIRequest(&req, info)
	if err != nil {
		t.Fatalf("convert to openai request: %v", err)
	}

	if len(openaiReq.Tools) != 1 {
		t.Fatalf("expected 1 openai tool, got %d", len(openaiReq.Tools))
	}
	if openaiReq.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("expected function name get_weather, got %q", openaiReq.Tools[0].Function.Name)
	}
}

func TestGeminiSnakeCaseConfigAndFunctionResponse(t *testing.T) {
	payload := []byte(`{
		"contents": [{
			"role": "user",
			"parts": [
				{"text": "hi"},
				{"function_response": {"name": "get_weather", "response": {"temp": 25}}}
			]
		}],
		"generation_config": {
			"max_output_tokens": 64,
			"top_p": 0.9
		},
		"tool_config": {
			"function_calling_config": {
				"mode": "ANY",
				"allowed_function_names": ["get_weather"]
			}
		},
		"safety_settings": [{
			"category": "HARM_CATEGORY_DEROGATORY",
			"threshold": "BLOCK_NONE"
		}],
		"system_instruction": {
			"role": "user",
			"parts": [{"text": "system"}]
		},
		"cached_content": "cached-value"
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	if req.GenerationConfig.MaxOutputTokens != 64 {
		t.Fatalf("expected max_output_tokens 64, got %d", req.GenerationConfig.MaxOutputTokens)
	}
	if req.GenerationConfig.TopP != 0.9 {
		t.Fatalf("expected top_p 0.9, got %v", req.GenerationConfig.TopP)
	}

	if req.ToolConfig == nil || req.ToolConfig.FunctionCallingConfig == nil {
		t.Fatal("expected tool_config.function_calling_config to be parsed")
	}
	if req.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Fatalf("expected mode ANY, got %q", req.ToolConfig.FunctionCallingConfig.Mode)
	}
	if len(req.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) != 1 {
		t.Fatalf("expected 1 allowed function name, got %d", len(req.ToolConfig.FunctionCallingConfig.AllowedFunctionNames))
	}

	if len(req.SafetySettings) != 1 {
		t.Fatalf("expected 1 safety setting, got %d", len(req.SafetySettings))
	}
	if req.SystemInstructions == nil || len(req.SystemInstructions.Parts) != 1 {
		t.Fatal("expected system_instruction to be parsed")
	}
	if req.CachedContent != "cached-value" {
		t.Fatalf("expected cached_content cached-value, got %q", req.CachedContent)
	}

	if len(req.Contents) != 1 || len(req.Contents[0].Parts) != 2 {
		t.Fatal("expected contents parts to be parsed")
	}
	if req.Contents[0].Parts[1].FunctionResponse == nil {
		t.Fatal("expected function_response to be parsed")
	}
	if req.Contents[0].Parts[1].FunctionResponse.Name != "get_weather" {
		t.Fatalf("expected function_response name get_weather, got %q", req.Contents[0].Parts[1].FunctionResponse.Name)
	}
}

func TestGeminiThoughtSignatureRoundTrip(t *testing.T) {
	payload := []byte(`{
		"contents": [{
			"role": "model",
			"parts": [{
				"functionCall": {
					"id": "call_123",
					"name": "get_weather",
					"args": {"city": "Beijing"}
				},
				"thoughtSignature": "sig-123"
			}]
		}]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	if !req.HasThoughtSignature() {
		t.Fatal("expected request to report thoughtSignature presence")
	}
	if got := req.Contents[0].Parts[0].ThoughtSignature; got != "sig-123" {
		t.Fatalf("expected thoughtSignature sig-123, got %q", got)
	}
	if req.Contents[0].Parts[0].FunctionCall == nil {
		t.Fatal("expected functionCall to be parsed")
	}
	if got := req.Contents[0].Parts[0].FunctionCall.ID; got != "call_123" {
		t.Fatalf("expected functionCall.id call_123, got %q", got)
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal gemini request: %v", err)
	}

	var roundTrip map[string]any
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal round-trip payload: %v", err)
	}

	contents := roundTrip["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	part := parts[0].(map[string]any)
	if got := part["thoughtSignature"]; got != "sig-123" {
		t.Fatalf("expected round-trip thoughtSignature sig-123, got %#v", got)
	}
	functionCall := part["functionCall"].(map[string]any)
	if got := functionCall["id"]; got != "call_123" {
		t.Fatalf("expected round-trip functionCall.id call_123, got %#v", got)
	}
}

func TestGeminiToOpenAIRequest_PreservesToolCallIDsForFunctionResponses(t *testing.T) {
	payload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "北京天气和时间分别是什么？"}]
			},
			{
				"role": "model",
				"parts": [
					{"functionCall": {"id": "call_weather", "name": "get_weather", "args": {"city": "北京"}}},
					{"functionCall": {"id": "call_time", "name": "get_time", "args": {"city": "北京"}}}
				]
			},
			{
				"role": "user",
				"parts": [
					{"functionResponse": {"name": "get_time", "response": {"result": {"time": "10:00"}}}},
					{"functionResponse": {"name": "get_weather", "response": {"result": {"temperature": 26}}}}
				]
			}
		]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
	}
	openaiReq, err := GeminiToOpenAIRequest(&req, info)
	if err != nil {
		t.Fatalf("convert to openai request: %v", err)
	}

	if len(openaiReq.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(openaiReq.Messages))
	}

	toolCalls := openaiReq.Messages[1].ParseToolCalls()
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call_weather" || toolCalls[1].ID != "call_time" {
		t.Fatalf("expected assistant tool call ids to be preserved, got %q and %q", toolCalls[0].ID, toolCalls[1].ID)
	}

	if openaiReq.Messages[2].Role != "tool" || openaiReq.Messages[2].ToolCallId != "call_time" {
		t.Fatalf("expected first tool response to map to call_time, got role=%q tool_call_id=%q", openaiReq.Messages[2].Role, openaiReq.Messages[2].ToolCallId)
	}
	if openaiReq.Messages[3].Role != "tool" || openaiReq.Messages[3].ToolCallId != "call_weather" {
		t.Fatalf("expected second tool response to map to call_weather, got role=%q tool_call_id=%q", openaiReq.Messages[3].Role, openaiReq.Messages[3].ToolCallId)
	}
}

func TestGeminiToOpenAIRequest_GeneratesUniqueToolCallIDsAroundPreservedIDs(t *testing.T) {
	payload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "先查天气，再查时间"}]
			},
			{
				"role": "model",
				"parts": [
					{"functionCall": {"id": "call_1", "name": "get_weather", "args": {"city": "北京"}}},
					{"functionCall": {"name": "get_time", "args": {"city": "北京"}}}
				]
			},
			{
				"role": "user",
				"parts": [
					{"functionResponse": {"name": "get_weather", "response": {"result": {"temperature": 26}}}},
					{"functionResponse": {"name": "get_time", "response": {"result": {"time": "10:00"}}}}
				]
			}
		]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
	}
	openaiReq, err := GeminiToOpenAIRequest(&req, info)
	if err != nil {
		t.Fatalf("convert to openai request: %v", err)
	}

	toolCalls := openaiReq.Messages[1].ParseToolCalls()
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call_1" {
		t.Fatalf("expected preserved id call_1, got %q", toolCalls[0].ID)
	}
	if toolCalls[1].ID != "call_2" {
		t.Fatalf("expected generated id call_2 to avoid collision, got %q", toolCalls[1].ID)
	}
	if openaiReq.Messages[2].ToolCallId != "call_1" {
		t.Fatalf("expected weather response to map to call_1, got %q", openaiReq.Messages[2].ToolCallId)
	}
	if openaiReq.Messages[3].ToolCallId != "call_2" {
		t.Fatalf("expected time response to map to call_2, got %q", openaiReq.Messages[3].ToolCallId)
	}
}

func TestGeminiToOpenAIRequest_ReservesFuturePreservedIDsBeforeGenerating(t *testing.T) {
	payload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "先查时间，再查天气"}]
			},
			{
				"role": "model",
				"parts": [
					{"functionCall": {"name": "get_time", "args": {"city": "北京"}}},
					{"functionCall": {"id": "call_1", "name": "get_weather", "args": {"city": "北京"}}}
				]
			},
			{
				"role": "user",
				"parts": [
					{"functionResponse": {"name": "get_time", "response": {"result": {"time": "10:00"}}}},
					{"functionResponse": {"name": "get_weather", "response": {"result": {"temperature": 26}}}}
				]
			}
		]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
	}
	openaiReq, err := GeminiToOpenAIRequest(&req, info)
	if err != nil {
		t.Fatalf("convert to openai request: %v", err)
	}

	toolCalls := openaiReq.Messages[1].ParseToolCalls()
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}
	if toolCalls[0].ID != "call_2" {
		t.Fatalf("expected generated id call_2 to avoid future preserved call_1, got %q", toolCalls[0].ID)
	}
	if toolCalls[1].ID != "call_1" {
		t.Fatalf("expected preserved id call_1, got %q", toolCalls[1].ID)
	}
}

func TestGeminiToOpenAIRequest_RejectsDuplicatePreservedToolCallIDs(t *testing.T) {
	payload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "重复 ID"}]
			},
			{
				"role": "model",
				"parts": [
					{"functionCall": {"id": "call_1", "name": "get_weather", "args": {"city": "北京"}}},
					{"functionCall": {"id": "call_1", "name": "get_time", "args": {"city": "北京"}}}
				]
			}
		]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o"},
	}
	if _, err := GeminiToOpenAIRequest(&req, info); err == nil {
		t.Fatal("expected duplicate preserved tool call ids to be rejected")
	}
}

func TestGeminiHasCurrentTurnThoughtSignature(t *testing.T) {
	payload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "北京的天气怎么样，现在几点了？"}]
			},
			{
				"role": "model",
				"parts": [
					{"thoughtSignature": "sig-123", "functionCall": {"id": "call_1", "name": "get_weather", "args": {"city": "北京"}}},
					{"functionCall": {"id": "call_2", "name": "get_time", "args": {}}}
				]
			},
			{
				"role": "user",
				"parts": [
					{"functionResponse": {"name": "get_weather", "response": {"result": {"temperature": 26}}}},
					{"functionResponse": {"name": "get_time", "response": {"result": {"time": "10:00"}}}}
				]
			}
		]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	if !req.HasCurrentTurnThoughtSignature() {
		t.Fatal("expected request to report current-turn thoughtSignature")
	}
}

func TestGeminiHasCurrentTurnThoughtSignature_IgnoresCurrentTurnToolLoopWithoutThoughtSignature(t *testing.T) {
	payload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "北京的天气怎么样，现在几点了？"}]
			},
			{
				"role": "model",
				"parts": [
					{"functionCall": {"id": "call_1", "name": "get_weather", "args": {"city": "北京"}}},
					{"functionCall": {"id": "call_2", "name": "get_time", "args": {}}}
				]
			},
			{
				"role": "user",
				"parts": [
					{"functionResponse": {"name": "get_weather", "response": {"result": {"temperature": 26}}}},
					{"functionResponse": {"name": "get_time", "response": {"result": {"time": "10:00"}}}}
				]
			}
		]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	if req.HasCurrentTurnThoughtSignature() {
		t.Fatal("expected current-turn tool loop without thoughtSignature to be relayable")
	}
}

func TestGeminiHasCurrentTurnThoughtSignature_IgnoresHistoricalCompletedToolLoop(t *testing.T) {
	payload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "北京的天气怎么样？"}]
			},
			{
				"role": "model",
				"parts": [
					{"functionCall": {"id": "call_1", "name": "get_weather", "args": {"city": "北京"}}, "thoughtSignature": "sig-123"}
				]
			},
			{
				"role": "user",
				"parts": [
					{"functionResponse": {"name": "get_weather", "response": {"result": {"temperature": 26}}}}
				]
			},
			{
				"role": "model",
				"parts": [{"text": "北京现在晴，26 度。"}]
			},
			{
				"role": "user",
				"parts": [{"text": "那上海呢？"}]
			}
		]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	if req.HasCurrentTurnThoughtSignature() {
		t.Fatal("expected completed historical tool loop to be ignored")
	}
}

func TestGeminiHasCurrentTurnThoughtSignature_IgnoresHistoricalThinkingSignature(t *testing.T) {
	payload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "先想一下。"}]
			},
			{
				"role": "model",
				"parts": [{"text": "这是历史回答。", "thoughtSignature": "sig-thinking-only"}]
			},
			{
				"role": "user",
				"parts": [{"text": "继续。"}]
			}
		]
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}

	if req.HasCurrentTurnThoughtSignature() {
		t.Fatal("expected historical thoughtSignature to be ignored")
	}
}
