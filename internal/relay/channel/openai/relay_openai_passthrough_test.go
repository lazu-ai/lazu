package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestOaiResponsesHandler_PreservesUnknownTopLevelFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	body := `{
		"id":"resp_1",
		"object":"response",
		"status":"completed",
		"model":"gpt-4.1-mini",
		"output":[],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},
		"x_future_response":{"enabled":true}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	usage, err := OaiResponsesHandler(c, &relaycommon.RelayInfo{}, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 2 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if got := usage.Measurement(); got.Input != dto.UsageSourceProvider || got.Output != dto.UsageSourceProvider {
		t.Fatalf("Responses provider provenance lost: %+v", got)
	}

	gotBody := recorder.Body.String()
	if !strings.Contains(gotBody, `"x_future_response"`) {
		t.Fatalf("expected unknown field to be preserved in response body, got: %s", gotBody)
	}
}

func TestOpenaiHandler_PreservesUnknownTopLevelFieldsWhenForceFormatDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	body := `{
		"id":"chatcmpl-1",
		"object":"chat.completion",
		"created":1,
		"model":"gpt-4o-mini",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},
		"x_future_response":{"enabled":true}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		PromptTokens: 9000, UsagePromptTokens: 1, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o-mini",
			ChannelSetting: dto.ChannelSettings{
				ForceFormat: false,
			},
		},
	}

	usage, err := OpenaiHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 2 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	gotBody := recorder.Body.String()
	if !strings.Contains(gotBody, `"x_future_response"`) {
		t.Fatalf("expected unknown field to be preserved in response body, got: %s", gotBody)
	}
}

func TestOpenaiHandler_NormalizesInputOutputUsageWithoutDroppingDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	body := `{
		"id":"chatcmpl-alias-usage",
		"object":"chat.completion",
		"created":1,
		"model":"gpt-4o-mini",
		"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
		"usage":{
			"input_tokens":100,
			"output_tokens":20,
			"total_tokens":120,
			"input_tokens_details":{"cached_tokens":40},
			"output_tokens_details":{"reasoning_tokens":10}
		}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		PromptTokens: 9000, UsagePromptTokens: 999, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5",
		},
	}

	usage, err := OpenaiHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 || usage.TotalTokens != 120 {
		t.Fatalf("unexpected normalized usage: %+v", usage)
	}
	if got := usage.Measurement(); got.Input != dto.UsageSourceProvider || got.Output != dto.UsageSourceProvider {
		t.Fatalf("Chat provider provenance lost: %+v", got)
	}
	if usage.PromptTokensDetails.CachedTokens != 40 || usage.CompletionTokenDetails.ReasoningTokens != 10 {
		t.Fatalf("expected usage details to survive normalization: %+v", usage)
	}
	if !strings.Contains(recorder.Body.String(), `"prompt_tokens":100`) {
		t.Fatalf("expected normalized usage in downstream response, got %s", recorder.Body.String())
	}
}

func TestOpenaiHandler_EstimatesToolCallTokensWhenUsageIsPartial(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	body := `{
		"id":"chatcmpl-tool-fallback",
		"object":"chat.completion",
		"created":1,
		"model":"gpt-5",
		"choices":[{
			"index":0,
			"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"id\":42}"}}]},
			"finish_reason":"tool_calls"
		}],
		"usage":{"prompt_tokens":10}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}

	usage, err := OpenaiHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.CompletionTokens <= 0 || usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("expected tool call fallback usage, got %+v", usage)
	}
	if got := usage.Measurement(); got.Input != dto.UsageSourceProvider || got.Output != dto.UsageSourceLocalEstimate {
		t.Fatalf("tool fallback provenance lost: %+v", got)
	}
}

