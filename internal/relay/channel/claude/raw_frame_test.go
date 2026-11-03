package claude

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

// Fields the DTO does not model reach the client as the upstream sent them;
// usage the upstream left out is filled; the model reads as requested.
func TestClaudeStreamForwardsUpstreamFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	common.SetContextKey(c, constant.ContextKeyPublicModel, "claude-opus-5-5")
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{}}
	state := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	for _, frame := range []string{
		`{"type":"message_start","message":{"id":"msg_1","model":"claude-opus-5-5-20261101","diagnostics":{"cache_miss_reason":"ttl"},"usage":{"input_tokens":1840,"cache_read_input_tokens":96512,"output_tokens":1,"service_tier":"standard"}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"category":"cyber"}},"usage":{"output_tokens":7}}`,
	} {
		if err := HandleStreamResponseData(c, info, state, frame, RequestModeMessage); err != nil {
			t.Fatal(err)
		}
	}
	var datas []string
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			datas = append(datas, data)
		}
	}
	if len(datas) != 2 {
		t.Fatalf("frames: %q", recorder.Body.String())
	}
	start, delta := datas[0], datas[1]
	for path, want := range map[string]string{
		"message.model":                         "claude-opus-5-5",
		"message.diagnostics.cache_miss_reason": "ttl",
		"message.usage.service_tier":            "standard",
		"message.usage.input_tokens":            "1840",
	} {
		if got := gjson.Get(start, path).String(); got != want {
			t.Fatalf("message_start %s = %q, want %q in %s", path, got, want, start)
		}
	}
	for path, want := range map[string]string{
		"delta.stop_details.category":   "cyber",
		"usage.output_tokens":           "7",
		"usage.input_tokens":            "1840",
		"usage.cache_read_input_tokens": "96512",
	} {
		if got := gjson.Get(delta, path).String(); got != want {
			t.Fatalf("message_delta %s = %q, want %q in %s", path, got, want, delta)
		}
	}
}
