package service

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/errmap"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// RelayErrorHandler classifies an upstream HTTP failure into a Lazu
// error. Accepts *gin.Context so it can look up the active channel type
// and dispatch to the provider-specific errmap (Anthropic / Gemini /
// Ali / etc.); falls back to MapDefault when the channel type is unset
// (e.g. unit tests pass nil) so the wrapper stays safe at all call sites.
func RelayErrorHandler(c *gin.Context, resp *http.Response, _ bool) (lazuErr *types.LazuError) {
	channelType := 0
	if c != nil {
		channelType = common.GetContextKeyInt(c, constant.ContextKeyChannelType)
		CaptureUpstreamRetryAfter(c, resp)
	}

	defer CloseResponseBodyGracefully(resp)
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamErrorBytes+1))
	if err != nil {
		return UpstreamReadError(c, err)
	}
	truncated := len(responseBody) > maxUpstreamErrorBytes
	if truncated {
		// Do not classify a partial JSON prefix or a secret echoed later in it.
		responseBody = nil
	}

	code, args := errmap.MapByChannelType(channelType, resp.StatusCode, responseBody)
	detail := map[string]any{"status_code": resp.StatusCode, "upstream_status": resp.StatusCode,
		"origin": "upstream", "attempt_provider_attempted": true, "error_body_truncated": truncated}
	if channelType != 0 {
		detail["channel_type"] = channelType
	}
	reason := safeUpstreamErrorReason(responseBody)
	if reason.Field != "" {
		detail["field"] = reason.Field
	}
	if reason.Feature != "" {
		detail["rejected_feature"] = reason.Feature
	}
	message := types.CatalogMessage(code, nil)
	if reason.Message != "" {
		message = reason.Message
	}
	detail["reason"] = message
	params := append(args, types.ErrOptionWithDetails(detail))
	lazuErr = types.NewErrorWithStatusCode(
		fmt.Errorf("%s (upstream status %d)", message, resp.StatusCode),
		code,
		types.StatusForCode(code, resp.StatusCode),
		params...,
	)
	if resp.StatusCode != 0 {
		lazuErr.StatusCode = resp.StatusCode
	}
	if code == types.ErrorCodeUpstreamOverloaded {
		lazuErr.StatusCode = types.StatusForCode(code, http.StatusBadGateway)
	}
	if code == types.ErrorCodeUpstreamRateLimited {
		lazuErr.StatusCode = http.StatusTooManyRequests
	}
	if code == types.ErrorCodeUpstreamTimeout {
		lazuErr.StatusCode = http.StatusGatewayTimeout
	}

	var errResponse dto.GeneralErrorResponse
	err = common.Unmarshal(responseBody, &errResponse)
	if err == nil && errResponse.Error.Message != "" && isUpstreamOpenAICompatibleError(errResponse.Error.Type) {
		// Preserve recognized protocol type, but not provider-controlled code,
		// details, param, request_id or arbitrary echoed message text.
		lazuErr = types.WithOpenAIError(
			types.OpenAIError{Type: errResponse.Error.Type, Message: message, Code: code},
			lazuErr.StatusCode,
			types.ErrOptionWithDetails(detail),
			types.ErrOptionWithErrorCode(code),
		)
		return
	}
	if reason.Message != "" {
		_, errType, _, _ := types.RenderError(code, args...)
		lazuErr = types.WithOpenAIError(types.OpenAIError{Type: errType, Message: message, Code: code},
			lazuErr.StatusCode, types.ErrOptionWithDetails(detail), types.ErrOptionWithErrorCode(code))
	}
	return
}

func isUpstreamOpenAICompatibleError(errType string) bool {
	switch errType {
	case types.ErrorTypeInvalidRequest,
		types.ErrorTypeAuthentication,
		types.ErrorTypePermission,
		types.ErrorTypeNotFound,
		types.ErrorTypeRateLimit,
		types.ErrorTypeAPI,
		types.ErrorTypeOverloaded,
		types.ErrorTypeAPIConnection,
		types.ErrorTypeTimeout,
		types.ErrorTypeContentPolicy,
		types.ErrorTypeInsufficientQuota:
		return true
	default:
		return false
	}
}

func ResetStatusCode(lazuErr *types.LazuError, statusCodeMappingStr string) {
	if statusCodeMappingStr == "" || statusCodeMappingStr == "{}" {
		return
	}
	statusCodeMapping := make(map[string]string)
	err := common.Unmarshal([]byte(statusCodeMappingStr), &statusCodeMapping)
	if err != nil {
		return
	}
	if lazuErr.StatusCode == http.StatusOK {
		return
	}
	codeStr := strconv.Itoa(lazuErr.StatusCode)
	if _, ok := statusCodeMapping[codeStr]; ok {
		intCode, _ := strconv.Atoi(statusCodeMapping[codeStr])
		lazuErr.StatusCode = intCode
	}
}
