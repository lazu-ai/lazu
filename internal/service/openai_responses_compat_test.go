package service

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
)

func TestChatCompletionsToResponsesRequest_BasicMapping(t *testing.T) {
	temperature := 0.3
	parallelToolCalls := true
	req := &dto.GeneralOpenAIRequest{
		Model:               "gpt-4.1-mini",
		Stream:              true,
		MaxCompletionTokens: 256,
		Temperature:         &temperature,
		TopP:                common.GetPointer(0.8),
		User:                "u-1",
		ServiceTier:         "flex",
		SafetyIdentifier:    "safe-user-1",
		ReasoningEffort:     "medium",
		ParallelToolCalls:   &parallelToolCalls,
		PromptCacheKey:      "cache-key",
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "get_weather",
			},
		},
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        "get_weather",
					Description: "Get weather by city",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"city": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		Messages: []dto.Message{
			{Role: "system", Content: "You are a helpful assistant"},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there"},
		},
	}

	got, err := ChatCompletionsToResponsesRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Model != req.Model {
		t.Fatalf("model mismatch: got=%s want=%s", got.Model, req.Model)
	}
	if !got.Stream {
		t.Fatal("stream should be mapped as true")
	}
	if got.MaxOutputTokens != req.MaxCompletionTokens {
		t.Fatalf("max_output_tokens mismatch: got=%d want=%d", got.MaxOutputTokens, req.MaxCompletionTokens)
	}
	if got.ServiceTier != "flex" || got.SafetyIdentifier != "safe-user-1" {
		t.Fatalf("shared responses fields mismatch: service_tier=%q safety_identifier=%q", got.ServiceTier, got.SafetyIdentifier)
	}
	if got.Reasoning == nil || got.Reasoning.Effort != "medium" {
		t.Fatalf("reasoning effort mismatch: %+v", got.Reasoning)
	}
	if string(got.ToolChoice) != `{"name":"get_weather","type":"function"}` {
		t.Fatalf("tool_choice mismatch: %s", string(got.ToolChoice))
	}
	if len(got.Tools) == 0 {
		t.Fatal("tools should be mapped")
	}
	if len(got.Input) == 0 {
		t.Fatal("input should be mapped")
	}

	var instructions string
	if err := common.Unmarshal(got.Instructions, &instructions); err != nil {
		t.Fatalf("unmarshal instructions failed: %v", err)
	}
	if instructions == "" {
		t.Fatal("instructions should contain system message")
	}
}

func TestChatCompletionsToResponsesRequest_RejectsToolRoleWithoutCallID(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-4.1-mini",
		Messages: []dto.Message{
			{Role: "tool", Content: "tool output"},
		},
	}

	_, err := ChatCompletionsToResponsesRequest(req)
	if err == nil {
		t.Fatal("expected missing tool_call_id error")
	}
}

func TestChatCompletionsToResponsesRequest_MapsToolLoopStructuredOutputAndWebSearch(t *testing.T) {
	assistant := dto.Message{Role: "assistant", Content: "checking"}
	assistant.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_weather",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "get_weather",
				Arguments: `{"city":"Paris"}`,
			},
		},
	})
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-4.1-mini",
		Messages: []dto.Message{
			{Role: "system", Content: "You are concise."},
			{Role: "user", Content: "Weather?"},
			assistant,
			{Role: "tool", ToolCallId: "call_weather", Content: `{"temp":"25C"}`},
		},
		ResponseFormat: &dto.ResponseFormat{
			Type:       "json_schema",
			JsonSchema: json.RawMessage(`{"name":"weather","strict":true,"schema":{"type":"object"}}`),
		},
		WebSearchOptions: &dto.WebSearchOptions{
			SearchContextSize: "low",
			UserLocation:      json.RawMessage(`{"type":"approximate","approximate":{"country":"FR"}}`),
		},
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:       "get_weather",
					Parameters: map[string]any{"type": "object"},
				},
			},
		},
	}

	got, err := ChatCompletionsToResponsesRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var input []map[string]any
	if err := common.Unmarshal(got.Input, &input); err != nil {
		t.Fatalf("unmarshal input failed: %v", err)
	}
	if !hasResponsesItem(input, dto.ResponsesOutputTypeFunctionCall, "call_id", "call_weather") {
		t.Fatalf("expected function_call input item, got %#v", input)
	}
	if !hasResponsesItem(input, "function_call_output", "call_id", "call_weather") {
		t.Fatalf("expected function_call_output input item, got %#v", input)
	}

	var text map[string]map[string]any
	if err := common.Unmarshal(got.Text, &text); err != nil {
		t.Fatalf("unmarshal text failed: %v", err)
	}
	if text["format"]["type"] != "json_schema" || text["format"]["name"] != "weather" {
		t.Fatalf("expected response_format to map to text.format, got %#v", text)
	}

	var tools []map[string]any
	if err := common.Unmarshal(got.Tools, &tools); err != nil {
		t.Fatalf("unmarshal tools failed: %v", err)
	}
	if !hasResponsesItem(tools, dto.BuildInToolWebSearchPreview, "search_context_size", "low") {
		t.Fatalf("expected web_search_preview tool, got %#v", tools)
	}
	if len(got.ToolChoice) != 0 {
		t.Fatalf("expected function-tool request to keep tool_choice unset, got %s", string(got.ToolChoice))
	}

	webSearchTool := findResponsesItem(tools, dto.BuildInToolWebSearchPreview)
	location, ok := webSearchTool["user_location"].(map[string]any)
	if !ok {
		t.Fatalf("expected flattened user_location, got %#v", webSearchTool["user_location"])
	}
	if location["type"] != "approximate" || location["country"] != "FR" {
		t.Fatalf("expected flattened user_location fields, got %#v", location)
	}
}

