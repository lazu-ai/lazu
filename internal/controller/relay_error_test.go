package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestWriteRelayError_OpenAIEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	err := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeInvalidRequest, http.StatusBadRequest)
	writeRelayError(c, types.RelayFormatOpenAI, nil, err)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}

	var body map[string]any
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &body); decodeErr != nil {
		t.Fatalf("decode response failed: %v", decodeErr)
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("expected openai envelope to include error field")
	}
	if _, ok := body["type"]; ok {
		t.Fatalf("openai envelope should not expose top-level type field")
	}
	if got := common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode); got != string(types.ErrorCodeInvalidRequest) {
		t.Fatalf("expected routing error code %q, got %q", types.ErrorCodeInvalidRequest, got)
	}
}

func TestWriteRelayErrorDoesNotAppendAfterProtocolTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	recorder.Body.WriteString("data: protocol-terminal\n\n")
	c, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(c, constant.ContextKeyProtocolStreamTerminal, true)
	err := types.NewErrorWithStatusCode(errors.New("already emitted"), types.ErrorCodeConvertRequestFailed, http.StatusBadGateway)
	writeRelayError(c, types.RelayFormatOpenAI, nil, err)
	if got := recorder.Body.String(); got != "data: protocol-terminal\n\n" {
		t.Fatalf("expected no second stream error, got %q", got)
	}
}

func TestRecordRetriedRoutingAttemptPersistsSupersededFailure(t *testing.T) {
	db := setupObservabilityTestDB(t)
	if err := db.AutoMigrate(&model.RoutingAttemptOutcome{}); err != nil {
		t.Fatalf("migrate attempt outcome: %v", err)
	}
	previousQueueEnabled := common.RelayEventQueueEnabled
	common.RelayEventQueueEnabled = false
	t.Cleanup(func() { common.RelayEventQueueEnabled = previousQueueEnabled })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "retried-upstream-request")
	c.Set("routing_decision_id", int64(771))
	c.Set("final_group", model.ChannelLaneAgent)
	lazuErr := types.NewErrorWithStatusCode(
		errors.New("upstream unavailable"), types.ErrorCodeUpstreamError,
		http.StatusBadGateway,
	)

	recordRetriedRoutingAttempt(c, 772, 0, time.Now().UTC().Add(-50*time.Millisecond), lazuErr)
	status := http.StatusBadGateway
	model.FlushRelayResultEvent(c, &model.RoutingOutcome{RequestID: "retried-upstream-request", DecisionID: 771, OutcomeAt: time.Now().UTC(), StatusCode: &status})

	var stored model.RoutingAttemptOutcome
	if err := db.First(&stored, "request_id = ?", "retried-upstream-request").Error; err != nil {
		t.Fatalf("load attempt outcome: %v", err)
	}
	if stored.ChannelID != 772 || stored.FinalGroup != model.ChannelLaneAgent ||
		stored.StatusCode != http.StatusBadGateway || stored.TotalDurationMs <= 0 {
		t.Fatalf("stored attempt = %+v", stored)
	}
}

func TestWriteRelayError_ClaudeEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	err := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeInvalidRequest, http.StatusTooManyRequests)
	writeRelayError(c, types.RelayFormatClaude, nil, err)

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status %d, got %d", http.StatusTooManyRequests, recorder.Code)
	}

	var body map[string]any
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &body); decodeErr != nil {
		t.Fatalf("decode response failed: %v", decodeErr)
	}
	if got, ok := body["type"].(string); !ok || got != "error" {
		t.Fatalf("expected claude envelope top-level type=error, got %#v", body["type"])
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("expected claude envelope to include error field")
	}
}

func TestWriteRelayError_ClientCancellationSetsRoutingOutcomeContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	err := types.NewErrorWithStatusCode(
		errors.New("request canceled"),
		types.ErrorCodeClientCanceled,
		types.StatusClientClosedRequest,
		types.ErrOptionWithSkipRetry(),
	)
	writeRelayError(c, types.RelayFormatOpenAI, nil, err)

	if recorder.Code != types.StatusClientClosedRequest {
		t.Fatalf("expected status %d, got %d", types.StatusClientClosedRequest, recorder.Code)
	}
	if got := common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode); got != string(types.ErrorCodeClientCanceled) {
		t.Fatalf("expected routing error code %q, got %q", types.ErrorCodeClientCanceled, got)
	}
}