func TestOpenaiHandlerWithUsage_DoesNotDoubleCanonicalAndAliasFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	body := `{
		"created":1,
		"data":[],
		"usage":{
			"prompt_tokens":100,
			"completion_tokens":20,
			"input_tokens":100,
			"output_tokens":20,
			"total_tokens":120,
			"input_tokens_details":{"image_tokens":30,"text_tokens":70}
		}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-image-1"},
	}

	usage, err := OpenaiHandlerWithUsage(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 || usage.TotalTokens != 120 {
		t.Fatalf("usage aliases must not be added twice: %+v", usage)
	}
	if usage.PromptTokensDetails.ImageTokens != 30 || usage.PromptTokensDetails.TextTokens != 70 {
		t.Fatalf("usage details must not be added twice: %+v", usage.PromptTokensDetails)
	}
}

func TestOaiStreamHandler_AcceptsInputOutputUsageAliases(t *testing.T) {
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
		`data: {"id":"chatcmpl-alias-stream","object":"chat.completion.chunk","created":1,"model":"gpt-5","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-alias-stream","object":"chat.completion.chunk","created":1,"model":"gpt-5","choices":[],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":40},"output_tokens_details":{"reasoning_tokens":10}}}`,
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
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"},
		UsagePromptTokens:  999, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
	}

	usage, err := OaiStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 || usage.TotalTokens != 120 {
		t.Fatalf("unexpected stream usage: %+v", usage)
	}
	if got := usage.Measurement(); got.Input != dto.UsageSourceProvider || got.Output != dto.UsageSourceProvider {
		t.Fatalf("stream provider provenance lost: %+v", got)
	}
	if usage.PromptTokensDetails.CachedTokens != 40 || usage.CompletionTokenDetails.ReasoningTokens != 10 {
		t.Fatalf("expected stream usage details to survive: %+v", usage)
	}
	if gotBody := recorder.Body.String(); !strings.Contains(gotBody, `"prompt_tokens":100`) || !strings.Contains(gotBody, `"completion_tokens":20`) {
		t.Fatalf("expected normalized canonical usage in forwarded stream chunk, got %s", gotBody)
	}
}

func TestOpenaiSTTHandler_AcceptsUsageWithoutTotalTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)

	body := `{"text":"hello","usage":{"input_tokens":30,"output_tokens":4,"input_tokens_details":{"audio_tokens":30}}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-transcribe"},
	}

	apiErr, usage := OpenaiSTTHandler(c, resp, info, "json")
	if apiErr != nil {
		t.Fatalf("expected nil error, got %v", apiErr)
	}
	if usage == nil || usage.PromptTokens != 30 || usage.CompletionTokens != 4 || usage.TotalTokens != 34 {
		t.Fatalf("unexpected transcription usage: %+v", usage)
	}
	if usage.PromptTokensDetails.AudioTokens != 30 {
		t.Fatalf("expected audio token details, got %+v", usage.PromptTokensDetails)
	}
}

func TestApplyUsagePostProcessingMapsPromptCacheHitTokens(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:         100,
		CompletionTokens:     20,
		PromptCacheHitTokens: 60,
	}

	applyUsagePostProcessing(&relaycommon.RelayInfo{}, usage, nil)

	if usage.PromptTokensDetails.CachedTokens != 60 {
		t.Fatalf("expected prompt cache hit tokens to map to cached tokens, got %d", usage.PromptTokensDetails.CachedTokens)
	}
	if usage.TotalTokens != 120 {
		t.Fatalf("expected total tokens to be repaired, got %d", usage.TotalTokens)
	}
}

func TestApplyUsagePostProcessingExtractsCachedTokensFromRawBody(t *testing.T) {
	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
	}
	body := []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_cache_hit_tokens":64}}`)

	applyUsagePostProcessing(&relaycommon.RelayInfo{}, usage, body)

	if usage.PromptTokensDetails.CachedTokens != 64 {
		t.Fatalf("expected cached tokens from raw body, got %d", usage.PromptTokensDetails.CachedTokens)
	}
}

