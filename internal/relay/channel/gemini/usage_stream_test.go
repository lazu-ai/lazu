package gemini

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestGeminiTextGenerationStreamHandlerUsesPartialUsageMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:test", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"candidates":[],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,"thoughtsTokenCount":3}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"},
	}

	usage, err := GeminiTextGenerationStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.PromptTokens != 20 || usage.CompletionTokens != 8 || usage.TotalTokens != 28 {
		t.Fatalf("unexpected stream usage: %+v", usage)
	}
}

func TestGeminiTextGenerationStreamHandlerKeepsPromptOnlyUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:test", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"candidates":[],"usageMetadata":{"promptTokenCount":20,"cachedContentTokenCount":5}}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"},
	}

	usage, err := GeminiTextGenerationStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.PromptTokens != 20 || usage.CompletionTokens != 0 || usage.TotalTokens != 20 {
		t.Fatalf("prompt-only provider usage must not be discarded: %+v", usage)
	}
	if usage.PromptTokensDetails.CachedTokens != 5 {
		t.Fatalf("expected cached prompt details to survive: %+v", usage.PromptTokensDetails)
	}
}

func TestGeminiTextGenerationStreamHandlerReconcilesTotalAfterTextFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:test", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"hello from Gemini"}]}}],"usageMetadata":{"promptTokenCount":20,"totalTokenCount":20}}`,
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

	usage, err := GeminiTextGenerationStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.CompletionTokens <= 0 {
		t.Fatalf("expected text fallback completion usage, got %+v", usage)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("expected reconciled stream total, got %+v", usage)
	}
}

func TestGeminiChatStreamHandlerReconcilesTotalAfterTextFallback(t *testing.T) {
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
		`data: {"candidates":[{"content":{"parts":[{"text":"hello from Gemini"}]}}],"usageMetadata":{"promptTokenCount":20,"totalTokenCount":20}}`,
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

	usage, err := GeminiChatStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage == nil || usage.CompletionTokens <= 0 {
		t.Fatalf("expected text fallback completion usage, got %+v", usage)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("expected reconciled chat stream total, got %+v", usage)
	}
}

func TestGeminiTextGenerationStreamHandlerDoesNotClassifyInlineAudioAsImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:test", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	audioData := base64.StdEncoding.EncodeToString(make([]byte, 8000))
	streamBody := strings.Join([]string{
		fmt.Sprintf(`data: {"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"%s"}}]}}]}`, audioData),
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"},
	}

	usage, err := GeminiTextGenerationStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.CompletionTokens <= 0 || usage.CompletionTokenDetails.AudioTokens != usage.CompletionTokens || usage.CompletionTokenDetails.ImageTokens != 0 {
		t.Fatalf("inline audio must be billed only as audio output: %+v", usage)
	}
}

func TestGeminiChatStreamHandlerDoesNotClassifyInlineAudioAsImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()

	audioData := base64.StdEncoding.EncodeToString(make([]byte, 8000))
	streamBody := strings.Join([]string{
		fmt.Sprintf(`data: {"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"%s"}}]}}]}`, audioData),
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"},
	}

	usage, err := GeminiChatStreamHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.CompletionTokens <= 0 || usage.CompletionTokenDetails.AudioTokens != usage.CompletionTokens || usage.CompletionTokenDetails.ImageTokens != 0 {
		t.Fatalf("inline audio must be billed only as audio output: %+v", usage)
	}
}