func TestChatCompletionsToResponsesRequest_DefaultsWebSearchToolChoiceWithoutFunctionTools(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-4.1-mini",
		Messages: []dto.Message{
			{Role: "user", Content: "Search the web."},
		},
		WebSearchOptions: &dto.WebSearchOptions{
			SearchContextSize: "low",
		},
	}

	got, err := ChatCompletionsToResponsesRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got.ToolChoice) != `{"type":"web_search_preview"}` {
		t.Fatalf("expected default web_search tool_choice, got %s", string(got.ToolChoice))
	}
}

func TestChatCompletionsToResponsesRequest_PreservesExplicitToolChoiceWithWebSearch(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-4.1-mini",
		Messages: []dto.Message{
			{Role: "user", Content: "Weather?"},
		},
		WebSearchOptions: &dto.WebSearchOptions{
			SearchContextSize: "low",
		},
		ToolChoice: "auto",
	}

	got, err := ChatCompletionsToResponsesRequest(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got.ToolChoice) != `"auto"` {
		t.Fatalf("expected explicit tool_choice to be preserved, got %s", string(got.ToolChoice))
	}
}

func TestOpenAIResponsesToChatCompletionsResponse_Basic(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        "resp_123",
		CreatedAt: 1700000000,
		Model:     "gpt-4.1-mini",
		Status:    "completed",
		Output: []dto.ResponsesOutput{
			{
				Type: "message",
				Content: []dto.ResponsesOutputContent{
					{Type: "output_text", Text: "Hello"},
					{Type: "output_text", Text: "world"},
				},
			},
		},
		Usage: &dto.Usage{
			InputTokens:  10,
			OutputTokens: 3,
			TotalTokens:  13,
		},
	}

	got := OpenAIResponsesToChatCompletionsResponse(resp, "fallback-model")
	if got.Object != "chat.completion" {
		t.Fatalf("unexpected object: %s", got.Object)
	}
	if got.Model != "gpt-4.1-mini" {
		t.Fatalf("unexpected model: %s", got.Model)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("unexpected choices len: %d", len(got.Choices))
	}
	if got.Choices[0].Message.StringContent() != "Hello\nworld" {
		t.Fatalf("unexpected content: %q", got.Choices[0].Message.StringContent())
	}
	if got.PromptTokens != 10 || got.CompletionTokens != 3 || got.TotalTokens != 13 {
		t.Fatalf("unexpected usage mapping: %+v", got.Usage)
	}
}

func TestOpenAIResponsesToChatCompletionsResponse_PreservesAnnotations(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        "resp_annotations",
		CreatedAt: 1700000000,
		Model:     "gpt-4.1-mini",
		Status:    "completed",
		Output: []dto.ResponsesOutput{
			{
				Type: "message",
				Content: []dto.ResponsesOutputContent{
					{
						Type: "output_text",
						Text: "source-backed answer",
						Annotations: []interface{}{
							map[string]interface{}{
								"type":  "url_citation",
								"url":   "https://example.com/source",
								"title": "Source",
							},
						},
					},
				},
			},
		},
	}

	got := OpenAIResponsesToChatCompletionsResponse(resp, "fallback-model")
	if len(got.Choices) != 1 || len(got.Choices[0].Message.Annotations) == 0 {
		t.Fatalf("expected annotations to be preserved, got %#v", got.Choices)
	}

	var annotations []map[string]interface{}
	if err := common.Unmarshal(got.Choices[0].Message.Annotations, &annotations); err != nil {
		t.Fatalf("unmarshal annotations failed: %v", err)
	}
	if len(annotations) != 1 || annotations[0]["type"] != "url_citation" {
		t.Fatalf("expected url_citation annotation, got %#v", annotations)
	}
	citation, ok := annotations[0]["url_citation"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected chat url_citation wrapper, got %#v", annotations[0])
	}
	if citation["url"] != "https://example.com/source" || citation["title"] != "Source" {
		t.Fatalf("unexpected citation payload: %#v", citation)
	}
}

