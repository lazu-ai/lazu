package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestStreamScannerReusePreservesConcurrentEventOrderAndLargeFrames(t *testing.T) {
	// Retained callback strings must survive later requests reusing the initial
	// scanner buffer. Include events that grow beyond that pooled allocation.
	for _, size := range []int{32, 128 << 10} {
		t.Run(fmt.Sprintf("payload-%d", size), func(t *testing.T) {
			const streams, events = 8, 20
			type result struct {
				got, want []string
				scan      StreamScanResult
			}
			results := make([]result, streams)
			var wg sync.WaitGroup
			for stream := range results {
				wg.Add(1)
				go func(stream int) {
					defer wg.Done()
					row := &results[stream]
					var body strings.Builder
					for event := 0; event < events; event++ {
						data := fmt.Sprintf(`{"delta":"stream-%d-event-%d-%s"}`, stream, event, strings.Repeat("x", size))
						row.want = append(row.want, data)
						fmt.Fprintf(&body, "data: %s\n\n", data)
					}
					body.WriteString("data: [DONE]\n\n")
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					row.scan = StreamScannerHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(body.String()))}, &relaycommon.RelayInfo{}, func(data string) bool {
						row.got = append(row.got, data)
						return true
					})
				}(stream)
			}
			wg.Wait()
			for stream, row := range results {
				if !row.scan.Done || row.scan.Err != nil || len(row.got) != events {
					t.Fatalf("stream %d result=%+v events=%d", stream, row.scan, len(row.got))
				}
				for event := range row.want {
					if row.got[event] != row.want[event] {
						t.Fatalf("stream %d event %d was changed or reordered", stream, event)
					}
				}
			}
		})
	}
}

func TestCompletedChunkedStreamReusesConnectionAfterDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"content\":\"ok\"}\n\ndata: [DONE]\n\n")
		w.(http.Flusher).Flush()
		// Make [DONE] precede the HTTP transfer EOF; this was previously
		// closed immediately and discarded an otherwise healthy connection.
		time.Sleep(2 * time.Millisecond)
	}))
	defer server.Close()
	transport := &http.Transport{MaxIdleConnsPerHost: 2}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	for i := 0; i < 20; i++ {
		idle := make(chan struct{}, 1)
		var reused bool
		trace := &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
			PutIdleConn: func(err error) {
				if err == nil {
					idle <- struct{}{}
				}
			},
		}
		req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), "POST", server.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && !reused {
			t.Fatalf("request %d discarded the completed connection", i)
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		result := StreamScannerHandler(c, resp, &relaycommon.RelayInfo{}, func(string) bool { return true })
		if !result.Done || result.Err != nil {
			t.Fatalf("scan=%+v", result)
		}
		select {
		case <-idle:
		case <-time.After(time.Second):
			t.Fatal("connection never returned to the pool")
		}
	}
}

func TestStreamNetworkRetryDependsOnResponseProgress(t *testing.T) {
	for _, started := range []bool{false, true} {
		err := (StreamScanResult{Err: io.ErrUnexpectedEOF}).RelayError(started)
		if err.GetErrorCode() != types.ErrorCodeUpstreamNetworkError || types.IsSkipRetryError(err) != started {
			t.Fatalf("wrong network retry policy: started=%v error=%+v", started, err)
		}
		canceled := (StreamScanResult{Err: context.Canceled}).RelayError(started)
		if !types.IsSkipRetryError(canceled) {
			t.Fatal("canceled request became retryable")
		}
	}
}

func TestStreamHandlerStopReachesMainWaiterWithPingEnabled(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	oldEnabled, oldInterval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 60
	defer func() { settings.PingIntervalEnabled, settings.PingIntervalSeconds = oldEnabled, oldInterval }()
	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		resp := &http.Response{Body: io.NopCloser(strings.NewReader("data: {\"error\":\"busy\"}\n\n"))}
		result := StreamScannerHandler(c, resp, &relaycommon.RelayInfo{}, func(string) bool { return false })
		cancel()
		if result.Err != nil || result.Done {
			t.Fatalf("handler stop was lost to ping; waited for request timeout: %+v", result)
		}
	}
}

func TestStreamLogicalTerminalReturnsBeforeTransferEOF(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	reader, writer := io.Pipe()
	defer writer.Close()
	resp := &http.Response{Body: reader}
	returned := make(chan StreamScanResult, 1)
	go func() {
		returned <- StreamScannerHandlerWithTerminal(c, resp, &relaycommon.RelayInfo{}, func(string) bool { return false }, func() bool { return true })
	}()
	if _, err := io.WriteString(writer, "data: {\"type\":\"response.completed\"}\n\n"); err != nil {
		t.Fatal(err)
	}
	// The upstream writer deliberately remains open. Returning here proves
	// terminal completion rather than observing an eventual transport EOF.
	select {
	case got := <-returned:
		if !got.Done || got.Err != nil {
			t.Fatalf("logical terminal was treated as callback failure: %+v", got)
		}
		if resp.Body != http.NoBody {
			t.Fatal("completed transfer was not handed to bounded background drain")
		}
	case <-time.After(time.Second):
		t.Fatal("logical terminal waited for upstream EOF")
	}
}