func TestOaiResponsesHandler_ChatCompatConvertsToChatEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	body := `{
		"id":"resp_compat_1",
		"object":"response",
		"created_at":1700000000,
		"status":"completed",
		"model":"gpt-4.1-mini",
		"output":[
			{"type":"message","content":[{"type":"output_text","text":"compat text"}]}
		],
		"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4.1-mini",
		},
	}

	usage, err := OaiResponsesHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	gotBody := recorder.Body.String()
	if !strings.Contains(gotBody, `"id":"chatcmpl-compat_1"`) {
		t.Fatalf("Responses ID was not normalized for Chat: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"object":"chat.completion"`) {
		t.Fatalf("expected chat.completion envelope, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"compat text"`) {
		t.Fatalf("expected converted content in body, got: %s", gotBody)
	}
}

func TestOaiResponsesHandler_ChatCompatEstimatesMissingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitTokenEncoders()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := `{
		"id":"resp_missing_usage",
		"object":"response",
		"status":"completed",
		"model":"missing-usage-test-model",
		"output":[{"type":"message","status":"completed","content":[{"type":"output_text","text":"bill this completion"}]}]
	}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{
		PromptTokens: 9000, UsagePromptTokens: 11, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		ChannelMeta:  &relaycommon.ChannelMeta{UpstreamModelName: "missing-usage-test-model"},
	}
	usage, err := OaiResponsesHandler(c, info, resp)
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens != 11 || usage.CompletionTokens <= 0 || usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("missing Responses usage must be estimated for billing: %+v", usage)
	}
	if !strings.Contains(recorder.Body.String(), `"completion_tokens":`) {
		t.Fatalf("estimated usage should be returned in converted Chat response: %s", recorder.Body.String())
	}
}

func TestOaiResponsesHandler_CountsBillableWebSearchActionsOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {SearchContextSize: "low"},
			},
		},
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	bodyWithoutCall := `{
		"id":"resp_no_search",
		"object":"response",
		"status":"completed",
		"model":"gpt-4.1-mini",
		"tools":[{"type":"web_search_preview"}],
		"output":[{"type":"message","content":[{"type":"output_text","text":"no search"}]}],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(bodyWithoutCall)),
	}
	if _, err := OaiResponsesHandler(c, info, resp); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount; got != 0 {
		t.Fatalf("expected echoed tools not to count as calls, got %d", got)
	}

	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	bodyOpenPage := `{
		"id":"resp_open_page",
		"object":"response",
		"status":"completed",
		"model":"gpt-4.1-mini",
		"tools":[{"type":"web_search_preview"}],
		"output":[{"type":"web_search_call","status":"completed","action":{"type":"open_page","url":"https://example.com"}}],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`
	resp = &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(bodyOpenPage)),
	}
	if _, err := OaiResponsesHandler(c, info, resp); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount; got != 0 {
		t.Fatalf("expected open_page actions not to count as billable calls, got %d", got)
	}

	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	bodyWithCall := `{
		"id":"resp_search",
		"object":"response",
		"status":"completed",
		"model":"gpt-4.1-mini",
		"tools":[{"type":"web_search_preview"}],
		"output":[{"type":"web_search_call","status":"completed","action":{"type":"search","query":"latest news"}}],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`
	resp = &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(bodyWithCall)),
	}
	if _, err := OaiResponsesHandler(c, info, resp); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount; got != 1 {
		t.Fatalf("expected observed web_search_call to count once, got %d", got)
	}
}

func TestOaiResponsesHandler_AcceptsObjectToolChoice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	body := `{
		"id":"resp_tool_choice",
		"object":"response",
		"status":"completed",
		"model":"gpt-4.1-mini",
		"tool_choice":{"type":"web_search_preview"},
		"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],
		"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	usage, err := OaiResponsesHandler(c, &relaycommon.RelayInfo{}, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if !strings.Contains(recorder.Body.String(), `"tool_choice":{"type":"web_search_preview"}`) {
		t.Fatalf("expected object tool_choice to pass through, got: %s", recorder.Body.String())
	}
}

func TestOaiResponsesStreamHandler_CountsBillableWebSearchActionsOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed","action":{"type":"open_page","url":"https://example.com"}}}`,
		``,
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed","action":{"type":"find_in_page","pattern":"needle"}}}`,
		``,
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed","action":{"type":"search","query":"latest news"}}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_stream_search","created_at":1700000001,"status":"completed","model":"gpt-4.1-mini","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
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
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {SearchContextSize: "low"},
			},
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4.1-mini",
		},
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if got := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount; got != 1 {
		t.Fatalf("expected only search action to count as billable, got %d", got)
	}
}

