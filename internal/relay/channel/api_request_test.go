package channel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	common2 "github.com/lazu-ai/lazu/internal/common"
	appconstant "github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestDecisionHTTPAttemptStartsChannelHealthAccounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const channelID = 9876501
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"answers":{}}`))
	}))
	defer server.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(`{}`))
	req, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatDecisions, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channelID}}
	resp, err := DoRequest(c, req, info)
	// DecisionHelper owns completion; this test calls only the HTTP boundary.
	defer metrics.Default.OnPostCall(channelID, 0, 0, metrics.CategoryOK)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := metrics.Default.Snapshot(channelID).InFlight; got != 1 {
		t.Fatalf("HTTP decision attempt missing from channel health: in-flight=%d", got)
	}
	started := common2.GetContextKeyTime(c, appconstant.ContextKeyDecisionUpstreamStartedAt)
	if started.IsZero() || time.Since(started) < 0 {
		t.Fatal("HTTP provider send time was not captured")
	}
}

type failingWSSAdaptor struct{}

func (failingWSSAdaptor) Init(*relaycommon.RelayInfo) {}
func (failingWSSAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) {
	return "://invalid-websocket-url", nil
}
func (failingWSSAdaptor) SetupRequestHeader(*gin.Context, *http.Header, *relaycommon.RelayInfo) error {
	return nil
}
func (failingWSSAdaptor) DoRequest(*gin.Context, *relaycommon.RelayInfo, io.Reader) (any, error) {
	return nil, nil
}
func (failingWSSAdaptor) DoResponse(*gin.Context, *http.Response, *relaycommon.RelayInfo) (any, *types.LazuError) {
	return nil, nil
}
func (failingWSSAdaptor) GetModelList() []string { return nil }
func (failingWSSAdaptor) GetChannelName() string { return "test" }

func TestDoWssRequestMarksProviderAttemptAtDialBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)

	_, err := DoWssRequest(failingWSSAdaptor{}, c, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	}, nil)
	if err == nil {
		t.Fatal("expected invalid WebSocket target to fail")
	}
	if !common2.GetContextKeyBool(c, appconstant.ContextKeyProviderAttempted) {
		t.Fatal("expected WebSocket dial attempt to be marked as provider evidence")
	}
}

func TestSetupApiRequestHeader_ForwardsProtocolHeadersAndBlocksCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "assistants=v2")
	req.Header.Set("Anthropic-Beta", "tools-2024-04-04")
	req.Header.Set("X-Goog-Api-Client", "gl-go/1.22")
	req.Header.Set("Authorization", "Bearer local-token")
	req.Header.Set("X-API-Key", "local-token")
	req.Header.Set("X-Goog-Api-Key", "local-token")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Host", "internal.service.local")
	c.Request = req

	out := http.Header{}
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		IsStream:  true,
	}

	SetupApiRequestHeader(info, c, &out)

	if got := out.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected content-type to be forwarded, got %q", got)
	}
	if got := out.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("expected stream accept fallback, got %q", got)
	}
	if got := out.Get("OpenAI-Beta"); got != "assistants=v2" {
		t.Fatalf("expected OpenAI-Beta header forwarded, got %q", got)
	}
	if got := out.Get("Anthropic-Beta"); got != "tools-2024-04-04" {
		t.Fatalf("expected Anthropic-Beta header forwarded, got %q", got)
	}
	if got := out.Get("X-Goog-Api-Client"); got != "gl-go/1.22" {
		t.Fatalf("expected X-Goog-Api-Client header forwarded, got %q", got)
	}
	if got := out.Get("Authorization"); got != "" {
		t.Fatalf("expected Authorization not to be forwarded, got %q", got)
	}
	if got := out.Get("X-API-Key"); got != "" {
		t.Fatalf("expected X-API-Key not to be forwarded, got %q", got)
	}
	if got := out.Get("X-Goog-Api-Key"); got != "" {
		t.Fatalf("expected X-Goog-Api-Key not to be forwarded, got %q", got)
	}
	if got := out.Get("X-Forwarded-For"); got != "" {
		t.Fatalf("expected X-Forwarded-For not to be forwarded, got %q", got)
	}
	if got := out.Get("X-Forwarded-Proto"); got != "" {
		t.Fatalf("expected X-Forwarded-Proto not to be forwarded, got %q", got)
	}
	if got := out.Get("Host"); got != "" {
		t.Fatalf("expected Host not to be forwarded, got %q", got)
	}
}

func TestForwardProtocolPassthroughHeaders_ForwardsFuturePrefixedHeaders(t *testing.T) {
	src := http.Header{}
	src.Add("OpenAI-Experimental-Feature", "a")
	src.Add("OpenAI-Experimental-Feature", "b")
	src.Set("Anthropic-New-Feature", "enabled")
	src.Set("X-Goog-New-Feature", "enabled")
	src.Set("X-Custom-Feature", "ignored")

	dst := http.Header{}
	forwardProtocolPassthroughHeaders(src, &dst)

	values := dst.Values("OpenAI-Experimental-Feature")
	if len(values) != 2 || values[0] != "a" || values[1] != "b" {
		t.Fatalf("expected OpenAI experimental header values to be preserved, got %#v", values)
	}
	if got := dst.Get("Anthropic-New-Feature"); got != "enabled" {
		t.Fatalf("expected Anthropic prefix header forwarded, got %q", got)
	}
	if got := dst.Get("X-Goog-New-Feature"); got != "enabled" {
		t.Fatalf("expected X-Goog prefix header forwarded, got %q", got)
	}
	if got := dst.Get("X-Custom-Feature"); got != "" {
		t.Fatalf("expected non-allowlisted header to stay filtered, got %q", got)
	}
}

func TestNewUpstreamRequestUsesInboundRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	parentCtx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(parentCtx)
	c.Request = req

	upstream, err := newUpstreamRequest(c, http.MethodPost, "https://upstream.example/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("new upstream request: %v", err)
	}
	cancel()

	select {
	case <-upstream.Context().Done():
	default:
		t.Fatal("expected upstream request context to be cancelled with inbound request context")
	}
}

func TestDoRequestClassifiesInboundCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	parentCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil).WithContext(parentCtx)

	upstream, err := http.NewRequestWithContext(parentCtx, http.MethodPost, "https://upstream.example/v1/images/generations", nil)
	if err != nil {
		t.Fatalf("new upstream request: %v", err)
	}
	cancel()

	_, err = doRequest(c, upstream, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	if err == nil {
		t.Fatal("expected canceled request to fail")
	}
	var lazuErr *types.LazuError
	if !errors.As(err, &lazuErr) {
		t.Fatalf("expected LazuError, got %T: %v", err, err)
	}
	if got := lazuErr.GetErrorCode(); got != types.ErrorCodeClientCanceled {
		t.Fatalf("error code=%q want %q", got, types.ErrorCodeClientCanceled)
	}
	if lazuErr.StatusCode != types.StatusClientClosedRequest {
		t.Fatalf("status=%d want %d", lazuErr.StatusCode, types.StatusClientClosedRequest)
	}
	if !types.IsSkipRetryError(lazuErr) {
		t.Fatal("expected client cancellation to skip retry")
	}
	if types.IsRecordErrorLog(lazuErr) {
		t.Fatal("expected client cancellation not to create an upstream error log")
	}
}

func TestNewUpstreamRequestRejectsIncompleteURL(t *testing.T) {
	for _, target := range []string{"", "/v1/embeddings", "//example.com/v1/embeddings", "https:///v1/embeddings", "ftp://example.com/v1/embeddings"} {
		t.Run(target, func(t *testing.T) {
			req, err := newUpstreamRequest(nil, http.MethodPost, target, nil)
			if err == nil || !strings.Contains(err.Error(), "configure the channel base URL") || req != nil {
				t.Fatalf("expected actionable configuration error, got request=%v error=%v", req, err)
			}
		})
	}
}

func TestNewUpstreamRequestSendsEmbeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	req, err := newUpstreamRequest(nil, http.MethodPost, server.URL+"/v1/embeddings", strings.NewReader(`{"input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestCodexHeaderPassthroughIsOptIn(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "Codex Desktop/0.147.0")
	c.Request.Header.Set("originator", "Codex Desktop")
	c.Request.Header.Set("session-id", "synthetic")
	for _, enabled := range []bool{false, true} {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{CodexClientOnly: enabled}}}
		dst := http.Header{}
		SetupApiRequestHeader(info, c, &dst)
		if (dst.Get("session-id") == "synthetic") != enabled {
			t.Fatal("passthrough ignored opt-in")
		}
	}
}

