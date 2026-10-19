package gemini

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/constant"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde")

func multipartEditContext(t *testing.T, field string, images int, contentType string) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "gemini-3.1-flash-image")
	_ = writer.WriteField("prompt", "make it blue")
	for i := 0; i < images; i++ {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="`+field+`"; filename="ref.png"`)
		if contentType != "" {
			header.Set("Content-Type", contentType)
		}
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatalf("create part: %v", err)
		}
		_, _ = part.Write(tinyPNG)
	}
	_ = writer.Close()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return c
}

func TestConvertImageRequestGeminiEdits(t *testing.T) {
	imagineInfo := func(mode int) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{RelayMode: mode, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.1-flash-image"}}
	}
	cases := []struct {
		name       string
		mode       int
		ctx        func(t *testing.T) *gin.Context
		wantParts  int
		wantErr    string
		wantInline string
	}{
		{name: "generation keeps the single text part", mode: constant.RelayModeImagesGenerations,
			ctx: func(*testing.T) *gin.Context { return nil }, wantParts: 1},
		{name: "image[] references become inline data", mode: constant.RelayModeImagesEdits,
			ctx: func(t *testing.T) *gin.Context { return multipartEditContext(t, "image[]", 3, "image/png") }, wantParts: 4, wantInline: "image/png"},
		{name: "single image field", mode: constant.RelayModeImagesEdits,
			ctx: func(t *testing.T) *gin.Context { return multipartEditContext(t, "image", 1, "") }, wantParts: 2, wantInline: "image/png"},
		{name: "no images is an error", mode: constant.RelayModeImagesEdits,
			ctx: func(t *testing.T) *gin.Context { return multipartEditContext(t, "image[]", 0, "") }, wantErr: "image is required"},
		{name: "too many images", mode: constant.RelayModeImagesEdits,
			ctx: func(t *testing.T) *gin.Context {
				return multipartEditContext(t, "image[]", GeminiImageReferenceMax+1, "image/png")
			}, wantErr: "at most 14"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			converted, err := (&Adaptor{}).ConvertImageRequest(tc.ctx(t), imagineInfo(tc.mode), dto.ImageRequest{
				Model: "gemini-3.1-flash-image", Prompt: "make it blue", N: 1,
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ConvertImageRequest: %v", err)
			}
			request := converted.(dto.GeminiChatRequest)
			parts := request.Contents[0].Parts
			if len(parts) != tc.wantParts {
				t.Fatalf("parts = %d, want %d", len(parts), tc.wantParts)
			}
			if parts[0].Text != "make it blue" || parts[0].InlineData != nil {
				t.Fatalf("first part must stay the prompt: %+v", parts[0])
			}
			for _, part := range parts[1:] {
				if part.InlineData == nil || part.InlineData.MimeType != tc.wantInline {
					t.Fatalf("reference part = %+v", part)
				}
				if decoded, _ := base64.StdEncoding.DecodeString(part.InlineData.Data); !bytes.Equal(decoded, tinyPNG) {
					t.Fatalf("reference bytes changed")
				}
			}
			if request.GenerationConfig.ResponseModalities[1] != "IMAGE" {
				t.Fatalf("modalities = %+v", request.GenerationConfig.ResponseModalities)
			}
		})
	}
}

func TestGeminiEditsRequestHeaderIsJSON(t *testing.T) {
	c := multipartEditContext(t, "image[]", 1, "image/png")
	header := http.Header{}
	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeImagesEdits, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k"}}
	if err := (&Adaptor{}).SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("SetupRequestHeader: %v", err)
	}
	if got := header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	// The text path keeps forwarding the client's own content type.
	textCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	textCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	textCtx.Request.Header.Set("Content-Type", "application/json; charset=utf-8")
	textHeader := http.Header{}
	textInfo := &relaycommon.RelayInfo{RelayMode: constant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "k"}}
	if err := (&Adaptor{}).SetupRequestHeader(textCtx, &textHeader, textInfo); err != nil {
		t.Fatalf("SetupRequestHeader text: %v", err)
	}
	if got := textHeader.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("text Content-Type = %q", got)
	}
}
