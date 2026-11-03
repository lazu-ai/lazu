package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestConvertImageRequestMapsImagenParameters(t *testing.T) {
	adaptor := &Adaptor{}
	converted, err := adaptor.ConvertImageRequest(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "imagen-4.0-generate-001"}}, dto.ImageRequest{
		Model:   "imagen-4.0-generate-001",
		Prompt:  "a clean product render",
		N:       3,
		Size:    "1792x1024",
		Quality: "high",
	})
	if err != nil {
		t.Fatalf("ConvertImageRequest error: %v", err)
	}
	request, ok := converted.(dto.GeminiImageRequest)
	if !ok {
		t.Fatalf("expected GeminiImageRequest, got %T", converted)
	}
	if len(request.Instances) != 1 || request.Instances[0].Prompt != "a clean product render" {
		t.Fatalf("unexpected instances: %+v", request.Instances)
	}
	if request.Parameters.SampleCount != 3 || request.Parameters.AspectRatio != "16:9" || request.Parameters.ImageSize != "2K" {
		t.Fatalf("unexpected imagen parameters: %+v", request.Parameters)
	}
}

func TestGeminiImageContentHandlerCombinesPartialPromptUsageWithImageFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	body := `{
		"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="}}]}}],
		"usageMetadata":{"promptTokenCount":20,"cachedContentTokenCount":5}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		PromptTokens: 999,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-image-test",
		},
	}

	usage, err := GeminiImageContentHandler(c, info, resp)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if usage.PromptTokens != 20 || usage.CompletionTokens != 258 || usage.TotalTokens != 278 {
		t.Fatalf("unexpected image usage: %+v", usage)
	}
	if usage.PromptTokensDetails.CachedTokens != 5 || usage.CompletionTokenDetails.ImageTokens != 258 {
		t.Fatalf("expected cache and image details to survive: %+v", usage)
	}
	if got := usage.Measurement(); got.Input != dto.UsageSourceProvider || got.Output != dto.UsageSourceLocalEstimate {
		t.Fatalf("image fallback mislabeled: %+v", got)
	}
}

func TestConvertImageRequestBuildsGeminiImagineRequest(t *testing.T) {
	adaptor := &Adaptor{}
	converted, err := adaptor.ConvertImageRequest(nil, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-nano-banana-2"}}, dto.ImageRequest{
		Model:  "gemini-nano-banana-2",
		Prompt: "a bright app icon",
		N:      2,
	})
	if err != nil {
		t.Fatalf("ConvertImageRequest error: %v", err)
	}
	request, ok := converted.(dto.GeminiChatRequest)
	if !ok {
		t.Fatalf("expected GeminiChatRequest, got %T", converted)
	}
	if request.GenerationConfig.CandidateCount != 2 {
		t.Fatalf("expected candidate count 2, got %+v", request.GenerationConfig)
	}
	if len(request.GenerationConfig.ResponseModalities) != 2 ||
		request.GenerationConfig.ResponseModalities[0] != "TEXT" ||
		request.GenerationConfig.ResponseModalities[1] != "IMAGE" {
		t.Fatalf("unexpected response modalities: %+v", request.GenerationConfig.ResponseModalities)
	}
}

func TestGeminiImageContentHandlerClassifiesReplyWithoutImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		body string
		code types.ErrorCode
	}{
		{"prompt blocked", `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`, types.ErrorCodePromptBlocked},
		{"image filtered", `{"candidates":[{"content":{"parts":[]},"finishReason":"IMAGE_SAFETY"}]}`, types.ErrorCodePromptBlocked},
		{"text only", `{"candidates":[{"content":{"parts":[{"text":"Here is a description."}]},"finishReason":"STOP"}]}`, types.ErrorCodeBadResponseBody},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-image-test"}}

			_, err := GeminiImageContentHandler(c, info, resp)
			if err == nil {
				t.Fatal("expected an error for a reply without an image")
			}
			if err.GetErrorCode() != tc.code {
				t.Fatalf("code = %s, want %s (%v)", err.GetErrorCode(), tc.code, err)
			}
			if tc.code == types.ErrorCodePromptBlocked && err.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", err.StatusCode)
			}
		})
	}
}
