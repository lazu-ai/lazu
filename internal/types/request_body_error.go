package types

import (
	"errors"
	"net/http"
)

// NewRequestBodyError keeps limits stable across chunked, compressed and
// already-buffered requests. Callers must preserve wrapped read errors.
func NewRequestBodyError(err error) *LazuError {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return NewError(err, ErrorCodeRequestBodyTooLarge, ErrOptionWithSkipRetry())
	}
	return NewError(err, ErrorCodeInvalidRequest, ErrOptionWithSkipRetry())
}
