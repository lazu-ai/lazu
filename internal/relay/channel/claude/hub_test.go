package claude

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

func hubInfo(client textgen.ProtocolFamily, stream bool) *relaycommon.RelayInfo {
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: "claude-haiku-5-5", Upstream: "claude-haiku-5-5"}}
	return &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		IsStream:        stream,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "claude-haiku-5-5"},
		ProtocolRequest: request,
		ProtocolPlan: &textgen.RelayPlan{
			ClientProtocol:   textgen.Protocol{Family: client, Dialect: "openai"},
			UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolAnthropic, Dialect: "anthropic"},
		},
	}
}

func hubContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, recorder
}

func upstreamStream(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

// chunks are the data payloads of a Chat SSE reply.
func chunks(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

func TestHubStreamServesAChatClientFromARecordedMessagesStream(t *testing.T) {
	service.InitTokenEncoders()
	c, recorder := hubContext(t)
	info := hubInfo(textgen.ProtocolOpenAIChat, true)
	info.ShouldIncludeUsage = true
	usage, err := HubStreamHandler(c, upstreamStream(fixtures.Raw(t, "anthropic/text.sse")), info)
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens == 0 || usage.CompletionTokens == 0 || usage.Measurement().Input != dto.UsageSourceProvider {
		t.Fatalf("usage not taken from the provider: %+v", usage)
	}
	if info.FinishReason != "stop" || !strings.HasPrefix(info.UpstreamModelName, "claude-") {
		t.Fatalf("outcome not observed: finish=%q model=%q", info.FinishReason, info.UpstreamModelName)
	}
	var text strings.Builder
	var finish string
	for _, chunk := range chunks(t, recorder.Body.String()) {
		if gjson.Get(chunk, "object").Str != "chat.completion.chunk" {
			t.Fatalf("not a Chat chunk: %s", chunk)
		}
		text.WriteString(gjson.Get(chunk, "choices.0.delta.content").Str)
		if reason := gjson.Get(chunk, "choices.0.finish_reason"); reason.Exists() && reason.Str != "" {
			finish = reason.Str
		}
	}
	if text.Len() == 0 || finish != "stop" {
		t.Fatalf("client saw text %q finish %q", text.String(), finish)
	}
	if !strings.HasSuffix(strings.TrimSpace(recorder.Body.String()), "data: [DONE]") {
		t.Fatal("stream not closed with [DONE]")
	}
}

func TestHubStreamServesToolCallsAndReasoning(t *testing.T) {
	service.InitTokenEncoders()
	for _, tc := range []struct {
		fixture string
		check   func(t *testing.T, chunks []string)
	}{
		{"anthropic/tool_call.sse", func(t *testing.T, chunks []string) {
			var name, args string
			for _, chunk := range chunks {
				if n := gjson.Get(chunk, "choices.0.delta.tool_calls.0.function.name").Str; n != "" {
					name = n
				}
				args += gjson.Get(chunk, "choices.0.delta.tool_calls.0.function.arguments").Str
			}
			if name == "" || !json.Valid([]byte(args)) {
				t.Fatalf("tool call not relayed: name=%q args=%q", name, args)
			}
		}},
		{"anthropic/thinking_block.sse", func(t *testing.T, chunks []string) {
			var reasoning, text string
			for _, chunk := range chunks {
				reasoning += gjson.Get(chunk, "choices.0.delta.reasoning_content").Str
				text += gjson.Get(chunk, "choices.0.delta.content").Str
			}
			if reasoning == "" || text == "" {
				t.Fatalf("reasoning %q text %q", reasoning, text)
			}
		}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			c, recorder := hubContext(t)
			info := hubInfo(textgen.ProtocolOpenAIChat, true)
			if _, err := HubStreamHandler(c, upstreamStream(fixtures.Raw(t, tc.fixture)), info); err != nil {
				t.Fatal(err)
			}
			tc.check(t, chunks(t, recorder.Body.String()))
		})
	}
}

func TestHubStreamServesAResponsesClient(t *testing.T) {
	service.InitTokenEncoders()
	c, recorder := hubContext(t)
	info := hubInfo(textgen.ProtocolOpenAIResponses, true)
	if _, err := HubStreamHandler(c, upstreamStream(fixtures.Raw(t, "anthropic/tool_call.sse")), info); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: response.created") || !strings.Contains(body, "event: response.completed") {
		t.Fatalf("not a Responses stream:\n%s", body)
	}
	var sawCall bool
	for _, chunk := range chunks(t, body) {
		if gjson.Get(chunk, "type").Str == "response.output_item.done" && gjson.Get(chunk, "item.type").Str == "function_call" {
			sawCall = true
		}
	}
	if !sawCall {
		t.Fatalf("function_call item missing:\n%s", body)
	}
}

