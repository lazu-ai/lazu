package openai

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestConvertGeminiRequest_RejectsCurrentTurnThoughtSignature(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
	}
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{
						Text: "北京的天气怎么样，现在几点了？",
					},
				},
			},
			{
				Role: "model",
				Parts: []dto.GeminiPart{
					{
						ThoughtSignature: "sig-123",
						FunctionCall: &dto.FunctionCall{
							FunctionName: "get_weather",
							Arguments:    map[string]any{"city": "Beijing"},
						},
					},
				},
			},
		},
	}

	_, err := adaptor.ConvertGeminiRequest(nil, info, request)
	if err == nil {
		t.Fatal("expected ConvertGeminiRequest to reject current-turn thoughtSignature")
	}
	if !strings.Contains(err.Error(), "latest user turn") {
		t.Fatalf("expected latest user turn error, got %v", err)
	}
}

func TestConvertGeminiRequest_AllowsCurrentTurnToolLoopWithoutThoughtSignature(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
	}
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{
						Text: "北京的天气怎么样？",
					},
				},
			},
			{
				Role: "model",
				Parts: []dto.GeminiPart{
					{
						FunctionCall: &dto.FunctionCall{
							FunctionName: "get_weather",
							Arguments:    map[string]any{"city": "Beijing"},
						},
					},
				},
			},
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{
						FunctionResponse: &dto.GeminiFunctionResponse{
							Name: "get_weather",
							Response: map[string]any{
								"result": map[string]any{"temperature": 26},
							},
						},
					},
				},
			},
		},
	}

	converted, err := adaptor.ConvertGeminiRequest(nil, info, request)
	if err != nil {
		t.Fatalf("expected current-turn tool loop without thoughtSignature to be relayable, got %v", err)
	}
	if _, ok := converted.(*dto.GeneralOpenAIRequest); !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", converted)
	}
}

func TestConvertGeminiRequest_AllowsHistoricalNativeToolLoop(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
	}
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Role: "model",
				Parts: []dto.GeminiPart{
					{
						ThoughtSignature: "sig-123",
						FunctionCall: &dto.FunctionCall{
							FunctionName: "get_weather",
							Arguments:    map[string]any{"city": "Beijing"},
						},
					},
				},
			},
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{
						FunctionResponse: &dto.GeminiFunctionResponse{
							Name: "get_weather",
							Response: map[string]any{
								"result": map[string]any{"temperature": 26},
							},
						},
					},
				},
			},
			{
				Role: "model",
				Parts: []dto.GeminiPart{
					{
						Text: "北京现在晴，26 度。",
					},
				},
			},
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{
						Text: "那上海呢？",
					},
				},
			},
		},
	}

	converted, err := adaptor.ConvertGeminiRequest(nil, info, request)
	if err != nil {
		t.Fatalf("expected historical tool loop to be relayable, got %v", err)
	}
	if _, ok := converted.(*dto.GeneralOpenAIRequest); !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", converted)
	}
}

func TestConvertGeminiRequest_AllowsHistoricalThoughtSignatureWithoutToolUse(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o",
		},
	}
	request := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{
						Text: "先想一想这个问题。",
					},
				},
			},
			{
				Role: "model",
				Parts: []dto.GeminiPart{
					{
						ThoughtSignature: "sig-thinking-only",
						Text:             "这是历史回答。",
					},
				},
			},
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{
						Text: "继续下一题。",
					},
				},
			},
		},
	}

	converted, err := adaptor.ConvertGeminiRequest(nil, info, request)
	if err != nil {
		t.Fatalf("expected historical thoughtSignature without tool use to be relayable, got %v", err)
	}
	if _, ok := converted.(*dto.GeneralOpenAIRequest); !ok {
		t.Fatalf("expected *dto.GeneralOpenAIRequest, got %T", converted)
	}
}
