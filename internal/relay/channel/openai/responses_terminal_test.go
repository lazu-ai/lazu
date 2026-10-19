package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

type responsesTerminalCase struct {
	name, event, status, reason string
	finish                      string
	bareFailure                 bool
}

var responsesTerminalCases = []responsesTerminalCase{
	{name: "completed", event: "response.completed", status: "completed", finish: "stop"},
	{name: "length", event: "response.incomplete", status: "incomplete", reason: "max_output_tokens", finish: "length"},
	{name: "content filter", event: "response.incomplete", status: "incomplete", reason: "content_filter", finish: "content_filter"},
	{name: "failed after content", event: "response.failed", status: "failed"},
	{name: "failed without response after content", event: "response.failed", status: "failed", bareFailure: true},
	{name: "error after content", event: "error"},
}

func terminalResponsesInfo(bridge bool) *relaycommon.RelayInfo {
	info := nativeResponsesInfo()
	info.OriginModelName = "gpt-4o-mini"
	info.ChannelMeta.UpstreamModelName = "gpt-4o-mini"
	info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{
		BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
			dto.BuildInToolWebSearchPreview: {SearchContextSize: "low"},
		},
	}
	if bridge {
		info.RelayFormat = types.RelayFormatOpenAI
		info.ShouldIncludeUsage = true
		info.ProtocolPlan = &textgen.RelayPlan{
			ClientProtocol:   textgen.Protocol{Family: textgen.ProtocolOpenAIChat},
			UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses},
		}
	}
	return info
}

func terminalResponsesFrames(tc responsesTerminalCase) []string {
	frames := []string{
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed","action":{"type":"search"}}}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","status":"completed","id":"fc_weather","call_id":"call_weather","name":"get_weather","arguments":"{\"city\":\"Taipei\"}"}}`,
	}
	if tc.event == "error" {
		return append(frames, `{"type":"error","error":{"type":"server_error","code":"overloaded","message":"terminal capacity failure"}}`)
	}
	if tc.bareFailure {
		return append(frames, `{"type":"response.failed"}`)
	}
	reason := ""
	if tc.reason != "" {
		reason = fmt.Sprintf(`,"incomplete_details":{"reason":%q}`, tc.reason)
	}
	failure := ""
	if tc.status == "failed" {
		failure = `,"error":{"type":"server_error","code":"overloaded","message":"terminal capacity failure"}`
	}
	terminal := fmt.Sprintf(`{"type":%q,"response":{"id":"resp_terminal","created_at":1700000001,"model":"gpt-4o-mini","status":%q%s%s,"output":[{"type":"image_generation_call","quality":"high","size":"1024x1024"}],"usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":7},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":15}},"x_future_terminal":{"integer":9007199254740993,"escaped":"\u4f60"}}`, tc.event, tc.status, reason, failure)
	return append(frames, terminal)
}

func responsesWireFrames(frames []string) string {
	return "data: " + strings.Join(frames, "\n\ndata: ") + "\n\n"
}

func responsesNativeFrames(frames []string) string {
	var output strings.Builder
	for _, frame := range frames {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(frame), &event); err != nil {
			panic(err)
		}
		fmt.Fprintf(&output, "event: %s\ndata: %s\n\n", event.Type, frame)
	}
	return output.String()
}

type terminalResponsesResult struct {
	usage                 *dto.Usage
	err                   *types.LazuError
	finish, quality, size string
	image                 bool
	webCalls              int
	recycled              bool
}

func disableTerminalModelCapture(t *testing.T) {
	t.Helper()
	old := common.ModelLogEnabled
	common.ModelLogEnabled = false
	t.Cleanup(func() { common.ModelLogEnabled = old })
	service.InitTokenEncoders()
}

