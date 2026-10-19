package service

import (
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	responseRecycleWorkers = 8
	responseRecycleQueue   = 128
	responseRecycleBytes   = 32 << 10
	responseRecycleTimeout = 50 * time.Millisecond
)

var completedResponseBodies struct {
	once  sync.Once
	queue chan io.ReadCloser
}

// RecycleCompletedResponse relinquishes a successfully terminated SSE body.
// HTTP/1 needs the chunked transfer EOF before a connection becomes reusable;
// the protocol's [DONE] sentinel alone is insufficient. A bounded worker drain
// keeps a provider's delayed or missing EOF off the caller's completion path.
// Call only after the response's last reader has stopped.
func RecycleCompletedResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil || resp.Body == http.NoBody {
		return
	}
	body := resp.Body
	resp.Body = http.NoBody
	completedResponseBodies.once.Do(func() {
		completedResponseBodies.queue = make(chan io.ReadCloser, responseRecycleQueue)
		for i := 0; i < responseRecycleWorkers; i++ {
			go func() {
				buffer := make([]byte, 4096)
				for body := range completedResponseBodies.queue {
					timer := time.AfterFunc(responseRecycleTimeout, func() { _ = body.Close() })
					_, _ = io.CopyBuffer(io.Discard, io.LimitReader(body, responseRecycleBytes+1), buffer)
					timer.Stop()
					_ = body.Close()
				}
			}()
		}
	})
	select {
	case completedResponseBodies.queue <- body:
	default:
		_ = body.Close()
	}
}