func TestOaiResponsesStreamHandler_UsesUsageFromIncompleteTerminalEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_stream_incomplete","status":"in_progress","model":"gpt-5.6-luna"}}`,
		``,
		`data: {"type":"response.incomplete","response":{"id":"resp_stream_incomplete","status":"incomplete","model":"gpt-5.6-luna","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":7492,"input_tokens_details":{"cached_tokens":7489},"output_tokens":4096,"output_tokens_details":{"reasoning_tokens":4096},"total_tokens":11588}}}`,
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
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.6-luna",
		},
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.PromptTokens != 7492 || usage.CompletionTokens != 4096 || usage.TotalTokens != 11588 {
		t.Fatalf("unexpected usage from incomplete response: %+v", usage)
	}
	if usage.PromptTokensDetails.CachedTokens != 7489 {
		t.Fatalf("expected cached input token details to survive, got %+v", usage.PromptTokensDetails)
	}
	if usage.CompletionTokenDetails.ReasoningTokens != 4096 {
		t.Fatalf("expected reasoning output token details to survive, got %+v", usage.CompletionTokenDetails)
	}
	if !strings.Contains(recorder.Body.String(), `"type":"response.incomplete"`) {
		t.Fatalf("expected incomplete event to pass through, got: %s", recorder.Body.String())
	}
}

func TestOaiResponsesStreamHandler_ReconcilesPromptOnlyTotalAfterTextFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"fallback text"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_partial_total","status":"completed","model":"gpt-4o-mini","usage":{"input_tokens":20,"output_tokens":0,"total_tokens":20}}}`,
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
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.CompletionTokens <= 0 || usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("expected fallback completion tokens and reconciled total, got %+v", usage)
	}
}

func TestOaiResponsesStreamHandler_RetainsPromptUsageForEmptySuccessfulStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	streamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_empty","status":"completed","model":"gpt-4o-mini"}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		PromptTokens: 9000, UsagePromptTokens: 37, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o-mini",
		},
	}
	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.PromptTokens != 37 || usage.CompletionTokens != 0 || usage.TotalTokens != 37 {
		t.Fatalf("empty successful stream lost measured prompt usage: %+v", usage)
	}
}

