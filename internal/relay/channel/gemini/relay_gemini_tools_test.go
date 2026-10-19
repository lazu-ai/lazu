package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestClearToolCallsRemovesGeminiThoughtSignatureFromArgumentChunk(t *testing.T) {
	toolIndex := 0
	response := dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{
							Index: &toolIndex,
							ID:    "call_123",
							Type:  "function",
							Function: dto.FunctionResponse{
								Name:      "get_weather",
								Arguments: `{"city":"Taipei"}`,
							},
							ExtraContent: &dto.ToolCallExtraContent{
								Google: &dto.GoogleToolCallExtraContent{
									ThoughtSignature: "signature-123",
								},
							},
						},
					},
				},
			},
		},
	}
	openingResponse := response
	openingResponse.Choices = append([]dto.ChatCompletionsStreamResponseChoice(nil), response.Choices...)
	openingResponse.Choices[0].Delta.ToolCalls = append(
		[]dto.ToolCallResponse(nil),
		response.Choices[0].Delta.ToolCalls...,
	)
	openingResponse.Choices[0].Delta.ToolCalls[0].Function.Arguments = ""

	response.ClearToolCalls()
	call := response.Choices[0].Delta.ToolCalls[0]
	if call.ID != "" || call.Type != nil || call.Function.Name != "" {
		t.Fatalf("expected streamed tool-call metadata to be cleared, got %+v", call)
	}
	if call.ExtraContent != nil {
		t.Fatalf("expected Gemini thought signature only in opening chunk, got %+v", call.ExtraContent)
	}
	if call.Function.Arguments != `{"city":"Taipei"}` {
		t.Fatalf("expected arguments to remain in argument chunk, got %q", call.Function.Arguments)
	}

	openingChunk, err := json.Marshal(openingResponse)
	if err != nil {
		t.Fatalf("marshal opening chunk: %v", err)
	}
	argumentChunk, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal argument chunk: %v", err)
	}
	if got := strings.Count(string(openingChunk)+string(argumentChunk), "thought_signature"); got != 1 {
		t.Fatalf("expected thought signature exactly once across stream chunks, got %d", got)
	}
	if strings.Contains(string(argumentChunk), "thought_signature") {
		t.Fatalf("expected no thought signature in argument chunk: %s", argumentChunk)
	}
	if !strings.Contains(string(argumentChunk), `"arguments":"{\"city\":\"Taipei\"}"`) {
		t.Fatalf("expected arguments to remain in argument chunk: %s", argumentChunk)
	}
}

func TestGetResponseToolCallPreservesGeminiIDAndThoughtSignature(t *testing.T) {
	part := dto.GeminiPart{
		ThoughtSignature: "signature-123",
		FunctionCall: &dto.FunctionCall{
			ID:           "call_123",
			FunctionName: "get_weather",
			Arguments:    map[string]interface{}{"city": "Taipei"},
		},
	}

	call := getResponseToolCall(&part)
	if call == nil {
		t.Fatal("expected tool call")
	}
	if call.ID != "call_123" {
		t.Fatalf("expected Gemini function id call_123, got %q", call.ID)
	}
	if call.ExtraContent == nil || call.ExtraContent.Google == nil {
		t.Fatal("expected google extra_content")
	}
	if got := call.ExtraContent.Google.ThoughtSignature; got != "signature-123" {
		t.Fatalf("expected thought signature signature-123, got %q", got)
	}
}

func TestConvertOpenAIToolHistoryPreservesGeminiIDAndThoughtSignature(t *testing.T) {
	assistant := dto.Message{Role: "assistant", Content: ""}
	assistant.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_123",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "get_weather",
				Arguments: `{"city":"Taipei"}`,
			},
			ExtraContent: &dto.ToolCallExtraContent{
				Google: &dto.GoogleToolCallExtraContent{
					ThoughtSignature: "signature-123",
				},
			},
		},
	})

	tool := dto.Message{
		Role:       "tool",
		Content:    `{"condition":"sunny"}`,
		ToolCallId: "call_123",
	}
	request := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "user", Content: "Check the weather."},
			assistant,
			tool,
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.6-flash"},
	}

	converted, err := CovertGemini2OpenAI(nil, request, info)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	if len(converted.Contents) != 3 {
		t.Fatalf("expected 3 Gemini contents, got %d", len(converted.Contents))
	}

	functionCallPart := converted.Contents[1].Parts[0]
	if functionCallPart.FunctionCall == nil {
		t.Fatal("expected function call part")
	}
	if got := functionCallPart.FunctionCall.ID; got != "call_123" {
		t.Fatalf("expected function call id call_123, got %q", got)
	}
	if got := functionCallPart.ThoughtSignature; got != "signature-123" {
		t.Fatalf("expected thought signature signature-123, got %q", got)
	}

	functionResponse := converted.Contents[2].Parts[0].FunctionResponse
	if functionResponse == nil {
		t.Fatal("expected function response part")
	}
	if got := functionResponse.ID; got != "call_123" {
		t.Fatalf("expected function response id call_123, got %q", got)
	}
	if got := functionResponse.Name; got != "get_weather" {
		t.Fatalf("expected function response name get_weather, got %q", got)
	}
}
