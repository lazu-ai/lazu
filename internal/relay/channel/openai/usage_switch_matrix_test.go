package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 执行清单 丁11: FORCE_STREAM_OPTION, GET_MEDIA_TOKEN and
// GET_MEDIA_TOKEN_NOT_STREAM decide where billed tokens come from. These
// tests pin today's behaviour, cell by cell, before anyone retires a switch.

func setSwitch(t *testing.T, target *bool, value bool) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

// What the gateway sends as stream_options.include_usage on a native Chat
// request to an OpenAI channel that supports stream options.
func TestStreamOptionsIncludeUsageMatrix(t *testing.T) {
	clients := map[string]string{
		"client omits":       ``,
		"client asks for it": `,"stream_options":{"include_usage":true}`,
		"client declines it": `,"stream_options":{"include_usage":false}`,
	}
	for _, force := range []bool{true, false} {
		for _, stream := range []bool{true, false} {
			for name, clientOptions := range clients {
				t.Run(fmt.Sprintf("force=%t/stream=%t/%s", force, stream, name), func(t *testing.T) {
					setSwitch(t, &constant.ForceStreamOption, force)
					body := []byte(fmt.Sprintf(`{"model":"gpt-4o-mini","stream":%t,"messages":[{"role":"user","content":"hi"}]%s}`, stream, clientOptions))
					info := &relaycommon.RelayInfo{IsStream: stream, ChannelMeta: &relaycommon.ChannelMeta{
						ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-4o-mini", SupportStreamOptions: true,
					}}
					got, err := (&Adaptor{}).ConvertNativeRequest(body, info, false)
					if err != nil {
						t.Fatal(err)
					}
					value := gjson.GetBytes(got, "stream_options.include_usage")
					var want string
					switch {
					case !stream:
						want = "absent" // stream_options only belongs on a stream
					case force:
						want = "true" // forced, even over the client's false
					case clientOptions == "":
						want = "absent"
					default:
						want = gjson.Get("{"+clientOptions[1:]+"}", "stream_options.include_usage").Raw
					}
					gotText := "absent"
					if value.Exists() {
						gotText = value.Raw
					}
					if gotText != want {
						t.Fatalf("include_usage = %s, want %s: %s", gotText, want, got)
					}
				})
			}
		}
	}
}

func usageMatrixInfo(stream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		IsStream:    stream,
		Request: &dto.GeneralOpenAIRequest{Model: "gpt-4o-mini", Messages: []dto.Message{
			{Role: "user", Content: "Count the tokens of this prompt, please, for the usage matrix."},
		}},
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4o-mini"},
	}
}

func usageMatrixContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

type usageCell struct {
	prompt int
	source dto.UsageSource
}

func describe(usage *dto.Usage) usageCell {
	return usageCell{prompt: usage.PromptTokens, source: usage.Measurement().Input}
}

// recordedChatStreams are a recorded OpenAI-compatible Chat stream that
// reports usage (DeepSeek, include_usage on) and the same stream with every
// usage object nulled out, as an upstream that ignores include_usage sends
// it. It also returns the recording's prompt_tokens.
func recordedChatStreams(t *testing.T) (withUsage, withoutUsage string, prompt int) {
	t.Helper()
	withUsage = fixtures.Raw(t, "deepseek/chat_text.sse")
	lines := strings.Split(withUsage, "\n")
	for i, line := range lines {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || !gjson.Valid(payload) {
			continue
		}
		if usage := gjson.Get(payload, "usage.prompt_tokens"); usage.Exists() {
			prompt = int(usage.Int())
		}
		nulled, err := sjson.SetRaw(payload, "usage", "null")
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = "data: " + nulled
	}
	if prompt == 0 {
		t.Fatal("recording reports no prompt_tokens")
	}
	return withUsage, strings.Join(lines, "\n"), prompt
}

// Streaming: upstream usage wins whatever the switches say; without it the
// prompt comes from the local estimate when GET_MEDIA_TOKEN is on, and is
// zero when it is off.
func TestStreamUsageSourceMatrix(t *testing.T) {
	withUsage, withoutUsage, recordedPrompt := recordedChatStreams(t)
	for _, mediaToken := range []bool{true, false} {
		for name, stream := range map[string]string{"upstream reports usage": withUsage, "upstream omits usage": withoutUsage} {
			t.Run(fmt.Sprintf("GET_MEDIA_TOKEN=%t/%s", mediaToken, name), func(t *testing.T) {
				setSwitch(t, &constant.GetMediaToken, mediaToken)
				usage, err := OaiStreamHandler(usageMatrixContext(), usageMatrixInfo(true), &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream)),
				})
				if err != nil {
					t.Fatalf("stream failed: %v", err)
				}
				got := describe(usage)
				switch {
				case name == "upstream reports usage":
					if got.prompt != recordedPrompt || got.source != dto.UsageSourceProvider {
						t.Fatalf("got %+v, want the provider's %d", got, recordedPrompt)
					}
				case mediaToken:
					if got.prompt <= 0 || got.source != dto.UsageSourceLocalEstimate {
						t.Fatalf("got %+v, want a local estimate", got)
					}
				default:
					if got.prompt != 0 {
						t.Fatalf("got %+v, want no prompt tokens", got)
					}
				}
			})
		}
	}
}

// Non-streaming: without upstream usage, the prompt is estimated locally
// only when both GET_MEDIA_TOKEN and GET_MEDIA_TOKEN_NOT_STREAM are on.
func TestNonStreamUsageSourceMatrix(t *testing.T) {
	const withoutUsage = `{"id":"c","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`
	for _, mediaToken := range []bool{true, false} {
		for _, notStream := range []bool{true, false} {
			t.Run(fmt.Sprintf("GET_MEDIA_TOKEN=%t/GET_MEDIA_TOKEN_NOT_STREAM=%t", mediaToken, notStream), func(t *testing.T) {
				setSwitch(t, &constant.GetMediaToken, mediaToken)
				setSwitch(t, &constant.GetMediaTokenNotStream, notStream)
				usage, err := OpenaiHandler(usageMatrixContext(), usageMatrixInfo(false), &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(withoutUsage)),
				})
				if err != nil {
					t.Fatal(err)
				}
				got := describe(usage)
				if mediaToken && notStream {
					if got.prompt <= 0 || got.source != dto.UsageSourceLocalEstimate {
						t.Fatalf("got %+v, want a local estimate", got)
					}
				} else if got.prompt != 0 {
					t.Fatalf("got %+v, want no prompt tokens", got)
				}
			})
		}
	}
}
