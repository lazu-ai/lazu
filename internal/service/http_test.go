package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/types"
)

type deliveryTestWriter struct {
	gin.ResponseWriter
	cancel context.CancelFunc
	err    error
}

func (w *deliveryTestWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 7, w.err
	}
	n, err := w.ResponseWriter.Write(p)
	if w.cancel != nil {
		w.cancel()
	}
	return n, err
}

func TestResponseWriteDistinguishesFailureFromNormalClose(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete_then_close", true: "partial_write_failure"}[fail], func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			w := &deliveryTestWriter{ResponseWriter: c.Writer, cancel: cancel}
			if fail {
				w.err = io.ErrClosedPipe
			}
			c.Writer = w
			payload := &ModelLogPayload{}
			AttachModelLogPayload(c, payload)
			IOCopyBytesGracefully(c, nil, []byte(strings.Repeat("x", 11059)))
			code := common.GetContextKeyString(c, constant.ContextKeyRequestErrorCode)
			if fail {
				if code != string(types.ErrorCodeDownstreamWriteFailed) || payload.Metadata["response_bytes_written"] != int64(7) {
					t.Fatalf("code=%s metadata=%v", code, payload.Metadata)
				}
			} else if code != "" || payload.Metadata["downstream_write_complete"] != true || ctx.Err() == nil {
				t.Fatalf("normal post-write close misclassified: code=%s metadata=%v", code, payload.Metadata)
			}
		})
	}
}

func TestUpstreamReadErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		cancel bool
		code   types.ErrorCode
		skip   bool
	}{
		{"inbound_canceled", context.Canceled, true, types.ErrorCodeClientCanceled, true},
		{"upstream_only_canceled", context.Canceled, false, types.ErrorCodeReadResponseBodyFailed, false},
		{"timeout", context.DeadlineExceeded, false, types.ErrorCodeUpstreamTimeout, true},
		{"upstream_reset_while_client_closes", io.ErrUnexpectedEOF, true, types.ErrorCodeReadResponseBodyFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
			got := UpstreamReadError(c, tc.err)
			if got.GetErrorCode() != tc.code || types.IsSkipRetryError(got) != tc.skip {
				t.Fatalf("got %v", got)
			}
			if tc.code == types.ErrorCodeClientCanceled && got.StatusCode != 499 {
				t.Fatalf("status=%d", got.StatusCode)
			}
		})
	}
}