func TestOaiResponsesStreamHandler_IncludesFunctionNameAndFramingInFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	const arguments = `{"city":"Paris"}`
	const functionName = "get_weather"
	streamBody := strings.Join([]string{
		`data: {"type":"response.function_call_arguments.delta","delta":"{\"city\":\"Paris\"}"}`,
		``,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_weather","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_tool_fallback","status":"completed","model":"gpt-4o-mini","usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`,
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
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	wantCompletion := service.CountTextToken(arguments+functionName, info.UpstreamModelName) + 7
	if usage.CompletionTokens != wantCompletion {
		t.Fatalf("expected arguments, function name, and framing tokens (%d), got %+v", wantCompletion, usage)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("expected reconciled total, got %+v", usage)
	}
}

func TestOaiResponsesStreamHandler_CountsArgumentsProvidedOnlyByCompletedItem(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	const arguments = `{"city":"Taipei","units":"metric"}`
	const functionName = "get_weather"
	streamBody := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_weather","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Taipei\",\"units\":\"metric\"}"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_tool_done_only","status":"completed","model":"gpt-4o-mini","usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"}}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	wantCompletion := service.CountTextToken(functionName+arguments, info.UpstreamModelName) + 7
	if usage.CompletionTokens != wantCompletion {
		t.Fatalf("expected completed-item arguments in fallback usage (%d), got %+v", wantCompletion, usage)
	}
}

func TestOaiResponsesStreamHandler_ChatCompatConvertsToChatChunks(t *testing.T) {
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
		`data: {"type":"response.created","response":{"id":"resp_stream_1","created_at":1700000001,"status":"in_progress","model":"gpt-4.1-mini"}}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		``,
		`data: {"type":"response.output_text.delta","delta":" "}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"world"}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"\n"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_stream_1","created_at":1700000001,"status":"completed","model":"gpt-4.1-mini","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
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
		RelayFormat:        types.RelayFormatOpenAI,
		ProtocolPlan:       &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		ShouldIncludeUsage: true,
		PromptTokens:       9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4.1-mini",
		},
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	gotBody := recorder.Body.String()
	if !strings.Contains(gotBody, `"id":"chatcmpl-stream_1"`) || strings.Contains(gotBody, `"id":"resp_stream_1"`) {
		t.Fatalf("streaming Responses ID was not normalized for Chat: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"object":"chat.completion.chunk"`) {
		t.Fatalf("expected chat completion chunks, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"content":"Hello"`) {
		t.Fatalf("expected text delta in stream output, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"content":" "`) {
		t.Fatalf("expected whitespace-only text delta in stream output, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"content":"\n"`) {
		t.Fatalf("expected newline-only text delta in stream output, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `data: [DONE]`) {
		t.Fatalf("expected [DONE] marker for chat stream compatibility, got: %s", gotBody)
	}
}

func TestOaiResponsesStreamHandler_ChatCompatDefersFallbackUsageUntilTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"billable text"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_no_usage","status":"completed","model":"gpt-4o-mini"}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		ProtocolPlan:       &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		ShouldIncludeUsage: true,
		PromptTokens:       9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}
	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatal(err)
	}
	if usage.CompletionTokens <= 0 {
		t.Fatalf("expected fallback completion usage, got %+v", usage)
	}
	body := recorder.Body.String()
	if strings.Contains(body, `"completion_tokens":0`) || !strings.Contains(body, `"prompt_tokens":2`) {
		t.Fatalf("terminal usage chunk was emitted before fallback estimation: %s", body)
	}
}

func TestOaiResponsesStreamHandler_ChatCompatAcceptsObjectToolChoice(t *testing.T) {
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
		`data: {"type":"response.output_text.delta","delta":"ok"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_stream_tool_choice","created_at":1700000001,"status":"completed","model":"gpt-4.1-mini","tool_choice":{"type":"web_search_preview"},"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
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
		RelayFormat:        types.RelayFormatOpenAI,
		ProtocolPlan:       &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		ShouldIncludeUsage: true,
		PromptTokens:       9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4.1-mini",
		},
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	gotBody := recorder.Body.String()
	if !strings.Contains(gotBody, `"finish_reason":"stop"`) {
		t.Fatalf("expected completion event to produce stop chunk, got: %s", gotBody)
	}
}

func TestOaiResponsesStreamHandler_ChatCompatConvertsFunctionCallChunk(t *testing.T) {
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
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_weather","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_stream_tool_1","created_at":1700000001,"status":"completed","model":"gpt-4.1-mini","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
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
		RelayFormat:        types.RelayFormatOpenAI,
		ProtocolPlan:       &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		ShouldIncludeUsage: true,
		PromptTokens:       9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4.1-mini",
		},
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	gotBody := recorder.Body.String()
	if !strings.Contains(gotBody, `"tool_calls"`) || !strings.Contains(gotBody, `"id":"call_weather"`) {
		t.Fatalf("expected tool call chunk, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"finish_reason":"tool_calls"`) {
		t.Fatalf("expected tool_calls finish reason, got: %s", gotBody)
	}
}

