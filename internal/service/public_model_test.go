package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/tidwall/gjson"
)

func publicModelContext(public string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(constant.ContextKeyPublicModel), public)
	return c
}

// A mapped channel's own model id is replaced wherever the served shapes
// carry it; without a mapping every body passes through byte for byte.
func TestRestorePublicModel(t *testing.T) {
	cases := []struct {
		name string
		body string
		path string
	}{
		{"openai body", `{"object":"list","data":[],"model":"@cf/baai/bge-m3"}`, "model"},
		{"responses stream event", `{"type":"response.created","response":{"id":"r","model":"vendor/x-2026"}}`, "response.model"},
		{"claude message_start", `{"type":"message_start","message":{"id":"m","model":"claude-upstream"}}`, "message.model"},
		{"gemini reply", `{"candidates":[],"modelVersion":"gemini-upstream-001"}`, "modelVersion"},
	}
	for _, tc := range cases {
		got := RestorePublicModel(publicModelContext("BAAI/bge-m3"), []byte(tc.body))
		if value := gjson.GetBytes(got, tc.path).Str; value != "BAAI/bge-m3" {
			t.Fatalf("%s: %s = %q, want the public name: %s", tc.name, tc.path, value, got)
		}
		if unmapped := RestorePublicModel(publicModelContext(""), []byte(tc.body)); string(unmapped) != tc.body {
			t.Fatalf("%s: rewritten without a mapping: %s", tc.name, unmapped)
		}
	}
}

func TestRestorePublicModelLeavesOtherFramesAlone(t *testing.T) {
	c := publicModelContext("BAAI/bge-m3")
	for _, frame := range []string{"[DONE]", "", `{"choices":[{"delta":{"content":"model"}}]}`, `{"model":{"id":"nested"}}`} {
		if got := RestorePublicModelString(c, frame); got != frame {
			t.Fatalf("frame %q rewritten to %q", frame, got)
		}
	}
	if got := RestorePublicModelString(nil, `{"model":"x"}`); got != `{"model":"x"}` {
		t.Fatalf("nil context rewrote the frame: %s", got)
	}
}
