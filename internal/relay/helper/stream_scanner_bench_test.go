package helper

import (
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// BenchmarkStreamScannerEvents measures the scanner's own cost per event:
// a stream of 2000 small output events and a callback that does nothing.
func BenchmarkStreamScannerEvents(b *testing.B) {
	gin.SetMode(gin.TestMode)
	var body strings.Builder
	for range 2000 {
		body.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
	}
	body.WriteString("data: [DONE]\n\n")
	payload := body.String()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		result := StreamScannerHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(payload))}, &relaycommon.RelayInfo{}, func(string) bool { return true })
		if !result.Done {
			b.Fatalf("stream not done: %+v", result)
		}
	}
}

// The per-stream handler goroutine must exit when the stream ends, or every
// stream would leave one behind.
func TestStreamScannerHandlerGoroutineExitsWithTheStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := "data: {\"n\":1}\n\ndata: {\"n\":2}\n\ndata: [DONE]\n\n"
	run := func() {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		result := StreamScannerHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(payload))}, &relaycommon.RelayInfo{}, func(string) bool { return true })
		if !result.Done {
			t.Fatalf("stream not done: %+v", result)
		}
	}
	run()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	for range 200 {
		run()
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+5 {
		t.Fatalf("goroutines grew from %d to %d over 200 streams", before, after)
	}
}