func TestOaiResponsesStreamHandler_ChatCompatPreservesLengthTruncatedFunctionCall(t *testing.T) {
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
		`data: {"type":"response.output_item.done","item":{"type":"function_call","status":"incomplete","id":"fc_weather","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Par"}}`,
		``,
		`data: {"type":"response.incomplete","response":{"id":"resp_stream_tool_incomplete","created_at":1700000001,"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"model":"gpt-4.1-mini","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
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
		RelayFormat:        types.RelayFormatOpenAI,
		ProtocolPlan:       &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		ShouldIncludeUsage: true,
		PromptTokens:       9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4.1-mini",
		},
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	gotBody := recorder.Body.String()
	if !strings.Contains(gotBody, `"tool_calls"`) || !strings.Contains(gotBody, `"id":"call_weather"`) || !strings.Contains(gotBody, `{\"city\":\"Par`) {
		t.Fatalf("length-truncated tool call was not preserved, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"finish_reason":"length"`) {
		t.Fatalf("expected length finish reason, got: %s", gotBody)
	}
}

func TestOaiResponsesStreamHandler_ChatCompatKeepsCompletedCallInIncompleteResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	streamBody := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"function_call","status":"completed","id":"fc_weather","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Taipei\"}"}}`,
		``,
		`data: {"type":"response.incomplete","response":{"id":"resp_partial","status":"incomplete","model":"gpt-4.1-mini","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1-mini"},
	}
	if _, err := OaiResponsesStreamHandler(c, info, resp); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"id":"call_weather"`) || !strings.Contains(body, `"finish_reason":"length"`) {
		t.Fatalf("completed call and incomplete terminal must both survive: %s", body)
	}
}

func TestOaiResponsesStreamHandler_ChatCompatPreservesContentFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	streamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_filtered","status":"incomplete","incomplete_details":{"reason":"content_filter"},"model":"gpt-4.1-mini","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1-mini"},
	}
	if _, err := OaiResponsesStreamHandler(c, info, resp); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"finish_reason":"content_filter"`) {
		t.Fatalf("expected content_filter finish reason, got: %s", body)
	}
}

func TestOaiResponsesStreamHandler_ChatCompatPreservesRefusalDelta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamBody := strings.Join([]string{
		`data: {"type":"response.refusal.delta","delta":"not allowed"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_refusal","status":"completed","model":"gpt-4.1-mini","usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1-mini"},
	}
	if _, err := OaiResponsesStreamHandler(c, info, resp); err != nil {
		t.Fatal(err)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"refusal":"not allowed"`) {
		t.Fatalf("refusal delta was lost: %s", body)
	}
}

// A response.failed before any output reached the client is a pre-stream
// failure: retryable, nothing written, classified by what the upstream said
// (as on the native Responses path and the other protocol hubs).
func TestOaiResponsesStreamHandler_ChatCompatFailureBeforeOutputIsRetryable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamBody := strings.Join([]string{
		`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","model":"gpt-4.1-mini","error":{"type":"server_error","code":"overloaded","message":"upstream overloaded"},"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1-mini"},
	}
	_, lazuErr := OaiResponsesStreamHandler(c, info, resp)
	if lazuErr == nil || types.IsSkipRetryError(lazuErr) {
		t.Fatalf("a failure before output must be a retryable error: %+v", lazuErr)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("nothing may reach the client before a retry: %s", recorder.Body.String())
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) {
		t.Fatal("the attempt must be marked as failed on its first event")
	}
}

// A response.failed after output is written as one catalogued error and one
// DONE, settled from the captured usage, and not replayed.
func TestOaiResponsesStreamHandler_ChatCompatPropagatesFailedTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		``,
		`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","model":"gpt-4.1-mini","error":{"type":"server_error","code":"overloaded","message":"upstream overloaded"},"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1-mini"},
	}
	usage, lazuErr := OaiResponsesStreamHandler(c, info, resp)
	if lazuErr == nil || lazuErr.GetErrorCode() != types.ErrorCodeUpstreamError || lazuErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("failed terminal was not propagated as an upstream failure: %+v", lazuErr)
	}
	if usage == nil || usage.PromptTokens != 2 {
		t.Fatalf("failed terminal usage was lost: %+v", usage)
	}
	body := recorder.Body.String()
	if strings.Count(body, `"code":"upstream_overloaded"`) != 1 || strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("failed terminal must emit one error and one DONE: %s", body)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamTerminal) {
		t.Fatal("failed terminal must suppress a second outer error frame")
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatal("failed terminal with captured usage must request quota settlement")
	}
}

