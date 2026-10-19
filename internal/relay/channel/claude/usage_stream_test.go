package claude

import (
	"fmt"
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
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

func TestLegacyClaudeCompletionUsageDoesNotUseAdmission(t *testing.T) {
	service.InitTokenEncoders()
	oldCount, oldNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	t.Cleanup(func() { constant.GetMediaToken, constant.GetMediaTokenNotStream = oldCount, oldNonStream })
	for _, stream := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		info := &relaycommon.RelayInfo{PromptTokens: 9000, OriginModelName: "gpt-4o-mini", RelayFormat: types.RelayFormatOpenAI,
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}, Request: &dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "hello"}}}}
		state := &ClaudeResponseInfo{Usage: &dto.Usage{}}
		if stream {
			state.ResponseText.WriteString("hello world")
			HandleStreamFinalResponse(c, info, state, RequestModeCompletion)
		} else {
			err := HandleClaudeResponseData(c, info, state, &http.Response{StatusCode: 200, Header: make(http.Header)}, []byte(`{"completion":"hello world","stop_reason":"stop_sequence"}`), RequestModeCompletion)
			if err != nil {
				t.Fatal(err)
			}
		}
		if state.Usage.PromptTokens <= 0 || state.Usage.PromptTokens == 9000 || state.Usage.CompletionTokens <= 0 || state.Usage.TotalTokens != state.Usage.PromptTokens+state.Usage.CompletionTokens {
			t.Fatalf("incorrect legacy usage: %+v", state.Usage)
		}
		if state.Usage.Measurement().Input != dto.UsageSourceLocalEstimate || state.Usage.Measurement().Output != dto.UsageSourceLocalEstimate {
			t.Fatalf("unmarked legacy estimate: %+v", state.Usage)
		}
	}
}

func TestHandleStreamResponseDataCapturesClaudeWebSearchUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude}
	claudeInfo := &ClaudeResponseInfo{
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	data := `{
		"type":"message_delta",
		"delta":{"stop_reason":"end_turn"},
		"usage":{"output_tokens":3,"server_tool_use":{"web_search_requests":2}}
	}`

	if err := HandleStreamResponseData(c, info, claudeInfo, data, RequestModeMessage); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got := c.GetInt("claude_web_search_requests"); got != 2 {
		t.Fatalf("expected two billable web searches from stream usage, got %d", got)
	}
	if got := claudeInfo.Usage.Measurement(); got.Output != dto.UsageSourceProvider || got.Input != dto.UsageSourceUnavailable {
		t.Fatalf("partial provider provenance lost: %+v", got)
	}
}

// Thinking is billed inside output_tokens, and with the summary omitted the
// usage detail is the only record of it. It lands in the shared reasoning
// field, not on top of the billed output.
func TestClaudeThinkingTokensBecomeReasoningTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude}
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	data := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},
		"usage":{"input_tokens":47,"output_tokens":1850,"output_tokens_details":{"thinking_tokens":855}}}`
	if err := HandleStreamResponseData(c, info, claudeInfo, data, RequestModeMessage); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if claudeInfo.Usage.CompletionTokens != 1850 || claudeInfo.Usage.CompletionTokenDetails.ReasoningTokens != 855 {
		t.Fatalf("usage = %d output / %d reasoning, want 1850 / 855",
			claudeInfo.Usage.CompletionTokens, claudeInfo.Usage.CompletionTokenDetails.ReasoningTokens)
	}

	whole := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],
		"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":300,"output_tokens_details":{"thinking_tokens":120}}}`)
	if err := HandleClaudeResponseData(c, info, whole, &http.Response{StatusCode: 200, Header: make(http.Header)}, body, RequestModeMessage); err != nil {
		t.Fatal(err)
	}
	if whole.Usage.CompletionTokens != 300 || whole.Usage.CompletionTokenDetails.ReasoningTokens != 120 {
		t.Fatalf("non-stream usage = %d / %d, want 300 / 120",
			whole.Usage.CompletionTokens, whole.Usage.CompletionTokenDetails.ReasoningTokens)
	}
}

// Stop after real SSE events without a terminal message_delta.
type interruptedClaudeReader struct{}