func TestHubStreamObservesWebSearchUsageAndUpstreamErrors(t *testing.T) {
	service.InitTokenEncoders()
	c, _ := hubContext(t)
	info := hubInfo(textgen.ProtocolOpenAIChat, true)
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-haiku-5-5-20260101\",\"content\":[],\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":30,\"server_tool_use\":{\"web_search_requests\":2}}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	usage, err := HubStreamHandler(c, upstreamStream(stream), info)
	if err != nil {
		t.Fatal(err)
	}
	if c.GetInt("claude_web_search_requests") != 2 {
		t.Fatalf("web search usage not captured: %d", c.GetInt("claude_web_search_requests"))
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 30 || info.UpstreamModelName != "claude-haiku-5-5-20260101" {
		t.Fatalf("usage %+v model %q", usage, info.UpstreamModelName)
	}

	// An in-band failure before any output is retryable and nothing is
	// written; after output it is written in the client's protocol and not
	// retried, as the Chat stream handler does.
	failure := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	c, recorder := hubContext(t)
	info = hubInfo(textgen.ProtocolOpenAIChat, true)
	if _, err := HubStreamHandler(c, upstreamStream(failure), info); err == nil || types.IsSkipRetryError(err) {
		t.Fatalf("a failure before any output must stay retryable: %v", err)
	}
	if strings.Contains(recorder.Body.String(), "Overloaded") {
		t.Fatalf("nothing may reach the client before a retry:\n%s", recorder.Body.String())
	}
	c, recorder = hubContext(t)
	info = hubInfo(textgen.ProtocolOpenAIChat, true)
	started := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-haiku-5-5\",\"content\":[],\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
	if _, err := HubStreamHandler(c, upstreamStream(started+failure), info); err == nil || !types.IsSkipRetryError(err) {
		t.Fatalf("a failure after output must not be retried: %v", err)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "partial") || !strings.Contains(body, "Overloaded") {
		t.Fatalf("output and the failure must both reach the client:\n%s", body)
	}
}

func TestHubServesAWholeReply(t *testing.T) {
	service.InitTokenEncoders()
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5-20260101","content":[{"type":"thinking","thinking":"hm","signature":"sig"},{"type":"text","text":"hello"},{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":300,"cache_read_input_tokens":5,"output_tokens_details":{"thinking_tokens":120},"server_tool_use":{"web_search_requests":1}}}`
	for _, client := range []textgen.ProtocolFamily{textgen.ProtocolOpenAIChat, textgen.ProtocolOpenAIResponses} {
		t.Run(string(client), func(t *testing.T) {
			c, recorder := hubContext(t)
			info := hubInfo(client, false)
			usage, err := HubHandler(c, upstreamStream(body), info)
			if err != nil {
				t.Fatal(err)
			}
			if usage.PromptTokens != 10 || usage.CompletionTokens != 300 || usage.CompletionTokenDetails.ReasoningTokens != 120 || usage.PromptTokensDetails.CachedTokens != 5 {
				t.Fatalf("usage %+v", usage)
			}
			if c.GetInt("claude_web_search_requests") != 1 || info.FinishReason != "tool_calls" {
				t.Fatalf("side effects: search=%d finish=%q", c.GetInt("claude_web_search_requests"), info.FinishReason)
			}
			reply := recorder.Body.String()
			switch client {
			case textgen.ProtocolOpenAIChat:
				if gjson.Get(reply, "choices.0.message.tool_calls.0.function.name").Str != "get_weather" || gjson.Get(reply, "choices.0.message.content").Str != "hello" || gjson.Get(reply, "choices.0.message.reasoning_content").Str != "hm" {
					t.Fatalf("chat reply: %s", reply)
				}
			case textgen.ProtocolOpenAIResponses:
				if gjson.Get(reply, "object").Str != "response" || !strings.Contains(reply, `"function_call"`) {
					t.Fatalf("responses reply: %s", reply)
				}
			}
		})
	}
}

// A stream that ends cleanly before the upstream said how the reply ended
// was cut off: retryable before any output, failed in the client's protocol
// after.
func TestHubStreamTreatsAStreamWithoutAStopAsCutOff(t *testing.T) {
	service.InitTokenEncoders()
	start := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-haiku-5-5\",\"content\":[],\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n\n"
	text := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
	c, recorder := hubContext(t)
	if _, err := HubStreamHandler(c, upstreamStream(start+text), hubInfo(textgen.ProtocolOpenAIChat, true)); err == nil || !types.IsSkipRetryError(err) {
		t.Fatalf("a cut-off stream after output must fail without retry: %v", err)
	}
	if body := recorder.Body.String(); strings.Contains(body, `"finish_reason":"stop"`) || !strings.Contains(body, "upstream stream ended") {
		t.Fatalf("a cut-off reply must not close as complete:\n%s", body)
	}
}