func TestOaiResponsesStreamHandler_ChatCompatClassifiesTopLevelErrorEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"billable partial"}`,
		``,
		`data: {"type":"error","error":{"type":"server_error","code":"overloaded","message":"capacity exhausted"}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		PromptTokens: 9000, UsagePromptTokens: 3, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1-mini"},
	}
	usage, lazuErr := OaiResponsesStreamHandler(c, info, resp)
	if lazuErr == nil || lazuErr.GetErrorCode() != types.ErrorCodeUpstreamError || lazuErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("top-level error event was not propagated as an upstream failure: %+v", lazuErr)
	}
	if usage == nil || usage.PromptTokens != 3 || usage.CompletionTokens <= 0 {
		t.Fatalf("top-level error lost observed usage: %+v", usage)
	}
	body := recorder.Body.String()
	for _, fragment := range []string{`"type":"overloaded_error"`, `"code":"upstream_overloaded"`, `"lazu":"LZ-6202"`, `"message":"capacity exhausted"`} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("top-level error field %s was not preserved: %s", fragment, body)
		}
	}
	if strings.Contains(body, "responses stream ended without a terminal event") || strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("top-level error was replaced or terminated twice: %s", body)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamTerminal) || !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatal("top-level error must mark terminal delivery and settle observed usage")
	}
}

func TestOaiResponsesStreamHandler_ChatCompatFailsWithoutTerminalEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1-mini"},
	}
	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err == nil {
		t.Fatal("expected truncated Responses stream error")
	}
	if usage == nil || usage.PromptTokens != 2 || usage.CompletionTokens <= 0 {
		t.Fatalf("truncated stream must settle observed output: %+v", usage)
	}
	body := recorder.Body.String()
	if strings.Count(body, `"code":"upstream_stream_failed"`) != 1 || strings.Count(body, "data: [DONE]") != 1 {
		t.Fatalf("expected exactly one error and terminal marker, got: %s", body)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamTerminal) {
		t.Fatal("expected emitted stream error marker")
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatal("truncated bridged stream must request observed-usage settlement")
	}
}

func TestOaiResponsesStreamHandler_ChatCompatSettlesObservedUsageBeforeConversionFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	streamBody := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"billable partial"}`,
		``,
		`data: {not-json}`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayFormat:  types.RelayFormatOpenAI,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
		PromptTokens: 9000, UsagePromptTokens: 3, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1-mini"},
	}
	usage, err := OaiResponsesStreamHandler(c, info, resp)
	if err == nil || err.GetErrorCode() != types.ErrorCodeConvertRequestFailed {
		t.Fatalf("expected conversion failure, got %v", err)
	}
	if usage == nil || usage.PromptTokens != 3 || usage.CompletionTokens <= 0 {
		t.Fatalf("conversion failure must retain observed usage: %+v", usage)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatal("conversion failure must request observed-usage settlement")
	}
}

func TestOaiChatStreamToResponsesHandlerEstimatesChatChunksWithoutUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	streamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_bridge","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"billable text"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_bridge","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayMode:    relayconstant.RelayModeResponses,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}
	usage, err := OaiChatStreamToResponsesHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.CompletionTokens <= 0 {
		t.Fatalf("expected fallback completion usage from Chat chunks, got %+v", usage)
	}
	body := recorder.Body.String()
	for _, eventType := range []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.content_part.done", "response.output_item.done", "response.completed"} {
		if !strings.Contains(body, "event: "+eventType) {
			t.Fatalf("missing named Responses SSE event %q: %s", eventType, body)
		}
	}
	if !strings.Contains(body, `"input_tokens":2`) || !strings.Contains(body, `"output_tokens":`) {
		t.Fatalf("terminal Responses usage fields were not normalized: %s", body)
	}
}

func TestOaiChatStreamToResponsesHandlerPreservesUpstreamErrorFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	streamBody := strings.Join([]string{
		`data: {"error":{"message":"provider overloaded","type":"server_error","code":"overloaded"}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayMode:    relayconstant.RelayModeResponses,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}
	_, err := OaiChatStreamToResponsesHandler(c, info, resp)
	if err == nil || err.GetErrorCode() != types.ErrorCode("overloaded") {
		t.Fatalf("expected preserved upstream error, got %v", err)
	}
	body := recorder.Body.String()
	for _, fragment := range []string{`"type":"response.failed"`, `"type":"server_error"`, `"code":"overloaded"`, `"message":"provider overloaded"`} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("upstream error field %s was not preserved: %s", fragment, body)
		}
	}
	eligible, failed := types.ChannelHealthVerdict(http.StatusOK, err.GetErrorCode(), true)
	if !eligible || !failed {
		t.Fatalf("provider error frame must count as channel-health failure: eligible=%t failed=%t", eligible, failed)
	}
}

