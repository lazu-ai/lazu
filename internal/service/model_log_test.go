package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func TestRecordModelExchangeWritesModelResponseJSONL(t *testing.T) {
	resetModelLogWriterForTest(t)

	oldEnabled := common.ModelLogEnabled
	oldPath := common.ModelLogPath
	t.Cleanup(func() {
		_ = CloseModelLogWriter()
		resetModelLogWriterForTest(t)
		common.ModelLogEnabled = oldEnabled
		common.ModelLogPath = oldPath
	})

	logDir := t.TempDir()
	common.ModelLogEnabled = true
	common.ModelLogPath = filepath.Join(logDir, "model-responses.jsonl")

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	requestBody := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"ping"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(requestBody))
	c.Set(common.KeyRequestBody, requestBody)
	c.Set(common.RequestIdKey, "req-model-log-test")
	common.SetContextKey(c, constant.ContextKeyChannelName, "primary-openai")
	common.SetContextKey(c, constant.ContextKeyRelayInfo, &relaycommon.RelayInfo{
		UserId:            42,
		TokenId:           7,
		StartTime:         time.Now().Add(-150 * time.Millisecond),
		FirstResponseTime: time.Now().Add(-100 * time.Millisecond),
		RelayMode:         relayconstant.RelayModeChatCompletions,
		RelayFormat:       types.RelayFormatOpenAI,
		RequestURLPath:    "/v1/chat/completions",
		OriginModelName:   "gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         12,
			ChannelType:       constant.ChannelTypeOpenAI,
			ApiType:           constant.APITypeOpenAI,
			UpstreamModelName: "gpt-4.1-mini",
		},
	})

	RecordModelExchange(c, &ModelLogPayload{
		ResponseBody: []byte(`{"id":"chatcmpl-test","model":"gpt-4.1-mini","choices":[]}`),
		Usage: map[string]int{
			"prompt_tokens":     1,
			"completion_tokens": 2,
			"total_tokens":      3,
		},
		StatusCode: 200,
		Metadata: map[string]any{
			"attempt": "primary",
		},
	})

	entry := readWrittenModelLogEntry(t, logDir)

	if entry.RequestID != "req-model-log-test" {
		t.Fatalf("unexpected request id: %q", entry.RequestID)
	}
	if entry.UserID != 42 || entry.TokenID != 7 {
		t.Fatalf("unexpected user/token ids: user=%d token=%d", entry.UserID, entry.TokenID)
	}
	if entry.ChannelID != 12 || entry.ChannelType != constant.ChannelTypeOpenAI {
		t.Fatalf("unexpected channel fields: id=%d type=%d", entry.ChannelID, entry.ChannelType)
	}
	if entry.ChannelName != "primary-openai" {
		t.Fatalf("unexpected channel name: %q", entry.ChannelName)
	}
	if entry.ProviderAPIType != constant.APITypeOpenAI || entry.ProviderFamily != "openai" {
		t.Fatalf("unexpected provider fields: api_type=%d family=%q", entry.ProviderAPIType, entry.ProviderFamily)
	}
	if entry.RelayMode != relayconstant.RelayModeChatCompletions || entry.RelayFormat != string(types.RelayFormatOpenAI) {
		t.Fatalf("unexpected relay fields: mode=%d format=%q", entry.RelayMode, entry.RelayFormat)
	}
	if entry.RequestPath != "/v1/chat/completions" || entry.OriginModel != "gpt-4.1" || entry.UpstreamModel != "gpt-4.1-mini" {
		t.Fatalf("unexpected routing fields: path=%q origin=%q upstream=%q", entry.RequestPath, entry.OriginModel, entry.UpstreamModel)
	}
	if entry.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status code: %d", entry.StatusCode)
	}
	if entry.RequestBody != string(requestBody) || entry.RequestBodyEncoding != "plain" {
		t.Fatalf("unexpected request body fields: body=%q encoding=%q", entry.RequestBody, entry.RequestBodyEncoding)
	}
	if entry.ResponseBody != `{"id":"chatcmpl-test","model":"gpt-4.1-mini","choices":[]}` || entry.ResponseBodyEncoding != "plain" {
		t.Fatalf("unexpected response body fields: body=%q encoding=%q", entry.ResponseBody, entry.ResponseBodyEncoding)
	}

	var usage map[string]int
	if err := json.Unmarshal(entry.Usage, &usage); err != nil {
		t.Fatalf("failed to unmarshal usage: %v", err)
	}
	if usage["total_tokens"] != 3 {
		t.Fatalf("unexpected usage payload: %+v", usage)
	}
	if entry.Metadata["attempt"] != "primary" {
		t.Fatalf("unexpected metadata: %+v", entry.Metadata)
	}
}

func resetModelLogWriterForTest(t *testing.T) {
	t.Helper()
	if modelLogWriter != nil {
		_ = modelLogWriter.Close()
	}
	modelLogWriter = nil
	modelLogWriterOnce = sync.Once{}
}

func readWrittenModelLogEntry(t *testing.T, logDir string) ModelLogEntry {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var lastUnmarshalErr error
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(filepath.Join(logDir, "model-responses-*.jsonl"))
		if err != nil {
			t.Fatalf("failed to glob model log files: %v", err)
		}
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read model log file %s: %v", path, err)
			}
			line := strings.TrimSpace(string(data))
			if line == "" {
				continue
			}
			line = strings.Split(line, "\n")[0]
			var entry ModelLogEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				lastUnmarshalErr = err
				continue
			}
			return entry
		}
		time.Sleep(10 * time.Millisecond)
	}

	if lastUnmarshalErr != nil {
		t.Fatalf("timed out waiting for complete model response log entry in %s: %v", logDir, lastUnmarshalErr)
	}
	t.Fatalf("timed out waiting for model response log file in %s", logDir)
	return ModelLogEntry{}
}

