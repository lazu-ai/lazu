package helper

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

type flushCountingWriter struct {
	*httptest.ResponseRecorder
	mu      sync.Mutex
	flushes atomic.Int32
	flushed string // body as of the last flush
	first   string // body as of the first flush
}

func (w *flushCountingWriter) firstFlushed() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.first
}

func (w *flushCountingWriter) Flush() {
	w.mu.Lock()
	w.flushed = w.Body.String()
	if w.flushes.Load() == 0 {
		w.first = w.flushed
	}
	w.mu.Unlock()
	w.flushes.Add(1)
	w.ResponseRecorder.Flush()
}

func (w *flushCountingWriter) flushedBody() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushed
}

func streamEvents(n int) string {
	var b strings.Builder
	for range n {
		b.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
	}
	return b.String()
}

// Events that arrive in one read are written with one flush, not one each;
// the stream's first event is flushed on its own, ahead of the rest.
func TestStreamScannerCoalescesFlushesForBufferedEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := &flushCountingWriter{ResponseRecorder: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	body := streamEvents(50) + "data: [DONE]\n\n"
	var handled atomic.Int32
	result := StreamScannerHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(body))}, &relaycommon.RelayInfo{}, func(data string) bool {
		handled.Add(1)
		return StringData(c, data) == nil
	})
	if !result.Done || handled.Load() != 50 {
		t.Fatalf("result=%+v handled=%d", result, handled.Load())
	}
	if got := strings.Count(writer.flushedBody(), "data: {"); got != 50 {
		t.Fatalf("flushed %d events, want all 50", got)
	}
	if got := writer.flushes.Load(); got > 5 {
		t.Fatalf("%d flushes for 50 buffered events, want a handful", got)
	}
	if first := writer.firstFlushed(); strings.Count(first, "data: {") != 1 {
		t.Fatalf("first flush carried %q, want the first event alone", first)
	}
}

// blockingReader returns its first chunk, then blocks until released.
type blockingReader struct {
	first   string
	release chan struct{}
	waiting chan struct{}
	rest    io.Reader
	sent    bool
}

func (r *blockingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.first), nil
	}
	if r.waiting != nil {
		close(r.waiting)
		r.waiting = nil
		<-r.release
	}
	return r.rest.Read(p)
}

// Output is never held back while the scanner waits on the network.
func TestStreamScannerFlushesBeforeWaitingOnTheNetwork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := &flushCountingWriter{ResponseRecorder: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	reader := &blockingReader{
		first:   streamEvents(3),
		release: make(chan struct{}),
		waiting: make(chan struct{}),
		rest:    strings.NewReader(streamEvents(1) + "data: [DONE]\n\n"),
	}
	waiting := reader.waiting
	done := make(chan StreamScanResult, 1)
	go func() {
		done <- StreamScannerHandler(c, &http.Response{Body: io.NopCloser(reader)}, &relaycommon.RelayInfo{}, func(data string) bool {
			return StringData(c, data) == nil
		})
	}()
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("scanner never asked for more data")
	}
	deadline := time.Now().Add(2 * time.Second)
	for strings.Count(writer.flushedBody(), "data: {") < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := strings.Count(writer.flushedBody(), "data: {"); got != 3 {
		t.Fatalf("%d of 3 events flushed while waiting on the network", got)
	}
	close(reader.release)
	if result := <-done; !result.Done {
		t.Fatalf("result = %+v", result)
	}
	if got := strings.Count(writer.flushedBody(), "data: {"); got != 4 {
		t.Fatalf("flushed %d events in the end, want 4", got)
	}
}

// stallingFlushWriter lets its first flush through and blocks every later
// one until released, like a client that stopped reading.
type stallingFlushWriter struct {
	*httptest.ResponseRecorder
	flushes atomic.Int32
	release chan struct{}
}

func (w *stallingFlushWriter) Flush() {
	if w.flushes.Add(1) > 1 {
		<-w.release
	}
}

// A held-back flush runs under the same write timeout as a callback: a client
// that stops reading mid-burst ends the stream instead of pinning the scanner
// until the idle timeout.
func TestStreamScannerBoundsHeldBackFlush(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := streamWriteTimeout
	streamWriteTimeout = 200 * time.Millisecond
	t.Cleanup(func() { streamWriteTimeout = previous })
	writer := &stallingFlushWriter{ResponseRecorder: httptest.NewRecorder(), release: make(chan struct{})}
	t.Cleanup(func() { close(writer.release) })
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	done := make(chan StreamScanResult, 1)
	go func() {
		done <- StreamScannerHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(streamEvents(3)))}, &relaycommon.RelayInfo{}, func(data string) bool {
			return StringData(c, data) == nil
		})
	}()
	select {
	case result := <-done:
		if !errors.Is(result.Err, context.DeadlineExceeded) {
			t.Fatalf("result = %+v, want a write timeout", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scanner stayed blocked on a held-back flush")
	}
}
