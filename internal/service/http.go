package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func CloseResponseBodyGracefully(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	err := httpResponse.Body.Close()
	if err != nil {
		common.SysError("failed to close response body: " + err.Error())
	}
}

func IOCopyBytesGracefully(c *gin.Context, src *http.Response, data []byte) {
	if c.Writer == nil {
		return
	}

	body := io.NopCloser(bytes.NewBuffer(data))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the httpClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	if src != nil {
		for k, v := range src.Header {
			// avoid setting Content-Length
			if k == "Content-Length" {
				continue
			}
			c.Writer.Header().Set(k, v[0])
		}
	}

	// set Content-Length header manually BEFORE calling WriteHeader
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write header with status code (this sends the headers)
	if src != nil {
		c.Writer.WriteHeader(src.StatusCode)
	} else {
		c.Writer.WriteHeader(http.StatusOK)
	}

	written, err := io.Copy(c.Writer, body)
	if err == nil && written != int64(len(data)) {
		err = io.ErrShortWrite
	}
	if err != nil {
		logger.LogError(c, fmt.Sprintf("failed to copy response body: %s", err.Error()))
		// Generation and usage may already be complete. Preserve accounting,
		// but persist delivery failure instead of a clean HTTP 200 outcome.
		common.SetContextKey(c, constant.ContextKeyRequestErrorCode, string(types.ErrorCodeDownstreamWriteFailed))
		common.SetContextKey(c, constant.ContextKeyRequestErrorType, string(types.ErrorTypeAPIConnection))
	}

	payload := extractModelLogPayload(c)
	if payload == nil {
		payload = &ModelLogPayload{}
	}
	if len(payload.ResponseBody) == 0 {
		payload.ResponseBody = append([]byte(nil), data...)
	}
	if payload.StatusCode == 0 {
		if src != nil {
			payload.StatusCode = src.StatusCode
		} else if c.Writer != nil {
			payload.StatusCode = c.Writer.Status()
		}
	}
	payload.IsStream = false
	if payload.Metadata == nil {
		payload.Metadata = make(map[string]any)
	}
	payload.Metadata["response_bytes_written"] = written
	payload.Metadata["response_bytes_expected"] = len(data)
	payload.Metadata["downstream_write_complete"] = err == nil
	if err != nil {
		payload.Error = common.MaskSensitiveInfo(err.Error())
	}

	RecordModelExchange(c, payload)
	common.SetContextKey(c, constant.ContextKeyModelLogPayload, nil)
}

// UpstreamReadError distinguishes cancellation during body reads from real
// upstream faults. A canceled context after a successful read is irrelevant.
func UpstreamReadError(c *gin.Context, err error) *types.LazuError {
	if errors.Is(err, context.Canceled) && c != nil && c.Request != nil &&
		errors.Is(c.Request.Context().Err(), context.Canceled) {
		return types.NewError(err, types.ErrorCodeClientCanceled,
			types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return types.NewError(err, types.ErrorCodeUpstreamTimeout, types.ErrOptionWithSkipRetry())
	}
	return types.NewError(err, types.ErrorCodeReadResponseBodyFailed)
}

func extractModelLogPayload(c *gin.Context) *ModelLogPayload {
	value, ok := common.GetContextKey(c, constant.ContextKeyModelLogPayload)
	if !ok || value == nil {
		return nil
	}
	payload, ok := value.(*ModelLogPayload)
	if !ok {
		return nil
	}
	return payload
}