func TestSharedTransportRejectsRestrictedClientBeforeNetwork(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{CodexClientOnly: true}}}
	req := httptest.NewRequest("POST", "https://must-not-contact.invalid/v1/responses", nil)
	_, err := DoRequest(c, req, info)
	if err == nil || !strings.Contains(err.Error(), "original Codex") {
		t.Fatalf("missing transport restriction: %v", err)
	}
	if common2.GetContextKeyBool(c, appconstant.ContextKeyProviderAttempted) {
		t.Fatal("rejection marked as upstream attempt")
	}
}

func TestClaudeCodeHeaderPassthroughIsOptIn(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.276 (external, cli)")
	c.Request.Header.Set("X-Claude-Code-Session-Id", "synthetic")
	c.Request.Header.Set("Authorization", "Bearer customer-key")
	for _, enabled := range []bool{false, true} {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{ClaudeCodeClientOnly: enabled}}}
		dst := http.Header{}
		SetupApiRequestHeader(info, c, &dst)
		if (dst.Get("X-Claude-Code-Session-Id") == "synthetic") != enabled {
			t.Fatal("passthrough ignored opt-in")
		}
		if (dst.Get("User-Agent") == "claude-cli/2.1.276 (external, cli)") != enabled {
			t.Fatal("User-Agent passthrough ignored opt-in")
		}
		if dst.Get("Authorization") != "" {
			t.Fatal("customer credential forwarded")
		}
	}
}

func TestSharedTransportRejectsNonClaudeCodeClientBeforeNetwork(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "Anthropic/Python 0.40.0")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{ClaudeCodeClientOnly: true}}}
	req := httptest.NewRequest("POST", "https://must-not-contact.invalid/v1/messages", nil)
	_, err := DoRequest(c, req, info)
	if err == nil || !strings.Contains(err.Error(), "Claude Code") {
		t.Fatalf("missing transport restriction: %v", err)
	}
	if common2.GetContextKeyBool(c, appconstant.ContextKeyProviderAttempted) {
		t.Fatal("rejection marked as upstream attempt")
	}
}