func TestOpenAIResponsesToChatCompletionsResponse_MapsFunctionCalls(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        "resp_tool_1",
		CreatedAt: 1700000000,
		Model:     "gpt-4.1-mini",
		Status:    "completed",
		Output: []dto.ResponsesOutput{
			{
				Type:      dto.ResponsesOutputTypeFunctionCall,
				ID:        "fc_weather",
				CallID:    "call_weather",
				Name:      "get_weather",
				Arguments: `{"city":"Paris"}`,
			},
		},
	}

	got := OpenAIResponsesToChatCompletionsResponse(resp, "fallback-model")
	if got.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("expected tool_calls finish reason, got %q", got.Choices[0].FinishReason)
	}
	toolCalls := got.Choices[0].Message.ParseToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", got.Choices[0].Message.ToolCalls)
	}
	if toolCalls[0].ID != "call_weather" || toolCalls[0].Function.Name != "get_weather" || toolCalls[0].Function.Arguments != `{"city":"Paris"}` {
		t.Fatalf("unexpected tool call mapping: %#v", toolCalls[0])
	}
}

func TestOpenAIResponsesToChatCompletionsResponse_IncompleteSuppressesFunctionCalls(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        "resp_tool_incomplete",
		CreatedAt: 1700000000,
		Model:     "gpt-4.1-mini",
		Status:    "incomplete",
		Output: []dto.ResponsesOutput{
			{
				Type:      dto.ResponsesOutputTypeFunctionCall,
				ID:        "fc_weather",
				CallID:    "call_weather",
				Name:      "get_weather",
				Arguments: `{"city":"Par`,
			},
		},
	}

	got := OpenAIResponsesToChatCompletionsResponse(resp, "fallback-model")
	if got.Choices[0].FinishReason != "length" {
		t.Fatalf("expected length finish reason, got %q", got.Choices[0].FinishReason)
	}
	if toolCalls := got.Choices[0].Message.ParseToolCalls(); len(toolCalls) != 0 {
		t.Fatalf("expected incomplete function call to be suppressed, got %#v", toolCalls)
	}
}

func TestOpenAIResponsesToChatCompletionsResponse_MapsPromptCacheHitTokens(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        "resp_cache",
		CreatedAt: 1700000000,
		Model:     "deepseek-v3.2",
		Status:    "completed",
		Output: []dto.ResponsesOutput{
			{
				Type: "message",
				Content: []dto.ResponsesOutputContent{
					{Type: "output_text", Text: "Hello"},
				},
			},
		},
		Usage: &dto.Usage{
			InputTokens:          100,
			OutputTokens:         10,
			TotalTokens:          110,
			PromptCacheHitTokens: 64,
		},
	}

	got := OpenAIResponsesToChatCompletionsResponse(resp, "fallback-model")
	if got.PromptTokensDetails.CachedTokens != 64 {
		t.Fatalf("expected cached tokens to map from prompt_cache_hit_tokens, got %d", got.PromptTokensDetails.CachedTokens)
	}
}

func TestOpenAIResponsesToChatCompletionsResponse_PreservesImageOutputTokens(t *testing.T) {
	resp := &dto.OpenAIResponsesResponse{
		ID:        "resp_image",
		CreatedAt: 1700000000,
		Model:     "gpt-image-2",
		Status:    "completed",
		Output: []dto.ResponsesOutput{
			{
				Type: "message",
				Content: []dto.ResponsesOutputContent{
					{Type: "output_text", Text: "ok"},
				},
			},
		},
		Usage: &dto.Usage{
			InputTokens:  12,
			OutputTokens: 7024,
			TotalTokens:  7036,
			CompletionTokenDetails: dto.OutputTokenDetails{
				ImageTokens: 7024,
			},
		},
	}

	got := OpenAIResponsesToChatCompletionsResponse(resp, "fallback-model")
	// Without carrying ImageTokens through, all 7024 tokens bill at the text
	// output rate instead of the image-output rate.
	if got.CompletionTokenDetails.ImageTokens != 7024 {
		t.Fatalf("expected image output tokens to survive the responses->chat mapping, got %d", got.CompletionTokenDetails.ImageTokens)
	}
}

func hasResponsesItem(items []map[string]any, itemType string, key string, value string) bool {
	return findResponsesItemWithValue(items, itemType, key, value) != nil
}

func findResponsesItemWithValue(items []map[string]any, itemType string, key string, value string) map[string]any {
	for _, item := range items {
		if item["type"] != itemType {
			continue
		}
		if item[key] == value {
			return item
		}
	}
	return nil
}

func findResponsesItem(items []map[string]any, itemType string) map[string]any {
	for _, item := range items {
		if item["type"] == itemType {
			return item
		}
	}
	return nil
}
