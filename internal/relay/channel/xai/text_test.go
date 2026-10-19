package xai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestNormalizeXAIUsagePreservesAliasesAndDetails(t *testing.T) {
	usage := &dto.Usage{
		InputTokens:  100,
		OutputTokens: 20,
		InputTokensDetails: &dto.InputTokenDetails{
			CachedTokens: 40,
		},
		OutputTokensDetails: &dto.OutputTokenDetails{
			ReasoningTokens: 10,
		},
	}

	normalizeXAIUsage(usage, 999)

	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 || usage.TotalTokens != 120 {
		t.Fatalf("unexpected normalized usage: %+v", usage)
	}
	if usage.PromptTokensDetails.CachedTokens != 40 || usage.CompletionTokenDetails.ReasoningTokens != 10 {
		t.Fatalf("expected usage details to survive: %+v", usage)
	}
	if usage.CompletionTokenDetails.TextTokens != 10 {
		t.Fatalf("expected non-reasoning output tokens to be classified as text: %+v", usage.CompletionTokenDetails)
	}
}

func TestXAIStreamHandlerPreservesProviderUsageDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"id":"chatcmpl-xai","object":"chat.completion.chunk","created":1,"model":"grok-test","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		``,
		`data: {"id":"chatcmpl-xai","object":"chat.completion.chunk","created":1,"model":"grok-test","choices":[],"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":40},"output_tokens_details":{"reasoning_tokens":10}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-test"},
	}

	usage, err := xAIStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 || usage.TotalTokens != 120 {
		t.Fatalf("unexpected stream usage: %+v", usage)
	}
	if usage.PromptTokensDetails.CachedTokens != 40 || usage.CompletionTokenDetails.ReasoningTokens != 10 {
		t.Fatalf("expected stream usage details to survive: %+v", usage)
	}
}

func TestXAIMissingAndPartialUsage(t *testing.T) {
	service.InitTokenEncoders()
	oldCount, oldNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	t.Cleanup(func() { constant.GetMediaToken, constant.GetMediaTokenNotStream = oldCount, oldNonStream })
	for _, tc := range []struct {
		name, body                                string
		stream, wantError, providerInput, noUsage bool
	}{
		{"json missing", `{"choices":[{"message":{"role":"assistant","content":"hello world"}}]}`, false, false, false, false},
		{"json partial", `{"choices":[{"message":{"role":"assistant","content":"hello world"}}],"usage":{"prompt_tokens":23}}`, false, false, true, false},
		{"json failed", `{"error":{"type":"server_error","message":"unavailable","code":"server_error"}}`, false, true, false, true},
		{"stream tools", "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"weather\",\"arguments\":\"{}\"}}]}}]}\n\ndata: [DONE]\n\n", true, false, false, false},
		{"stream partial then error", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"error\":{\"type\":\"server_error\",\"message\":\"unavailable\",\"code\":\"server_error\"}}\n\n", true, true, false, false},
		{"stream error only", "data: {\"error\":{\"type\":\"server_error\",\"message\":\"unavailable\",\"code\":\"server_error\"}}\n\n", true, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{PromptTokens: 9000, OriginModelName: "gpt-4o-mini", RelayFormat: types.RelayFormatOpenAI,
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}, Request: &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}}}
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}
			var usage *dto.Usage
			var relayErr *types.LazuError
			if tc.stream {
				usage, relayErr = xAIStreamHandler(c, info, resp)
			} else {
				usage, relayErr = xAIHandler(c, info, resp)
			}
			if (relayErr != nil) != tc.wantError {
				t.Fatalf("error=%v wantError=%t", relayErr, tc.wantError)
			}
			if tc.noUsage {
				if service.ValidUsage(usage) || common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
					t.Fatalf("failed empty response became billable: %+v", usage)
				}
				return
			}
			if usage.PromptTokens <= 0 || usage.PromptTokens == 9000 || usage.CompletionTokens <= 0 || usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
				t.Fatalf("incorrect fallback: %+v", usage)
			}
			if tc.providerInput {
				if usage.PromptTokens != 23 || usage.Measurement().Input != dto.UsageSourceProvider || info.UsagePromptTokensSet {
					t.Fatalf("replaced provider input: %+v", usage)
				}
			} else if usage.Measurement().Input != dto.UsageSourceLocalEstimate {
				t.Fatalf("missing input provenance: %+v", usage)
			}
			if usage.Measurement().Output != dto.UsageSourceLocalEstimate {
				t.Fatalf("missing output provenance: %+v", usage)
			}
			if tc.wantError && !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
				t.Fatal("partial output lost settlement signal")
			}
			if info.PromptTokens != 9000 {
				t.Fatal("admission changed")
			}
		})
	}
}