func (interruptedClaudeReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestInterruptedClaudeStreamPreservesCacheSeparatedInput(t *testing.T) {
	service.InitTokenEncoders()
	for _, tc := range []struct {
		name, providerUsage          string
		input, cacheRead, cacheWrite int
		source                       dto.UsageSource
	}{
		{"cache read only", `{"input_tokens":0,"output_tokens":1,"cache_read_input_tokens":100}`, 0, 100, 0, dto.UsageSourceProvider},
		{"cache write only", `{"input_tokens":0,"output_tokens":1,"cache_creation_input_tokens":100}`, 0, 0, 100, dto.UsageSourceProvider},
		{"both caches", `{"input_tokens":0,"output_tokens":1,"cache_read_input_tokens":60,"cache_creation_input_tokens":40}`, 0, 60, 40, dto.UsageSourceProvider},
		{"mixed uncached and cached", `{"input_tokens":7,"output_tokens":1,"cache_read_input_tokens":100}`, 7, 100, 0, dto.UsageSourceProvider},
		{"explicit zero without cache", `{"input_tokens":0,"output_tokens":1}`, 0, 0, 0, dto.UsageSourceProvider},
		{"missing usage", `null`, 120, 0, 0, dto.UsageSourceLocalEstimate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			info := &relaycommon.RelayInfo{PromptTokens: 9000, UsagePromptTokens: 120, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
				RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}
			events := fmt.Sprintf("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"model\":\"gpt-4o-mini\",\"usage\":%s}}\n\n", tc.providerUsage)
			events += "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello world\"}}\n\n"
			body := io.NopCloser(io.MultiReader(strings.NewReader(events), interruptedClaudeReader{}))
			got, err := ClaudeStreamHandler(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, info, RequestModeMessage)
			if err == nil || !types.IsSkipRetryError(err) {
				t.Fatalf("interruption must fail without retry: %v", err)
			}
			if got.PromptTokens != tc.input || got.Measurement().Input != tc.source || got.CompletionTokens <= 0 {
				t.Fatalf("incorrect interrupted usage: %+v", got)
			}
			fact := usagepkg.FactFromProviderUsage(got, nil, usagepkg.ProviderFamilyAnthropic)
			if fact.InputTokens != tc.input || fact.Measurement.Input != tc.source {
				t.Fatalf("wrong persisted input: %+v", fact)
			}
			dimensions := make(map[string]int64)
			for _, d := range fact.Dimensions {
				dimensions[d.Name] += d.Quantity
			}
			if dimensions[usagepkg.DimensionInput] != int64(tc.input) || dimensions[usagepkg.DimensionCacheRead] != int64(tc.cacheRead) || dimensions[usagepkg.DimensionCacheWrite] != int64(tc.cacheWrite) {
				t.Fatalf("cache-separated fact counted prompt incorrectly: %+v", fact)
			}
		})
	}
}

func TestClaudeStreamUsageBillsInputBesideCache(t *testing.T) {
	claudeInfo := &ClaudeResponseInfo{ResponseText: strings.Builder{}, Usage: &dto.Usage{}}
	var start dto.ClaudeResponse
	if err := common.UnmarshalJsonStr(`{"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-5",
		"usage":{"input_tokens":1840,"output_tokens":1,"cache_read_input_tokens":96512,"cache_creation_input_tokens":8230}}}`, &start); err != nil {
		t.Fatal(err)
	}
	FormatClaudeResponseInfo(RequestModeMessage, &start, nil, claudeInfo)
	var end dto.ClaudeResponse
	if err := common.UnmarshalJsonStr(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1960}}`, &end); err != nil {
		t.Fatal(err)
	}
	FormatClaudeResponseInfo(RequestModeMessage, &end, nil, claudeInfo)

	got := usagepkg.BillingUsageFromSummary(usagepkg.SummaryFromDTO(claudeInfo.Usage))
	if got.InputTokens != 1_840 || got.CacheReadTokens != 96_512 || got.CacheWrite5mTokens != 8_230 || got.OutputTokens != 1_960 {
		t.Fatalf("claude stream usage billed as %+v; want input 1840 beside cache read 96512 and write 8230", got)
	}
}