func TestClaudeCompatibilityRejectionDoesNotRetryAndRefunds(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set(common.RequestIdKey, "req-compat-local")
	err := types.NewErrorWithStatusCode(errors.New("mid-conversation system is not supported by this channel"), types.ErrorCodeChannelCompatibilityUnsupported, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	if shouldRetry(c, err, 3) {
		t.Fatal("local compatibility rejection must not select another channel")
	}
	info := &relaycommon.RelayInfo{FinalPreConsumedQuota: 100}
	if !shouldReturnPreConsumedQuota(info, err) {
		t.Fatal("local rejection must refund its outstanding reservation")
	}
	writeRelayError(c, types.RelayFormatClaude, nil, err)
	if got := c.Writer.Header().Get("X-Request-Id"); got != "req-compat-local" {
		t.Fatalf("missing request id: %q", got)
	}
	if common.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) {
		t.Fatal("local rejection changed attempt evidence")
	}
	if err.ToClaudeError().Type != types.ErrorTypeInvalidRequest {
		t.Fatal("Claude error type is not protocol compatible")
	}
}

func TestRelayStubAndUnknownRouteRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		handler gin.HandlerFunc
		status  int
		code    string
	}{
		{RelayNotImplemented, http.StatusNotImplemented, "not_implemented"},
		{RelayNotFound, http.StatusNotFound, "endpoint_not_supported"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/variations", nil)
		tc.handler(c)
		var body struct {
			Error types.OpenAIError `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || body.Error.Code != tc.code {
			t.Fatalf("got %d %+v", w.Code, body)
		}
	}
}

// An empty wallet must reach the client as a final answer: Claude Code and
// the Anthropic SDK stop on 402 or `x-should-retry: false`, Codex stops on
// 429 insufficient_quota, and the OpenAI SDKs obey the header.
func TestWriteRelayErrorInsufficientQuotaPerWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name   string
		format types.RelayFormat
		path   string
		stream bool
		status int
	}{
		{"anthropic messages", types.RelayFormatClaude, "/v1/messages", false, http.StatusPaymentRequired},
		{"anthropic messages stream", types.RelayFormatClaude, "/v1/messages", true, http.StatusPaymentRequired},
		{"openai chat", types.RelayFormatOpenAI, "/v1/chat/completions", false, http.StatusTooManyRequests},
		{"openai chat stream", types.RelayFormatOpenAI, "/v1/chat/completions", true, http.StatusTooManyRequests},
		{"openai responses", types.RelayFormatOpenAIResponses, "/v1/responses", false, http.StatusTooManyRequests},
		{"openai responses stream", types.RelayFormatOpenAIResponses, "/v1/responses", true, http.StatusTooManyRequests},
		{"gemini", types.RelayFormatGemini, "/v1beta/models/gemini-2.5-pro:generateContent", false, http.StatusTooManyRequests},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, test.path, nil)
			if test.stream {
				common.SetContextKey(c, constant.ContextKeyIsStream, true)
			}
			err := types.NewErrorWithStatusCode(errors.New("insufficient account balance"),
				types.ErrorCodeInsufficientQuota, http.StatusTooManyRequests, types.ErrOptionWithSkipRetry())
			writeRelayError(c, test.format, nil, err)

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if got := recorder.Header().Get("x-should-retry"); got != "false" {
				t.Fatalf("x-should-retry = %q, want false", got)
			}
			var body struct {
				Error struct {
					Type    string `json:"type"`
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &body); decodeErr != nil {
				t.Fatalf("decode %q: %v", recorder.Body.String(), decodeErr)
			}
			wantType := "insufficient_quota"
			if test.format == types.RelayFormatClaude {
				wantType = "billing_error"
			}
			if body.Error.Type != wantType || body.Error.Code != "insufficient_quota" {
				t.Fatalf("error type/code = %q/%q, want %q/insufficient_quota", body.Error.Type, body.Error.Code, wantType)
			}
			if body.Error.Message == "" {
				t.Fatal("message missing")
			}
		})
	}
}

func TestWriteRelayErrorRateLimitStaysRetryable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		err := types.NewErrorWithStatusCode(errors.New("rpm"), types.ErrorCodeRequestRateLimitExceeded, http.StatusTooManyRequests)
		writeRelayError(c, format, nil, err)
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("%s: status = %d, want 429", format, recorder.Code)
		}
		if got := recorder.Header().Get("x-should-retry"); got != "" {
			t.Fatalf("%s: rate limit carries x-should-retry %q", format, got)
		}
	}
}
