package helper

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// 执行清单 丙16, step one: pin how the scanner treats a callback that blocks
// (a client write that never completes), before anyone removes the
// per-event goroutine. Today the scanner gives up after 10 seconds and ends
// the stream with context.DeadlineExceeded; later events are not handled.
func TestStreamScannerEndsTheStreamWhenACallbackBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the scanner's 10s callback timeout")
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var calls atomic.Int32
	body := "data: {\"n\":1}\n\ndata: {\"n\":2}\n\ndata: [DONE]\n\n"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	start := time.Now()
	result := StreamScannerHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(body))}, &relaycommon.RelayInfo{}, func(string) bool {
		calls.Add(1)
		<-release // a write that never completes
		return true
	})
	elapsed := time.Since(start)
	if elapsed < 9*time.Second || elapsed > 15*time.Second {
		t.Fatalf("scanner returned after %s, want about 10s", elapsed)
	}
	if !errors.Is(result.Err, context.DeadlineExceeded) || result.Done {
		t.Fatalf("result = %+v, want DeadlineExceeded and not done", result)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("callback ran %d times, want 1: later events must not be handled", got)
	}
}

// Step two will replace the per-event goroutine with a write deadline. This
// proves the mechanism on its own: http.ResponseController.SetWriteDeadline
// interrupts a write to a client that has stopped reading, instead of
// leaving it blocked.
func TestWriteDeadlineInterruptsABlockedClientWrite(t *testing.T) {
	writeErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		controller := http.NewResponseController(w)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := []byte("data: " + strings.Repeat("x", 64<<10) + "\n\n")
		if err := controller.SetWriteDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			writeErr <- err
			return
		}
		for {
			if _, err := w.Write(chunk); err != nil {
				writeErr <- err
				return
			}
			if err := controller.Flush(); err != nil {
				writeErr <- err
				return
			}
		}
	}))
	defer server.Close()

	// A client that sends the request and never reads the response.
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(4 << 10)
	}
	writer := bufio.NewWriter(conn)
	_, _ = writer.WriteString("GET / HTTP/1.1\r\nHost: test\r\n\r\n")
	_ = writer.Flush()

	select {
	case err := <-writeErr:
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("write ended with %v, want a timeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the write deadline did not interrupt a blocked write")
	}
}