// A provider gate makes the causal claim independent of scheduler timing:
// neither handler return nor downstream HTTP EOF may require transfer EOF.
func TestResponsesLogicalTerminalCompletesBeforeProviderEOF(t *testing.T) {
	disableTerminalModelCapture(t)
	for _, bridge := range []bool{false, true} {
		for _, tc := range responsesTerminalCases {
			name := "native/" + tc.name
			if bridge {
				name = "chat bridge/" + tc.name
			}
			t.Run(name, func(t *testing.T) {
				frames := terminalResponsesFrames(tc)
				gate := make(chan struct{})
				var gateOnce sync.Once
				release := func() { gateOnce.Do(func() { close(gate) }) }
				defer release()
				providerFlushed := make(chan struct{})
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, responsesWireFrames(append(append([]string(nil), frames...), `{"type":"response.output_text.delta","delta":"POISON_AFTER_TERMINAL"}`)))
					w.(http.Flusher).Flush()
					close(providerFlushed)
					select {
					case <-gate:
					case <-r.Context().Done():
					}
				}))
				defer func() { release(); provider.Close() }()
				upstreamTransport := &http.Transport{}
				defer upstreamTransport.CloseIdleConnections()
				upstreamClient := &http.Client{Transport: upstreamTransport, Timeout: 5 * time.Second}
				returned := make(chan terminalResponsesResult, 1)
				engine := gin.New()
				engine.POST("/relay", func(c *gin.Context) {
					response, err := upstreamClient.Get(provider.URL)
					if err != nil {
						c.Status(http.StatusBadGateway)
						return
					}
					info := terminalResponsesInfo(bridge)
					usage, relayErr := OaiResponsesStreamHandler(c, info, response)
					returned <- terminalResponsesResult{
						usage: usage, err: relayErr, finish: info.FinishReason,
						image: c.GetBool("image_generation_call"), quality: c.GetString("image_generation_call_quality"), size: c.GetString("image_generation_call_size"),
						webCalls: info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount,
						recycled: response.Body == http.NoBody,
					}
				})
				gateway := httptest.NewServer(engine)
				defer func() { release(); gateway.Close() }()
				client := &http.Client{Timeout: 3 * time.Second}
				response, err := client.Post(gateway.URL+"/relay", "application/json", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				select {
				case <-providerFlushed:
				case <-time.After(time.Second):
					t.Fatal("provider never flushed the terminal event")
				}
				var result terminalResponsesResult
				select {
				case result = <-returned:
				case <-time.After(2 * time.Second):
					release()
					t.Fatal("handler waited for provider transfer EOF")
				}
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatalf("downstream did not reach HTTP EOF while the provider gate was held: %v", err)
				}
				select {
				case <-gate:
					t.Fatal("provider EOF was released before downstream completion")
				default:
				}
				if !result.recycled {
					t.Fatal("logical terminal did not transfer the body to bounded background drain")
				}
				if result.finish != tc.finish || result.webCalls != 1 {
					t.Fatalf("terminal metadata lost: finish=%q web calls=%d", result.finish, result.webCalls)
				}
				if tc.event != "error" && !tc.bareFailure {
					if result.usage == nil || result.usage.PromptTokens != 12 || result.usage.CompletionTokens != 3 || result.usage.TotalTokens != 15 || result.usage.PromptTokensDetails.CachedTokens != 7 || result.usage.CompletionTokenDetails.ReasoningTokens != 2 {
						t.Fatalf("terminal provider usage/details lost: %+v", result.usage)
					}
					if !result.image || result.quality != "high" || result.size != "1024x1024" {
						t.Fatalf("terminal image metadata lost: %+v", result)
					}
				} else if result.usage == nil || result.usage.CompletionTokens <= 0 {
					t.Fatalf("post-content error lost observed usage: %+v", result.usage)
				}
				if strings.Contains(string(body), "POISON_AFTER_TERMINAL") {
					t.Fatalf("forwarded an event after protocol termination: %s", body)
				}
				if bridge {
					assertTerminalChatFrames(t, string(body), tc, result.err)
				} else {
					if result.err != nil {
						t.Fatalf("native passthrough semantics changed: %v", result.err)
					}
					if string(body) != responsesNativeFrames(frames) {
						t.Fatalf("native terminal frames were truncated, rewritten, duplicated or reordered: %s", body)
					}
				}
				release()
			})
		}
	}
}

