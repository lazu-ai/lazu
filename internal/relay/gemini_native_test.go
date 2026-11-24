package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	geminichannel "github.com/lazu-ai/lazu/internal/relay/channel/gemini"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

type geminiNativeCaptureAdaptor struct {
	geminichannel.Adaptor
	bodies [][]byte
}

func (a *geminiNativeCaptureAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, body io.Reader) (any, error) {
	raw, _ := io.ReadAll(body)
	a.bodies = append(a.bodies, raw)
	return nil, errors.New("stop after outbound capture")
}

func useGeminiNativeCapture(t *testing.T) *geminiNativeCaptureAdaptor {
	t.Helper()
	capture := &geminiNativeCaptureAdaptor{}
	old := adaptorFactories[constant.APITypeGemini]
	adaptorFactories[constant.APITypeGemini] = func() channel.BaseAdaptor { return capture }
	t.Cleanup(func() { adaptorFactories[constant.APITypeGemini] = old })
	return capture
}

func TestGeminiHelperNativePreservesPartVideoControlsAndExplicitZeros(t *testing.T) {
	capture := useGeminiNativeCapture(t)

	raw := []byte(`{
		"contents": [{
			"parts": [
				{
					"inlineData": {"mimeType": "video/mp4", "data": "AAAA"},
					"videoMetadata": {"fps": 10, "startOffset": "2s", "endOffset": "4s"},
					"mediaResolution": {"level": "media_resolution_high"}
				},
				{"text": "What appears in the clip?"}
			]
		}],
		"generationConfig": {
			"topP": 0,
			"topK": 0,
			"seed": 0,
			"mediaResolution": "MEDIA_RESOLUTION_LOW",
			"thinkingConfig": {"thinkingLevel": "low", "includeThoughts": false}
		}
	}`)

	var req dto.GeminiChatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini-4-carbon:generateContent", bytes.NewReader(raw))
	c.Set(common.KeyRequestBody, raw)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gemini-4-carbon")
	common.SetContextKey(c, constant.ContextKeyChannelId, 47)

	info := &relaycommon.RelayInfo{
		Request:         &req,
		OriginModelName: "gemini-4-carbon",
		RelayFormat:     types.RelayFormatGemini,
	}
	_ = GeminiHelper(c, info)

	if len(capture.bodies) != 1 {
		t.Fatalf("captured %d bodies, want 1", len(capture.bodies))
	}
	body := capture.bodies[0]
	if got := gjson.GetBytes(body, "contents.0.role").Str; got != "user" {
		t.Fatalf("contents.0.role = %q, want user", got)
	}
	if got := gjson.GetBytes(body, "contents.0.parts.0.videoMetadata.fps").Int(); got != 10 {
		t.Fatalf("videoMetadata.fps = %d, want 10", got)
	}
	if got := gjson.GetBytes(body, "contents.0.parts.0.videoMetadata.startOffset").Str; got != "2s" {
		t.Fatalf("videoMetadata.startOffset = %q, want 2s", got)
	}
	if got := gjson.GetBytes(body, "contents.0.parts.0.videoMetadata.endOffset").Str; got != "4s" {
		t.Fatalf("videoMetadata.endOffset = %q, want 4s", got)
	}
	if got := gjson.GetBytes(body, "contents.0.parts.0.mediaResolution.level").Str; got != "media_resolution_high" {
		t.Fatalf("part mediaResolution.level = %q, want media_resolution_high", got)
	}
	for _, path := range []string{
		"generationConfig.topP",
		"generationConfig.topK",
		"generationConfig.seed",
		"generationConfig.thinkingConfig.includeThoughts",
	} {
		if !gjson.GetBytes(body, path).Exists() {
			t.Fatalf("explicit zero/false field %s missing from outbound body: %s", path, body)
		}
	}
}

func TestGeminiHelperNativeBodyCacheIsolatedByChannel(t *testing.T) {
	capture := useGeminiNativeCapture(t)

	raw := []byte(`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"video/mp4","data":"AAAA"},"videoMetadata":{"fps":4}}]}]}`)
	var req dto.GeminiChatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini-4-carbon:generateContent", bytes.NewReader(raw))
	c.Set(common.KeyRequestBody, raw)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gemini-4-carbon")

	info := &relaycommon.RelayInfo{
		Request:         &req,
		OriginModelName: "gemini-4-carbon",
	}

	// Attempt 1 on channel 10 with channel-specific override.
	common.SetContextKey(c, constant.ContextKeyChannelId, 10)
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]interface{}{
		"generationConfig": map[string]interface{}{"temperature": 0.1},
	})
	_ = GeminiHelper(c, info)

	// Attempt 2 retries on channel 20 with a different override.
	common.SetContextKey(c, constant.ContextKeyChannelId, 20)
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]interface{}{
		"generationConfig": map[string]interface{}{"temperature": 0.9},
	})
	_ = GeminiHelper(c, info)

	if len(capture.bodies) != 2 {
		t.Fatalf("captured %d bodies, want 2", len(capture.bodies))
	}
	if got := gjson.GetBytes(capture.bodies[0], "generationConfig.temperature").Float(); got != 0.1 {
		t.Fatalf("channel 10 temperature = %v, want 0.1", got)
	}
	if got := gjson.GetBytes(capture.bodies[1], "generationConfig.temperature").Float(); got != 0.9 {
		t.Fatalf("channel 20 temperature = %v, want 0.9 (stale cache leaked?)", got)
	}
	if got := gjson.GetBytes(capture.bodies[1], "contents.0.parts.0.videoMetadata.fps").Int(); got != 4 {
		t.Fatalf("channel 20 videoMetadata.fps = %d, want 4", got)
	}
}
