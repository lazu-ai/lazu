package types

import (
	"errors"
	"fmt"
)

// D is the details map attached to an error. Values must be plain data
// (strings, numbers, booleans, small slices); they are sent to clients,
// interpolated into messages and localized on the console.
type D = map[string]any

// APIError is an error with a catalog code. Model and service code return it
// for any failure a caller could act on; controllers hand it to
// httperr.Write. The cause, when present, is logged and never written.
type APIError struct {
	Code    ErrorCode
	Details D
	Cause   error
}

// Err builds an APIError for a catalog code.
func Err(code ErrorCode, details ...D) *APIError {
	return &APIError{Code: code, Details: mergeDetails(details)}
}

// WrapErr attaches a cause that stays on the server. A cause that already is
// an APIError is returned unchanged so the innermost, most precise code wins.
func WrapErr(cause error, code ErrorCode, details ...D) *APIError {
	var existing *APIError
	if errors.As(cause, &existing) {
		return existing
	}
	return &APIError{Code: code, Details: mergeDetails(details), Cause: cause}
}

func mergeDetails(details []D) D {
	if len(details) == 0 {
		return nil
	}
	if len(details) == 1 {
		return details[0]
	}
	out := D{}
	for _, d := range details {
		for k, v := range d {
			out[k] = v
		}
	}
	return out
}

// Error is the English catalog message with the code, and the cause when one
// exists. It is meant for logs; clients get the rendered public row.
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	message := CatalogMessage(e.Code, e.Details)
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, message)
}

func (e *APIError) Unwrap() error { return e.Cause }

// Is makes sentinel APIErrors comparable by code:
//
//	var ErrUserNotFound = types.Err(types.ErrorCodeUserNotFound)
//	errors.Is(err, ErrUserNotFound)
func (e *APIError) Is(target error) bool {
	t, ok := target.(*APIError)
	return ok && t.Code == e.Code
}

// With returns a copy carrying extra details.
func (e *APIError) With(details D) *APIError {
	merged := D{}
	for k, v := range e.Details {
		merged[k] = v
	}
	for k, v := range details {
		merged[k] = v
	}
	return &APIError{Code: e.Code, Details: merged, Cause: e.Cause}
}

// Wire renders the error's public face.
func (e *APIError) Wire() WireError {
	return RenderWire(e.Code, e.Details, 0)
}

// ErrCode returns the catalog code of err, or "" when err carries none.
func ErrCode(err error) ErrorCode {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	var lazuErr *LazuError
	if errors.As(err, &lazuErr) {
		return lazuErr.GetErrorCode()
	}
	return ""
}

// IsErrCode reports whether err carries the given catalog code.
func IsErrCode(err error, code ErrorCode) bool {
	return err != nil && ErrCode(err) == code
}