func TestTerminalArchiveWithoutRelayInfoRetainsPartialResponseOnce(t *testing.T) {
	resetModelLogWriterForTest(t)
	oldEnabled, oldPath := common.ModelLogEnabled, common.ModelLogPath
	t.Cleanup(func() {
		_ = CloseModelLogWriter()
		resetModelLogWriterForTest(t)
		common.ModelLogEnabled, common.ModelLogPath = oldEnabled, oldPath
	})
	dir := t.TempDir()
	common.ModelLogEnabled, common.ModelLogPath = true, filepath.Join(dir, "model-responses.jsonl")
	c, w := gin.CreateTestContext(httptest.NewRecorder())
	_ = w
	body := []byte(`{"model":"test","reasoning":{"effort":"high"}}`)
	c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	c.Set(common.KeyRequestBody, body)
	c.Set(common.RequestIdKey, "req-early-error")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "test")
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now().Add(-1500*time.Millisecond))
	common.SetContextKey(c, constant.ContextKeyUserId, 42)
	common.SetContextKey(c, constant.ContextKeyRequestErrorCode, "read_response_body_failed")
	common.SetContextKey(c, constant.ContextKeyAnyProviderAttempted, true)
	c.Writer.WriteHeader(502)
	CaptureIncompleteModelResponse(c, bytes.Repeat([]byte("x"), 70*1024), 200)
	RecordUnfinishedModelExchange(c)
	RecordUnfinishedModelExchange(c)
	entry := readWrittenModelLogEntry(t, dir)
	if entry.RequestID != "req-early-error" || entry.OriginModel != "test" || entry.UserID != 42 || entry.StatusCode != 502 || entry.DurationMs < 1500 {
		t.Fatalf("lost terminal metadata: %+v", entry)
	}
	if entry.RequestBody != string(body) || len(entry.ResponseBody) != 64*1024 || entry.Metadata["response_truncated"] != true || entry.Metadata["response_complete"] != false {
		t.Fatal("partial response or request not preserved")
	}
	_ = CloseModelLogWriter()
	files, _ := filepath.Glob(filepath.Join(dir, "model-responses-*.jsonl"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if bytes.Count(b, []byte("\n")) != 1 {
			t.Fatal("duplicate terminal archive")
		}
	}
}

func TestReasoningTelemetryDoesNotDependOnArchiveEnabled(t *testing.T) {
	old := common.ModelLogEnabled
	common.ModelLogEnabled = false
	defer func() { common.ModelLogEnabled = old }()
	for _, n := range []int{0, 97} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		RecordModelExchange(c, &ModelLogPayload{Usage: &dto.Usage{OutputTokensDetails: &dto.OutputTokenDetails{ReasoningTokens: n}}})
		v, ok := c.Get("reported_reasoning_tokens")
		if !ok || v != n {
			t.Fatalf("lost reported reasoning %d: %v", n, v)
		}
	}
	// Chat Completions reports reasoning under completion_tokens_details.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	RecordModelExchange(c, &ModelLogPayload{Usage: &dto.Usage{CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 41}}})
	if v, _ := c.Get("reported_reasoning_tokens"); v != 41 {
		t.Fatalf("chat completions reasoning lost: %v", v)
	}
}

func TestModelLogCaptureKeepsCompleteIndependentBody(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 512<<10)
	bounded := boundedModelLogPayload(&ModelLogPayload{ResponseBody: body, Metadata: map[string]interface{}{"caller": "kept"}})
	if !bytes.Equal(bounded.ResponseBody, body) || bounded.Metadata["caller"] != "kept" {
		t.Fatal("capture lost body or metadata")
	}
	body[0] = 'z'
	if bounded.ResponseBody[0] != 'x' {
		t.Fatal("bounded prefix retained mutable oversized backing allocation")
	}
}
func TestModelLogStreamCaptureWritesOneCompleteRepresentation(t *testing.T) {
	resetModelLogWriterForTest(t)
	oldEnabled, oldPath := common.ModelLogEnabled, common.ModelLogPath
	t.Cleanup(func() {
		_ = CloseModelLogWriter()
		resetModelLogWriterForTest(t)
		common.ModelLogEnabled, common.ModelLogPath = oldEnabled, oldPath
	})
	dir := t.TempDir()
	common.ModelLogEnabled = true
	common.ModelLogPath = filepath.Join(dir, "model-responses.jsonl")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4.1","messages":[]}`)))
	c.Set(common.RequestIdKey, "capture-stream-single")
	common.SetContextKey(c, constant.ContextKeyRelayInfo, &relaycommon.RelayInfo{StartTime: time.Now(), RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions})
	chunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hello"}}]}`
	RecordModelExchange(c, &ModelLogPayload{IsStream: true, StatusCode: 200, ResponseChunks: []string{chunk}, Usage: &dto.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}})
	entry := readWrittenModelLogEntry(t, dir)
	if len(entry.ResponseChunks) > 0 || !strings.Contains(entry.ResponseBody, "hello") {
		t.Fatalf("duplicate raw events or missing reconstructed output: %+v", entry)
	}
}
