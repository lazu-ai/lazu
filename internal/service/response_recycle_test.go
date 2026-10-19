package service

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type observedRecycleBody struct {
	io.ReadCloser
	once   sync.Once
	closed chan struct{}
}

func (b *observedRecycleBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return b.ReadCloser.Close()
}

func TestCompletedResponseDrainClosesBoundedOrBlockedBody(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		var reader io.ReadCloser = io.NopCloser(strings.NewReader(strings.Repeat("x", responseRecycleBytes*4)))
		if blocked {
			r, w := io.Pipe()
			reader = r
			defer w.Close()
		}
		body := &observedRecycleBody{ReadCloser: reader, closed: make(chan struct{})}
		resp := &http.Response{Body: body}
		RecycleCompletedResponse(resp)
		if resp.Body != http.NoBody {
			t.Fatal("ownership was not detached before returning")
		}
		select {
		case <-body.closed:
		case <-time.After(time.Second):
			t.Fatalf("body not closed within bounded drain: blocked=%v", blocked)
		}
	}
}
