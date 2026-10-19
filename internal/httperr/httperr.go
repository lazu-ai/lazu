// Package httperr is the single way an error leaves the server.
//
// Two envelopes exist and both are rendered from the error catalog:
//
//	console (/api):  {"success": false, "error": {message,type,code,lazu,docs,request_id,details}}
//	OpenAI  (/v1):   {"error": {message,type,param,code,lazu,docs,request_id,details}}
//
// The status is always the real HTTP status. A cause that is not a catalog
// error is logged with the request ID and written as internal_error; its text
// never reaches the client.
package httperr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// HeaderLazuError carries the LZ number of an error response.
const HeaderLazuError = "X-Lazu-Error"

// Body is the error object shared by both envelopes.
type Body struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Param     string `json:"param,omitempty"`
	Code      string `json:"code"`
	Lazu      string `json:"lazu,omitempty"`
	Docs      string `json:"docs,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Details   any    `json:"details,omitempty"`
}

// Resolve turns any error into its wire status and body. It also logs causes
// that must stay on the server.
func Resolve(c *gin.Context, err error) (int, Body) {
	code, details, cause := classify(err)
	w := types.RenderWire(code, details, 0)

	var lazuErr *types.LazuError
	if errors.As(err, &lazuErr) {
		w = lazuErr.Wire()
		if d, ok := lazuErr.ToOpenAIError().Details.(map[string]any); ok {
			details = d
		}
	}
	if !w.Known && code != types.ErrorCodeInternalError {
		cause = fmt.Errorf("uncatalogued error code %q: %w", code, err)
	}
	// Operator console forms still need their structured validation guidance.
	// Only a role established by auth middleware on /api may read it; even
	// an operator's /v1 API key gets the customer-safe gateway response.
	operatorConsole := c != nil && c.Request != nil && c.Request.URL != nil &&
		strings.HasPrefix(c.Request.URL.Path, "/api/") && c.GetInt("role") >= common.RoleRootUser
	if w.Audience == types.AudienceInternal || (w.Audience == types.AudienceOperator && !operatorConsole) {
		details = nil
	}

	// Only the decision controller publishes these validated, customer-owned retry fields.
	if c != nil {
		if safe, exists := c.Get("decision_generation_error_details"); exists {
			if retry, ok := safe.(map[string]interface{}); ok {
				details = retry
			}
		}
	}
	requestID := ""
	if c != nil {
		requestID = c.GetString(common.RequestIdKey)
	}
	if cause != nil || w.Status >= http.StatusInternalServerError {
		logCause(c, requestID, w, cause, err)
	}

	body := Body{
		Message:   w.Message,
		Type:      w.Type,
		Code:      string(w.Code),
		Lazu:      w.ID,
		Docs:      w.Docs,
		RequestID: requestID,
	}
	if len(details) > 0 {
		body.Details = details
		if field, ok := details["field"].(string); ok {
			body.Param = field
		}
	}
	return w.Status, body
}

// classify extracts a catalog code from err. Errors without one become
// internal_error with err kept as the cause.
func classify(err error) (types.ErrorCode, map[string]any, error) {
	if err == nil {
		return types.ErrorCodeInternalError, nil, errors.New("nil error written as failure")
	}
	var apiErr *types.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code, apiErr.Details, apiErr.Cause
	}
	var lazuErr *types.LazuError
	if errors.As(err, &lazuErr) {
		return lazuErr.GetErrorCode(), nil, nil
	}
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return types.ErrorCodeResourceNotFound, nil, nil
	case errors.Is(err, context.Canceled):
		return types.ErrorCodeClientCanceled, nil, nil
	}
	return types.ErrorCodeInternalError, nil, err
}

func logCause(c *gin.Context, requestID string, w types.WireError, cause error, err error) {
	if cause == nil {
		cause = err
	}
	if cause == nil {
		return
	}
	ctx := context.Background()
	path := ""
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
		if c.Request.URL != nil {
			path = c.Request.Method + " " + c.Request.URL.Path
		}
	}
	logger.LogError(ctx, fmt.Sprintf("%s %s request_id=%s path=%q cause=%s",
		w.ID, w.Code, requestID, path, common.MaskSensitiveInfo(cause.Error())))
}

func setHeaders(c *gin.Context, body Body) {
	if body.RequestID != "" {
		c.Header("X-Request-Id", body.RequestID)
	}
	if body.Lazu != "" {
		c.Header(HeaderLazuError, body.Lazu)
	}
}

// Write sends err in the console envelope.
func Write(c *gin.Context, err error) {
	status, body := Resolve(c, err)
	setHeaders(c, body)
	c.JSON(status, gin.H{"success": false, "error": body})
}

// Abort sends err in the console envelope and stops the handler chain.
func Abort(c *gin.Context, err error) {
	Write(c, err)
	c.Abort()
}

// Code is shorthand for Write(c, types.Err(code, details...)).
func Code(c *gin.Context, code types.ErrorCode, details ...types.D) {
	Write(c, types.Err(code, details...))
}

// Invalid reports a request validation failure on one field.
func Invalid(c *gin.Context, field string, details ...types.D) {
	d := types.D{"field": field}
	for _, extra := range details {
		for k, v := range extra {
			d[k] = v
		}
	}
	Write(c, types.Err(types.ErrorCodeInvalidRequest, d))
}

// WriteOpenAI sends err in the OpenAI envelope, for /v1 control-plane routes.
func WriteOpenAI(c *gin.Context, err error) {
	status, body := Resolve(c, err)
	setHeaders(c, body)
	c.JSON(status, gin.H{"error": body})
}

// AbortOpenAI sends err in the OpenAI envelope and stops the handler chain.
func AbortOpenAI(c *gin.Context, err error) {
	WriteOpenAI(c, err)
	c.Abort()
}

// WriteGatewayBody writes an already-rendered error using the request's
// protocol, including failures before the relay handler is reached.
func WriteGatewayBody(c *gin.Context, status int, body Body) {
	setHeaders(c, body)
	path := c.Request.URL.Path
	if path == "/v1/messages" || strings.HasPrefix(path, "/v1/messages/") {
		c.JSON(status, gin.H{
			"type": "error",
			"error": types.ClaudeError{
				Type: types.ClaudeErrorType(body.Type, status), Message: body.Message,
				Code: body.Code, Lazu: body.Lazu, Docs: body.Docs, Details: body.Details,
			},
			"request_id": body.RequestID,
		})
		return
	}
	c.JSON(status, gin.H{"error": body})
}

// AbortGateway sends a protocol-aware error and stops the handler chain.
func AbortGateway(c *gin.Context, err error) {
	status, body := Resolve(c, err)
	WriteGatewayBody(c, status, body)
	c.Abort()
}