func assertTerminalChatFrames(t *testing.T, body string, tc responsesTerminalCase, relayErr *types.LazuError) {
	t.Helper()
	if !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("incomplete final SSE frame: %q", body)
	}
	var content, arguments, finish string
	var usage *dto.Usage
	done, errors, calls := 0, 0, 0
	for _, frame := range strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n") {
		if !strings.HasPrefix(frame, "data: ") {
			t.Fatalf("invalid Chat SSE frame: %q", frame)
		}
		data := strings.TrimPrefix(frame, "data: ")
		if data == "[DONE]" {
			done++
			continue
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name, Arguments string
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *dto.Usage      `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("truncated Chat JSON frame: %q: %v", data, err)
		}
		if len(event.Error) != 0 && string(event.Error) != "null" {
			errors++
		}
		if event.Usage != nil {
			usage = event.Usage
		}
		for _, choice := range event.Choices {
			content += choice.Delta.Content
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
			for _, call := range choice.Delta.ToolCalls {
				if call.ID == "call_weather" && call.Function.Name == "get_weather" {
					calls++
				}
				arguments += call.Function.Arguments
			}
		}
	}
	if content != "hello" || done != 1 {
		t.Fatalf("Chat content or termination changed: content=%q DONE=%d body=%s", content, done, body)
	}
	if tc.event == "error" || tc.status == "failed" {
		if relayErr == nil || relayErr.GetErrorCode() != types.ErrorCodeUpstreamError || errors != 1 {
			t.Fatalf("bridged failure classification lost: error=%v error frames=%d body=%s", relayErr, errors, body)
		}
		return
	}
	wantFinish := tc.finish
	if tc.status == "completed" {
		wantFinish = "tool_calls"
	}
	if relayErr != nil || errors != 0 || finish != wantFinish || calls != 1 || arguments != `{"city":"Taipei"}` || usage == nil || usage.PromptTokens != 12 || usage.CompletionTokens != 3 || usage.TotalTokens != 15 || usage.PromptTokensDetails.CachedTokens != 7 || usage.CompletionTokenDetails.ReasoningTokens != 2 {
		t.Fatalf("Chat terminal/tool/usage frames lost: error=%v finish=%q calls=%d arguments=%q usage=%+v body=%s", relayErr, finish, calls, arguments, usage, body)
	}
}

func TestResponsesLogicalTerminalReusesConnectionForTwentyRequests(t *testing.T) {
	disableTerminalModelCapture(t)
	gate := make(chan chan struct{}, 1)
	var connections atomic.Int64
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release := make(chan struct{})
		gate <- release
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, responsesWireFrames(terminalResponsesFrames(responsesTerminalCases[0])))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	provider.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	provider.Start()
	defer provider.Close()
	transport := &http.Transport{MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for i := 0; i < 20; i++ {
		idle := make(chan error, 1)
		var reused bool
		trace := &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
			PutIdleConn: func(err error) {
				select {
				case idle <- err:
				default:
				}
			},
		}
		request, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodPost, provider.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		release := <-gate
		// Unblock the provider even when the assertion fails. Natural transfer
		// EOF is released only after the protocol handler has returned.
		var releaseOnce sync.Once
		finishProvider := func() { releaseOnce.Do(func() { close(release) }) }
		defer finishProvider()
		if reused != (i > 0) {
			finishProvider()
			t.Fatalf("request %d connection reused=%v", i, reused)
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		returned := make(chan *types.LazuError, 1)
		go func() {
			_, relayErr := OaiResponsesStreamHandler(c, terminalResponsesInfo(false), response)
			returned <- relayErr
		}()
		select {
		case relayErr := <-returned:
			finishProvider()
			if relayErr != nil {
				t.Fatal(relayErr)
			}
		case <-time.After(2 * time.Second):
			finishProvider()
			t.Fatal("terminal handler waited for gated transfer EOF")
		}
		select {
		case err := <-idle:
			if err != nil {
				t.Fatalf("connection was not reusable: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("background drain did not return the connection after natural EOF")
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("20 requests opened %d HTTP connections", connections.Load())
	}
}

func TestResponsesNonTerminalStopsDoNotBecomeCompletedDrains(t *testing.T) {
	disableTerminalModelCapture(t)
	for _, tc := range []struct {
		name, data string
		cancel     bool
	}{
		{name: "first failed event", data: `{"type":"response.failed","response":{"error":{"message":"overloaded"}}}`},
		{name: "first error event", data: `{"type":"error","message":"overloaded"}`},
		{name: "malformed JSON", data: `{"type":"response.completed","response":`},
		{name: "terminal without response", data: `{"type":"response.completed"}`},
		{name: "metadata mentions terminal", data: `{"type":"response.in_progress","nested":{"type":"response.completed"}}`},
		{name: "canceled before terminal", data: terminalResponsesFrames(responsesTerminalCases[0])[3], cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			if tc.cancel {
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			}
			response := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(responsesWireFrames([]string{tc.data}))),
			}
			_, relayErr := OaiResponsesStreamHandler(c, terminalResponsesInfo(false), response)
			if response.Body == http.NoBody {
				t.Fatal("unparsed, rejected, incomplete or canceled stop became a completed drain")
			}
			if tc.cancel && (relayErr == nil || relayErr.GetErrorCode() != types.ErrorCodeClientCanceled) {
				t.Fatalf("client cancellation classification changed: %v", relayErr)
			}
			if strings.HasPrefix(tc.name, "first") && (relayErr == nil || relayErr.StatusCode/100 == 2) {
				t.Fatalf("first-event failure became success: %v", relayErr)
			}
		})
	}
}
