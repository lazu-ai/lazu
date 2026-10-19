package service

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestRebuildLoggedStreamResponseBodyChatCompletion(t *testing.T) {
	t.Parallel()

	payload := &ModelLogPayload{
		IsStream: true,
		ResponseChunks: []string{
			`{"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"gpt-4.1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"gpt-4.1","choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"gpt-4.1","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"gpt-4.1","choices":[{"index":0,"finish_reason":"stop"}]}`,
			`{"id":"chatcmpl-test","object":"chat.completion.chunk","created":123,"model":"gpt-4.1","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3,"prompt_tokens_details":{},"completion_tokens_details":{},"input_tokens":0,"output_tokens":0}}`,
		},
		Usage: dto.Usage{
			PromptTokens:     1,
			CompletionTokens: 2,
			TotalTokens:      3,
		},
	}

	body := rebuildLoggedStreamResponseBody(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1"},
	}, payload)
	if len(body) == 0 {
		t.Fatal("expected rebuilt response body")
	}

	var response dto.OpenAITextResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("unmarshal rebuilt chat completion response: %v", err)
	}

	if response.Object != "chat.completion" {
		t.Fatalf("unexpected response object: %q", response.Object)
	}
	if len(response.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(response.Choices))
	}
	if got := response.Choices[0].Message.StringContent(); got != "Hello" {
		t.Fatalf("unexpected reconstructed content: %q", got)
	}
	if response.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected usage total_tokens: %d", response.Usage.TotalTokens)
	}
}

func TestRebuildLoggedStreamResponseBodyPreservesGeminiToolSignature(t *testing.T) {
	t.Parallel()

	payload := &ModelLogPayload{
		IsStream: true,
		ResponseChunks: []string{
			`{"id":"chatcmpl-gemini","object":"chat.completion.chunk","created":123,"model":"gemini-3.6-flash","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_123","type":"function","function":{"name":"get_weather","arguments":""},"extra_content":{"google":{"thought_signature":"signature-123"}}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-gemini","object":"chat.completion.chunk","created":123,"model":"gemini-3.6-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"type":null,"function":{"arguments":"{\"city\":\"Taipei\"}"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-gemini","object":"chat.completion.chunk","created":123,"model":"gemini-3.6-flash","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		},
	}

	body := rebuildLoggedStreamResponseBody(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.6-flash"},
	}, payload)
	if len(body) == 0 {
		t.Fatal("expected rebuilt response body")
	}

	var response dto.OpenAITextResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("unmarshal rebuilt Gemini response: %v", err)
	}
	if len(response.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(response.Choices))
	}
	toolCalls := response.Choices[0].Message.ParseToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	call := toolCalls[0]
	if call.ID != "call_123" || call.Function.Name != "get_weather" {
		t.Fatalf("unexpected rebuilt tool call: %+v", call)
	}
	if call.Function.Arguments != `{"city":"Taipei"}` {
		t.Fatalf("unexpected rebuilt arguments: %q", call.Function.Arguments)
	}
	if call.ExtraContent == nil || call.ExtraContent.Google == nil ||
		call.ExtraContent.Google.ThoughtSignature != "signature-123" {
		t.Fatalf("expected preserved Gemini thought signature, got %+v", call.ExtraContent)
	}
}

func TestRebuildLoggedStreamResponseBodyResponses(t *testing.T) {
	t.Parallel()

	payload := &ModelLogPayload{
		IsStream: true,
		ResponseChunks: []string{
			`{"type":"response.output_text.delta","delta":"Hel"}`,
			`{"type":"response.output_text.delta","delta":"lo"}`,
			`{"type":"response.completed","response":{"id":"resp_test","object":"response","created_at":456,"status":"completed","model":"gpt-5","output":[{"type":"message","id":"msg_test","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello","annotations":[]}]}]}}`,
		},
		Usage: dto.Usage{
			PromptTokens:     4,
			CompletionTokens: 5,
			TotalTokens:      9,
		},
	}

	body := rebuildLoggedStreamResponseBody(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"},
	}, payload)
	if len(body) == 0 {
		t.Fatal("expected rebuilt responses body")
	}

	var response dto.OpenAIResponsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("unmarshal rebuilt responses body: %v", err)
	}

	if response.Object != "response" {
		t.Fatalf("unexpected response object: %q", response.Object)
	}
	if response.Usage == nil || response.Usage.TotalTokens != 9 {
		t.Fatalf("expected usage total_tokens to be injected, got %+v", response.Usage)
	}
	if len(response.Output) != 1 || len(response.Output[0].Content) != 1 {
		t.Fatalf("unexpected output shape: %+v", response.Output)
	}
	if got := response.Output[0].Content[0].Text; got != "Hello" {
		t.Fatalf("unexpected reconstructed output text: %q", got)
	}
}

func TestRebuildLoggedStreamResponseBodyResponsesMetadataOnlyCompleted(t *testing.T) {
	t.Parallel()

	payload := &ModelLogPayload{
		IsStream: true,
		ResponseChunks: []string{
			`{"type":"response.output_text.delta","delta":"Hello"}`,
			`{"type":"response.output_text.delta","delta":" world"}`,
			`{"type":"response.completed","response":{"id":"resp_test","object":"response","created_at":456,"status":"completed","model":"gpt-5","usage":{"input_tokens":4,"output_tokens":5,"total_tokens":9}}}`,
		},
		Usage: dto.Usage{
			PromptTokens:     4,
			CompletionTokens: 5,
			TotalTokens:      9,
		},
	}

	body := rebuildLoggedStreamResponseBody(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"},
	}, payload)
	if len(body) == 0 {
		t.Fatal("expected rebuilt responses body")
	}

	var response dto.OpenAIResponsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("unmarshal rebuilt responses body: %v", err)
	}

	if response.Usage == nil || response.Usage.TotalTokens != 9 {
		t.Fatalf("expected usage total_tokens to be injected, got %+v", response.Usage)
	}
	if len(response.Output) != 1 || len(response.Output[0].Content) != 1 {
		t.Fatalf("unexpected output shape: %+v", response.Output)
	}
	if got := response.Output[0].Content[0].Text; got != "Hello world" {
		t.Fatalf("unexpected reconstructed output text: %q", got)
	}
}

func TestRebuildLoggedStreamResponseBodyResponsesPreservesNonTextOutputItems(t *testing.T) {
	t.Parallel()

	payload := &ModelLogPayload{
		IsStream: true,
		ResponseChunks: []string{
			`{"type":"response.output_text.delta","delta":"Hello world"}`,
			`{"type":"response.completed","response":{"id":"resp_test","object":"response","created_at":456,"status":"completed","model":"gpt-5","output":[{"type":"image_generation_call","quality":"high","size":"1024x1024"}]}}`,
		},
	}

	body := rebuildLoggedStreamResponseBody(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"},
	}, payload)
	if len(body) == 0 {
		t.Fatal("expected rebuilt responses body")
	}

	var response dto.OpenAIResponsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("unmarshal rebuilt responses body: %v", err)
	}

	if len(response.Output) != 2 {
		t.Fatalf("expected preserved non-text output plus backfilled message, got %+v", response.Output)
	}
	if response.Output[0].Type != dto.ResponsesOutputTypeImageGenerationCall {
		t.Fatalf("expected first output item to be preserved, got %+v", response.Output[0])
	}
	if got := response.Output[1].Content[0].Text; got != "Hello world" {
		t.Fatalf("unexpected reconstructed output text: %q", got)
	}
}