func TestStreamCanceledTerminalIsNotCompleted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	reader, writer := io.Pipe()
	defer writer.Close()
	returned := make(chan StreamScanResult, 1)
	go func() {
		returned <- StreamScannerHandlerWithTerminal(c, &http.Response{Body: reader}, &relaycommon.RelayInfo{}, func(string) bool {
			cancel()
			return false
		}, func() bool { return true })
	}()
	if _, err := io.WriteString(writer, "data: {\"type\":\"response.completed\"}\n\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-returned:
		if !errors.Is(got.Err, context.Canceled) || got.Done {
			t.Fatalf("canceled callback was marked completed: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled terminal did not unblock the scanner")
	}
}

func TestStreamCancellationClosesBlockedUpstreamBeforeJoining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	reader, writer := io.Pipe()
	defer writer.Close()
	observed := make(chan struct{})
	result := make(chan StreamScanResult, 1)
	go func() {
		result <- StreamScannerHandler(c, &http.Response{Body: reader}, &relaycommon.RelayInfo{}, func(string) bool { close(observed); return true })
	}()
	if _, err := io.WriteString(writer, "data: {\"text\":\"hello\"}\n\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("scanner did not deliver initial data")
	}
	cancel()
	select {
	case got := <-result:
		if !errors.Is(got.Err, context.Canceled) || got.Done {
			t.Fatalf("wrong cancellation result: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scanner waited for join timeout before closing upstream")
	}
}

func TestHostedStreamingDefaults(t *testing.T) {
	if DefaultPingInterval != 15*time.Second {
		t.Fatalf("expected 15 second ping interval, got %s", DefaultPingInterval)
	}
	if DefaultStreamingTimeout != 15*time.Minute {
		t.Fatalf("expected 15 minute streaming idle timeout, got %s", DefaultStreamingTimeout)
	}
}

func TestStreamScannerHandler_ZeroTimeoutFallsBackSafely(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 0
	defer func() {
		constant.StreamingTimeout = oldTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"delta":"hello"}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		StartTime: time.Now(),
	}

	var got []string
	StreamScannerHandler(c, resp, info, func(data string) bool {
		got = append(got, data)
		return true
	})

	if len(got) != 1 || got[0] != `{"delta":"hello"}` {
		t.Fatalf("unexpected parsed chunks: %+v", got)
	}
	if recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected SSE header, got %q", recorder.Header().Get("Content-Type"))
	}
}

func TestStreamScannerHandler_PreservesChunkOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	defer func() {
		constant.StreamingTimeout = oldTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"delta":"A"}`,
		``,
		`data: {"delta":"B"}`,
		``,
		`data: {"delta":"C"}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		StartTime: time.Now(),
	}

	var got []string
	StreamScannerHandler(c, resp, info, func(data string) bool {
		got = append(got, data)
		return true
	})

	want := []string{`{"delta":"A"}`, `{"delta":"B"}`, `{"delta":"C"}`}
	if len(got) != len(want) {
		t.Fatalf("unexpected chunk count: got=%d want=%d gotChunks=%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk order mismatch at %d: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestStreamScannerHandler_StopsWhenHandlerReturnsFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	defer func() {
		constant.StreamingTimeout = oldTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"delta":"first"}`,
		``,
		`data: {"delta":"second"}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		StartTime: time.Now(),
	}

	var got []string
	StreamScannerHandler(c, resp, info, func(data string) bool {
		got = append(got, data)
		return false
	})

	if len(got) != 1 {
		t.Fatalf("expected scanner to stop after first chunk, got=%+v", got)
	}
	if got[0] != `{"delta":"first"}` {
		t.Fatalf("unexpected first chunk: %q", got[0])
	}
}

func TestStreamScannerHandler_ReturnsQuicklyOnCanceledClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	c.Request = req.WithContext(ctx)

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	defer func() {
		constant.StreamingTimeout = oldTimeout
	}()

	streamBody := strings.Join([]string{
		`data: {"delta":"first"}`,
		``,
		`data: {"delta":"second"}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	info := &relaycommon.RelayInfo{
		StartTime: time.Now(),
	}

	start := time.Now()
	var handled int
	StreamScannerHandler(c, resp, info, func(data string) bool {
		handled++
		return true
	})
	elapsed := time.Since(start)

	if handled != 0 {
		t.Fatalf("expected no chunks handled after client cancellation, got %d", handled)
	}
	if elapsed > time.Second {
		t.Fatalf("expected quick return on canceled context, elapsed=%s", elapsed)
	}
}