func TestOaiChatStreamToResponsesHandlerFailsTruncatedStreamOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	streamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_truncated","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayMode:    relayconstant.RelayModeResponses,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}},
		PromptTokens: 9000, UsagePromptTokens: 2, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}
	if _, err := OaiChatStreamToResponsesHandler(c, info, resp); err == nil {
		t.Fatal("expected truncated stream error")
	}
	body := recorder.Body.String()
	if strings.Count(body, `"type":"response.failed"`) != 1 {
		t.Fatalf("expected exactly one response.failed event, got: %s", body)
	}
	if strings.Count(body, "event: response.failed") != 1 {
		t.Fatalf("expected exactly one named response.failed event, got: %s", body)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamTerminal) {
		t.Fatal("expected emitted stream error marker")
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatal("failed Responses envelope must request quota settlement")
	}
}

func TestOaiChatStreamToResponsesHandlerSettlesObservedUsageBeforeConversionFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	streamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_partial","object":"chat.completion.chunk","model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"billable partial"},"finish_reason":null}]}`,
		``,
		`data: {not-json}`,
		``,
	}, "\n")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamBody))}
	info := &relaycommon.RelayInfo{
		RelayMode:    relayconstant.RelayModeResponses,
		ProtocolPlan: &textgen.RelayPlan{ClientProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}},
		PromptTokens: 9000, UsagePromptTokens: 4, UsagePromptTokensSet: true, UsagePromptTokensAvailable: true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}
	usage, err := OaiChatStreamToResponsesHandler(c, info, resp)
	if err == nil || err.GetErrorCode() != types.ErrorCodeConvertRequestFailed {
		t.Fatalf("expected conversion failure, got %v", err)
	}
	if usage == nil || usage.PromptTokens != 4 || usage.CompletionTokens <= 0 {
		t.Fatalf("conversion failure must retain observed Chat usage: %+v", usage)
	}
	if !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatal("conversion failure must request observed-usage settlement")
	}
	if strings.Count(recorder.Body.String(), `"type":"response.failed"`) != 1 {
		t.Fatalf("conversion failure must emit one terminal Responses event: %s", recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"usage":{"input_tokens":4`) || strings.Contains(body, `"prompt_tokens"`) {
		t.Fatalf("failed Responses envelope must expose settled Responses usage only: %s", body)
	}
}

type canceledBodyReader struct{}

func (canceledBodyReader) Read([]byte) (int, error) { return 0, context.Canceled }

func TestNonStreamHandlersClassifyCancellationAfterUpstreamHeaders(t *testing.T) {
	handlers := map[string]func(*gin.Context, *relaycommon.RelayInfo, *http.Response) (*dto.Usage, *types.LazuError){
		"chat": OpenaiHandler, "responses": OaiResponsesHandler, "image": OpenaiHandlerWithUsage,
	}
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(canceledBodyReader{})}
			usage, err := handler(c, &relaycommon.RelayInfo{}, resp)
			if usage != nil || err == nil || err.GetErrorCode() != types.ErrorCodeClientCanceled || err.StatusCode != 499 || !types.IsSkipRetryError(err) {
				t.Fatalf("usage=%v err=%v", usage, err)
			}
		})
	}
}
