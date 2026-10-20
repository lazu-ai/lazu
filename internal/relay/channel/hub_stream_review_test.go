package channel

import (
	"errors"
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
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// fakeClient records what the runner gave the client.
type fakeClient struct {
	emitted   []textgen.Event
	finished  *dto.Usage
	failEmit  bool
	failFinal bool
}

func (f *fakeClient) Emit(_ *gin.Context, events []textgen.Event) (bool, error) {
	f.emitted = append(f.emitted, events...)
	if f.failEmit {
		return true, errors.New("client went away")
	}
	return len(events) > 0, nil
}

func (f *fakeClient) Finish(_ *gin.Context, usage *dto.Usage) error {
	f.finished = usage
	if f.failFinal {
		return errors.New("client went away")
	}
	return nil
}

func (f *fakeClient) Fail(*gin.Context, types.OpenAIError, *dto.Usage) {}

// scripted decodes each data line by name: text, stop, usage, error, or
// anything else as a decode failure.
func scripted(data string) ([]textgen.Event, error) {
	switch data {
	case "start":
		return []textgen.Event{{Kind: textgen.EventStart}}, nil
	case "text":
		return []textgen.Event{{Kind: textgen.EventText, Text: "hello"}}, nil
	case "stop":
		return []textgen.Event{{Kind: textgen.EventStop, Stop: textgen.StopEnd}}, nil
	case "usage":
		return []textgen.Event{{Kind: textgen.EventUsage, Usage: &dto.Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15, InputTokenSource: dto.UsageSourceProvider, OutputTokenSource: dto.UsageSourceProvider}}}, nil
	case "failed":
		return []textgen.Event{
			{Kind: textgen.EventUsage, Usage: &dto.Usage{PromptTokens: 12, CompletionTokens: 1, TotalTokens: 13, CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 1}}},
			{Kind: textgen.EventError, Error: &textgen.SemanticError{Type: "server_error", Message: "overloaded"}},
		}, nil
	}
	return nil, errors.New("malformed event")
}

func runScripted(t *testing.T, hub HubStream, client HubClient, lines ...string) (*gin.Context, *relaycommon.RelayInfo, *dto.Usage, *types.LazuError) {
	t.Helper()
	service.InitTokenEncoders()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "m"}}
	info.ResetFirstResponse()
	var body strings.Builder
	for _, line := range lines {
		body.WriteString("data: " + line + "\n\n")
	}
	if hub.Decode == nil {
		hub.Decode = scripted
	}
	usage, err := RunHubStream(c, &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body.String()))}, info, hub, client)
	return c, info, usage, err
}

// A Chat upstream's usage chunk follows its finish: read, not dropped.
func TestRunHubStreamReadsPastAChatStop(t *testing.T) {
	client := &fakeClient{}
	_, _, usage, err := runScripted(t, HubStream{ReadPastStop: true}, client, "text", "stop", "usage", "[DONE]")
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens != 12 || usage.CompletionTokens != 3 {
		t.Fatalf("the usage after the stop was dropped: %+v", usage)
	}
}

// A compatible upstream that ends without [DONE] still finishes cleanly.
func TestRunHubStreamReadPastStopEndsAtEOF(t *testing.T) {
	_, _, usage, err := runScripted(t, HubStream{ReadPastStop: true}, &fakeClient{}, "text", "stop", "usage")
	if err != nil || usage.CompletionTokens != 3 {
		t.Fatalf("a stream without [DONE]: %+v %v", usage, err)
	}
}

// A failure before output is absorbed: retryable, refunded even though it
// reported usage, and its exchange left for the attempt that answers.
func TestRunHubStreamHeldFailureIsRefundedAndNotLogged(t *testing.T) {
	c, info, _, err := runScripted(t, HubStream{}, &fakeClient{}, "start", "failed")
	if err == nil || types.IsSkipRetryError(err) {
		t.Fatalf("a failure before output must be retryable: %v", err)
	}
	if !info.StreamFailedWithoutUsage || !common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) {
		t.Fatal("the absorbed attempt must be refunded and marked a first-event failure")
	}
	// RecordModelExchange stamps the reasoning count of any usage that
	// reports one, whether or not full logging is on
	if _, recorded := c.Get("reported_reasoning_tokens"); recorded {
		t.Fatal("a retryable attempt must not take the request's one exchange log")
	}
}

// A cut-off before output, after metadata only, is the same.
func TestRunHubStreamCutOffBeforeOutputIsRetryable(t *testing.T) {
	c, info, _, err := runScripted(t, HubStream{}, &fakeClient{}, "start")
	if err == nil || types.IsSkipRetryError(err) || !info.StreamFailedWithoutUsage || !common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) {
		t.Fatalf("a cut-off before output must be a refunded, retryable first-event failure: %v", err)
	}
}

// A corrupt upstream event is the upstream's fault, retryable before output.
func TestRunHubStreamDecodeFailureIsABadUpstreamBody(t *testing.T) {
	c, info, _, err := runScripted(t, HubStream{}, &fakeClient{}, "{garbage")
	if err == nil || err.GetErrorCode() != types.ErrorCodeBadResponseBody || types.IsSkipRetryError(err) {
		t.Fatalf("decode failure before output: %v", err)
	}
	if !info.StreamFailedWithoutUsage || !common.GetContextKeyBool(c, constant.ContextKeyStreamFirstEventFailed) {
		t.Fatal("a decode failure before output must be refunded and marked a first-event failure")
	}
}

// Usage the upstream produced is settled when the client goes away, mid
// reply or on the last frame.
func TestRunHubStreamSettlesWhenTheClientWriteFails(t *testing.T) {
	c, _, _, err := runScripted(t, HubStream{}, &fakeClient{failEmit: true}, "usage", "text", "stop")
	if err == nil || !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatalf("a failed write after billable output must settle: %v", err)
	}
	c, _, _, err = runScripted(t, HubStream{}, &fakeClient{failFinal: true}, "text", "stop", "usage")
	if err == nil || !common.GetContextKeyBool(c, constant.ContextKeyProtocolStreamSettle) {
		t.Fatalf("a failed final write must settle: %v", err)
	}
}

// The client reads the usage in its own form; the settled one is unchanged.
func TestRunHubStreamGivesTheClientItsUsage(t *testing.T) {
	client := &fakeClient{}
	_, _, usage, err := runScripted(t, HubStream{ClientUsage: func(u *dto.Usage) *dto.Usage {
		shown := *u
		shown.PromptTokens += 100
		return &shown
	}}, client, "text", "usage", "stop")
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens != 12 || client.finished == nil || client.finished.PromptTokens != 112 {
		t.Fatalf("settled %+v, shown %+v", usage, client.finished)
	}
}
