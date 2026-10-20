package openai

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

func claudeClientInfo(upstream textgen.ProtocolFamily, stream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		IsStream:        stream,
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-flash"},
		ProtocolRequest: &textgen.Request{Model: textgen.ModelRef{Canonical: "deepseek-flash", Upstream: "deepseek-flash"}},
		ProtocolPlan: &textgen.RelayPlan{
			ClientProtocol:   textgen.Protocol{Family: textgen.ProtocolAnthropic, Dialect: "anthropic"},
			UpstreamProtocol: textgen.Protocol{Family: upstream, Dialect: "deepseek"},
		},
	}
}

func claudeClientContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, recorder
}

func upstreamBody(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

// sseFrames are a Messages stream's frames: event name and data.
func sseFrames(body string) [][2]string {
	var out [][2]string
	var event string
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			out = append(out, [2]string{event, strings.TrimPrefix(line, "data: ")})
		}
	}
	return out
}

// A recorded DeepSeek stream, Chat or Responses, reaches a Claude client as
// a well-formed Messages stream with its text, tool call and usage.
func TestClaudeClientHubServesRecordedDeepSeekStreams(t *testing.T) {
	service.InitTokenEncoders()
	for _, tc := range []struct {
		fixture  string
		upstream textgen.ProtocolFamily
		tool     bool
	}{
		{"deepseek/chat_text.sse", textgen.ProtocolOpenAIChat, false},
		{"deepseek/chat_tool_call.sse", textgen.ProtocolOpenAIChat, true},
		{"deepseek/responses_text.sse", textgen.ProtocolOpenAIResponses, false},
		{"deepseek/responses_tool_call.sse", textgen.ProtocolOpenAIResponses, true},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			c, recorder := claudeClientContext()
			info := claudeClientInfo(tc.upstream, true)
			usage, err := ClaudeClientStreamHandler(c, upstreamBody(fixtures.Raw(t, tc.fixture)), info)
			if err != nil {
				t.Fatal(err)
			}
			if usage.CompletionTokens == 0 || usage.Measurement().Output != dto.UsageSourceProvider {
				t.Fatalf("usage not the provider's: %+v", usage)
			}
			frames := sseFrames(recorder.Body.String())
			if len(frames) < 3 || frames[0][0] != "message_start" || frames[len(frames)-1][0] != "message_stop" {
				t.Fatalf("not a Messages stream: %v", frames)
			}
			var text, args, stop string
			for _, frame := range frames {
				if gjson.Get(frame[1], "type").String() != frame[0] {
					t.Fatalf("event name and data type differ: %v", frame)
				}
				text += gjson.Get(frame[1], "delta.text").String()
				args += gjson.Get(frame[1], "delta.partial_json").String()
				if reason := gjson.Get(frame[1], "delta.stop_reason").String(); reason != "" {
					stop = reason
				}
			}
			if tc.tool {
				if stop != "tool_use" || !json.Valid([]byte(args)) || !strings.Contains(args, "Paris") {
					t.Fatalf("tool call: stop=%q args=%q", stop, args)
				}
			} else if text == "" || stop != "end_turn" {
				t.Fatalf("text %q stop %q", text, stop)
			}
		})
	}
}

// An in-band failure before any output writes nothing and stays retryable;
// a stream cut off after output ends with Messages' error event and is not
// replayed.
func TestClaudeClientHubRetryRules(t *testing.T) {
	service.InitTokenEncoders()
	c, recorder := claudeClientContext()
	failing := "data: {\"error\":{\"message\":\"Overloaded\",\"type\":\"server_error\"}}\n\n"
	if _, err := ClaudeClientStreamHandler(c, upstreamBody(failing), claudeClientInfo(textgen.ProtocolOpenAIChat, true)); err == nil || types.IsSkipRetryError(err) {
		t.Fatalf("a failure before output must be retryable: %v", err)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("nothing may reach the client before a retry: %q", recorder.Body.String())
	}

	c, recorder = claudeClientContext()
	cut := "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"deepseek-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial\"}}]}\n\n"
	if _, err := ClaudeClientStreamHandler(c, upstreamBody(cut), claudeClientInfo(textgen.ProtocolOpenAIChat, true)); err == nil || !types.IsSkipRetryError(err) {
		t.Fatalf("a cut-off stream after output must not be replayed: %v", err)
	}
	frames := sseFrames(recorder.Body.String())
	if len(frames) == 0 || frames[len(frames)-1][0] != "error" || strings.Contains(recorder.Body.String(), "message_stop") {
		t.Fatalf("a cut-off reply must end with an error event: %v", frames)
	}
}

func TestClaudeClientHubServesWholeReplies(t *testing.T) {
	service.InitTokenEncoders()
	chat := `{"id":"c1","object":"chat.completion","model":"deepseek-flash","choices":[{"index":0,"message":{"role":"assistant","content":"Checking.","reasoning_content":"think","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":80}}}`
	responses := `{"id":"resp_1","object":"response","status":"completed","model":"deepseek-flash","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checking."}]},{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":80}}}`
	for name, tc := range map[string]struct {
		upstream textgen.ProtocolFamily
		body     string
	}{"chat": {textgen.ProtocolOpenAIChat, chat}, "responses": {textgen.ProtocolOpenAIResponses, responses}} {
		t.Run(name, func(t *testing.T) {
			c, recorder := claudeClientContext()
			info := claudeClientInfo(tc.upstream, false)
			usage, err := ClaudeClientHandler(c, upstreamBody(tc.body), info)
			if err != nil {
				t.Fatal(err)
			}
			if usage.PromptTokens != 100 || usage.CompletionTokens != 20 {
				t.Fatalf("usage: %+v", usage)
			}
			reply := recorder.Body.String()
			if gjson.Get(reply, "type").String() != "message" || gjson.Get(reply, "stop_reason").String() != "tool_use" {
				t.Fatalf("reply: %s", reply)
			}
			if gjson.Get(reply, `content.#(type=="tool_use").input.city`).String() != "Paris" || gjson.Get(reply, `content.#(type=="text").text`).String() != "Checking." {
				t.Fatalf("content: %s", reply)
			}
			// Messages' input_tokens is the fresh input beside the cache
			if gjson.Get(reply, "usage.input_tokens").Int() != 20 || gjson.Get(reply, "usage.cache_read_input_tokens").Int() != 80 {
				t.Fatalf("usage in the reply: %s", gjson.Get(reply, "usage").Raw)
			}
			if info.FinishReason != "tool_calls" {
				t.Fatalf("finish reason not observed: %q", info.FinishReason)
			}
		})
	}
}

// An empty reply (a role chunk, then a stop) is still a whole Messages
// stream: message_start, message_delta with the stop, message_stop.
func TestClaudeClientHubServesAnEmptyReply(t *testing.T) {
	service.InitTokenEncoders()
	c, recorder := claudeClientContext()
	stream := "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"deepseek-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"model\":\"deepseek-flash\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":0,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	if _, err := ClaudeClientStreamHandler(c, upstreamBody(stream), claudeClientInfo(textgen.ProtocolOpenAIChat, true)); err != nil {
		t.Fatal(err)
	}
	frames := sseFrames(recorder.Body.String())
	var names []string
	for _, frame := range frames {
		names = append(names, frame[0])
	}
	if len(frames) < 3 || names[0] != "message_start" || names[len(names)-1] != "message_stop" || gjson.Get(frames[len(frames)-2][1], "delta.stop_reason").String() != "end_turn" {
		t.Fatalf("empty reply frames: %v", names)
	}
}
