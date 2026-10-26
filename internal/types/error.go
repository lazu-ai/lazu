package types

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
)

// OpenAIError is the OpenAI `error` object. The first four fields are
// OpenAI's own and keep their meaning; the rest are Lazu additions that
// OpenAI-compatible clients ignore.
type OpenAIError struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Param     string `json:"param,omitempty"`
	Code      any    `json:"code"`
	Lazu      string `json:"lazu,omitempty"`
	Docs      string `json:"docs,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Details   any    `json:"details,omitempty"`
}

// ClaudeError is the Anthropic `error` object: type and message are
// Anthropic's, the rest are Lazu additions.
type ClaudeError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
	Lazu    string `json:"lazu,omitempty"`
	Docs    string `json:"docs,omitempty"`
	Details any    `json:"details,omitempty"`
}

type ErrorType string

const (
	ErrorTypeLazuError     ErrorType = "lazu_error"
	ErrorTypeOpenAIError   ErrorType = "openai_error"
	ErrorTypeClaudeError   ErrorType = "claude_error"
	ErrorTypeGeminiError   ErrorType = "gemini_error"
	ErrorTypeRerankError   ErrorType = "rerank_error"
	ErrorTypeUpstreamError ErrorType = "upstream_error"
)

type ErrorCode string

const StatusClientClosedRequest = 499

// Error codes. Rows that clients see carry an LZ number in the catalog;
// the rest are internal distinctions that resolve to a public row on the
// wire (see ErrorSpec.Public). Console domains declare their codes next to
// their catalog rows in error_catalog_<domain>.go.
const (
	// shared request rows
	ErrorCodeInvalidRequest            ErrorCode = "invalid_request"
	ErrorCodeBadRequestBody            ErrorCode = "bad_request_body"
	ErrorCodeModelNameRequired         ErrorCode = "model_name_required"
	ErrorCodeRequestBodyTooLarge       ErrorCode = "request_body_too_large"
	ErrorCodeContextLengthExceeded     ErrorCode = "context_length_exceeded"
	ErrorCodeProtocolBridgeUnsupported ErrorCode = "protocol_bridge_unsupported"
	ErrorCodeCapabilityUnsupported     ErrorCode = "capability_unsupported"
	ErrorCodeConvertRequestFailed      ErrorCode = "convert_request_failed"
	ErrorCodeMissingRequiredParameter  ErrorCode = "missing_required_parameter"
	ErrorCodeEndpointNotSupported      ErrorCode = "endpoint_not_supported"
	ErrorCodeTokenizationError         ErrorCode = "tokenization_error"
	ErrorCodeResourceNotFound          ErrorCode = "resource_not_found"
	ErrorCodeVersionConflict           ErrorCode = "version_conflict"
	ErrorCodeStateConflict             ErrorCode = "state_conflict"
	ErrorCodeClientCanceled            ErrorCode = "client_canceled"
	ErrorCodeDownstreamWriteFailed     ErrorCode = "downstream_write_failed"

	// API keys, sessions, access
	ErrorCodeInvalidAPIKey             ErrorCode = "invalid_api_key"
	ErrorCodeTokenExpired              ErrorCode = "token_expired"
	ErrorCodeTokenDisabled             ErrorCode = "token_disabled"
	ErrorCodeEphemeralCredentialDenied ErrorCode = "ephemeral_credential_denied"
	ErrorCodeProxySignatureInvalid     ErrorCode = "proxy_signature_invalid"
	ErrorCodeSessionRequired           ErrorCode = "session_required"
	ErrorCodeSessionExpired            ErrorCode = "session_expired"
	ErrorCodeSessionInvalid            ErrorCode = "session_invalid"
	ErrorCodeSessionUserMismatch       ErrorCode = "session_user_mismatch"
	ErrorCodeAccessTokenInvalid        ErrorCode = "access_token_invalid"
	ErrorCodeAccessDenied              ErrorCode = "access_denied"
	ErrorCodeIPNotAllowed              ErrorCode = "ip_not_allowed"
	ErrorCodeTokenIpNotAllowed         ErrorCode = "token_ip_not_allowed"
	ErrorCodeTokenIpDenied             ErrorCode = "token_ip_denied"
	ErrorCodeUserBanned                ErrorCode = "user_banned"
	ErrorCodeGroupAccessDenied         ErrorCode = "group_access_denied"
	ErrorCodeChannelSelectionDenied    ErrorCode = "channel_selection_denied"
	ErrorCodeProjectAccessSuspended    ErrorCode = "project_access_suspended"
	ErrorCodeAddressNotEnabled         ErrorCode = "address_not_enabled"

	// wallet and budgets
	ErrorCodeInsufficientQuota          ErrorCode = "insufficient_quota"
	ErrorCodeKeyBudgetExceeded          ErrorCode = "key_budget_exceeded"
	ErrorCodePreConsumeTokenQuotaFailed ErrorCode = "pre_consume_token_quota_failed"
	ErrorCodeMemberBudgetExceeded       ErrorCode = "member_budget_exceeded"
	ErrorCodeProjectBudgetExceeded      ErrorCode = "project_budget_exceeded"
	ErrorCodePlatformBudgetExceeded     ErrorCode = "platform_budget_exceeded"
	ErrorCodePricingNotConfigured       ErrorCode = "pricing_not_configured"

	// models and routes
	ErrorCodeModelNotFound                 ErrorCode = "model_not_found"
	ErrorCodeModelNotAllowed               ErrorCode = "model_not_allowed"
	ErrorCodeTokenNoModelAccess            ErrorCode = "token_no_model_access"
	ErrorCodeTokenModelDenied              ErrorCode = "token_model_denied"
	ErrorCodeTokenVendorDenied             ErrorCode = "token_vendor_denied"
	ErrorCodeNoAvailableChannel            ErrorCode = "no_available_channel"
	ErrorCodeChannelSelectFailed           ErrorCode = "channel_select_failed"
	ErrorCodeInvalidChannelId              ErrorCode = "invalid_channel_id"
	ErrorCodeChannelDisabled               ErrorCode = "channel_disabled"
	ErrorCodeChannelConfigInvalid          ErrorCode = "channel_config_invalid"
	ErrorCodeChannelNoAvailableKeyPublic   ErrorCode = "channel_no_available_key"
	ErrorCodePreferredLaneUnavailable      ErrorCode = "preferred_lane_unavailable"
	ErrorCodeGatewayTemporarilyUnavailable ErrorCode = "gateway_temporarily_unavailable"
	ErrorCodePromptBlocked                 ErrorCode = "prompt_blocked"

	// Internal channel codes keep their colon prefix: IsChannelError keys
	// the retry class off it. Clients never see these spellings.
	ErrorCodeChannelNoAvailableKey           ErrorCode = "channel:no_available_key"
	ErrorCodeChannelParamOverrideInvalid     ErrorCode = "channel:param_override_invalid"
	ErrorCodeChannelHeaderOverrideInvalid    ErrorCode = "channel:header_override_invalid"
	ErrorCodeChannelModelMappedError         ErrorCode = "channel:model_mapped_error"
	ErrorCodeChannelInvalidKey               ErrorCode = "channel:invalid_key"
	ErrorCodeChannelResponseTimeExceeded     ErrorCode = "channel:response_time_exceeded"
	ErrorCodeChannelCompatibilityUnsupported ErrorCode = "channel_compatibility_unsupported"
	ErrorCodeChannelCompatibilityInvalid     ErrorCode = "channel_compatibility_invalid"
	ErrorCodeInvalidApiType                  ErrorCode = "invalid_api_type"

	// rate and capacity
	ErrorCodeTokenRateLimitExceeded        ErrorCode = "token_rate_limit_exceeded"
	ErrorCodeRequestRateLimitExceeded      ErrorCode = "request_rate_limit_exceeded"
	ErrorCodeTotalRequestRateLimitExceeded ErrorCode = "total_request_rate_limit_exceeded"
	ErrorCodeGatewayOverloaded             ErrorCode = "gateway_overloaded"
	ErrorCodeUpstreamRateLimited           ErrorCode = "upstream_rate_limited"
	ErrorCodeTooManyAttempts               ErrorCode = "too_many_attempts"

	// upstream
	ErrorCodeUpstreamError           ErrorCode = "upstream_error"
	ErrorCodeUpstreamInvalidResponse ErrorCode = "upstream_invalid_response"
	ErrorCodeReadResponseBodyFailed  ErrorCode = "read_response_body_failed"
	ErrorCodeBadResponse             ErrorCode = "bad_response"
	ErrorCodeBadResponseBody         ErrorCode = "bad_response_body"
	ErrorCodeEmptyResponse           ErrorCode = "empty_response"
	ErrorCodeUpstreamStreamFailed    ErrorCode = "upstream_stream_failed"
	ErrorCodeUpstreamTimeout         ErrorCode = "upstream_timeout"
	ErrorCodeUpstreamNetworkError    ErrorCode = "upstream_network_error"
	ErrorCodeAPIConnectionError      ErrorCode = "api_connection_error"
	ErrorCodeUpstreamOverloaded      ErrorCode = "upstream_overloaded"
	ErrorCodeUpstreamAuthFailed      ErrorCode = "upstream_auth_failed"
	ErrorCodeUpstreamBillingFailed   ErrorCode = "upstream_billing_failed"

	// internal
	ErrorCodeInternalError        ErrorCode = "internal_error"
	ErrorCodeNotImplemented       ErrorCode = "not_implemented"
	ErrorCodeJsonMarshalFailed    ErrorCode = "json_marshal_failed"
	ErrorCodeGetChannelFailed     ErrorCode = "get_channel_failed"
	ErrorCodeGenRelayInfoFailed   ErrorCode = "gen_relay_info_failed"
	ErrorCodeRateLimitCheckFailed ErrorCode = "rate_limit_check_failed"
	ErrorCodeQueryDataError       ErrorCode = "query_data_error"
	ErrorCodeUpdateDataError      ErrorCode = "update_data_error"
)

type LazuError struct {
	Err            error
	RelayError     any
	templateArgs   []any
	details        any
	messageSuffix  string
	skipRetry      bool
	recordErrorLog *bool
	errorType      ErrorType
	errorCode      ErrorCode
	StatusCode     int
}

func (e *LazuError) GetErrorCode() ErrorCode {
	if e == nil {
		return ""
	}
	return e.errorCode
}

func (e *LazuError) GetErrorType() ErrorType {
	if e == nil {
		return ""
	}
	return e.errorType
}

func (e *LazuError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		// fallback message when underlying error is missing
		return string(e.errorCode)
	}
	return e.Err.Error()
}

func (e *LazuError) MaskSensitiveError() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.errorCode)
	}
	errStr := e.Err.Error()
	if e.errorCode == ErrorCodeTokenizationError {
		return errStr
	}
	return common.MaskSensitiveInfo(errStr)
}

func (e *LazuError) SetMessage(message string) {
	e.Err = errors.New(message)
}

// AppendMessage adds a sentence to the message every client-facing renderer
// produces. SetMessage cannot do this: the rendered message comes from the error
// catalog, and the wrapped error only ever surfaces as a raw_message detail that
// agent clients do not display. Use this for guidance the customer must actually
// read, and keep it to one sentence — it lands in a CLI error line.
func (e *LazuError) AppendMessage(sentence string) {
	if sentence == "" {
		return
	}
	if strings.Contains(e.messageSuffix, sentence) {
		return
	}
	e.messageSuffix += sentence
}

// withSuffix applies the appended guidance to a rendered message.
func (e *LazuError) withSuffix(message string) string {
	if e.messageSuffix == "" {
		return message
	}
	return strings.TrimRight(message, " ") + e.messageSuffix
}

// detailMap returns the error's details as a map for template rendering.
func (e *LazuError) detailMap() map[string]any {
	switch d := e.details.(type) {
	case map[string]any:
		return d
	case map[string]string:
		out := make(map[string]any, len(d))
		for k, v := range d {
			out[k] = v
		}
		return out
	}
	return detailsFromArgs(e.templateArgs)
}

// Wire resolves the error's public face: code, type, number, message and
// the status a client should receive. The runtime StatusCode keeps driving
// retry and breaker decisions; only the written response uses this.
func (e *LazuError) Wire() WireError {
	if e == nil {
		return RenderWire(ErrorCodeInternalError, nil, 0)
	}
	w := RenderWire(e.errorCode, e.detailMap(), e.StatusCode)
	if !w.Known {
		// Upstream protocol errors keep an upstream-supplied code that the
		// catalog does not know; they are still upstream failures.
		if e.errorType == ErrorTypeOpenAIError || e.errorType == ErrorTypeClaudeError {
			w = RenderWire(ErrorCodeUpstreamError, e.detailMap(), e.StatusCode)
			// Keep the provider's own code (e.g. OpenAI's
			// context_length_exceeded) exactly as clients saw it before.
			if e.errorCode != "" {
				w.Code = e.errorCode
			}
		}
	}
	return w
}

// WireStatus is the HTTP status written to the client.
func (e *LazuError) WireStatus() int {
	return e.Wire().Status
}

// hidesCause reports whether the public row must speak for this error: the
// code maps to another row, or the audience is not the customer.
func (e *LazuError) hidesCause() bool {
	spec, ok := lookupSpec(e.errorCode)
	if !ok {
		return false
	}
	if spec.Public != "" {
		return true
	}
	return spec.Audience == AudienceOperator || spec.Audience == AudienceInternal
}

func (e *LazuError) ToOpenAIError() OpenAIError {
	w := e.Wire()
	result := OpenAIError{
		Message: e.withSuffix(w.Message),
		Type:    w.Type,
		Code:    string(w.Code),
		Lazu:    w.ID,
		Docs:    w.Docs,
		Details: e.publicDetails(),
	}
	if e.hidesCause() {
		return result
	}
	switch e.errorType {
	case ErrorTypeOpenAIError:
		if upstream, ok := e.RelayError.(OpenAIError); ok {
			if message := common.MaskSensitiveInfo(upstream.Message); message != "" {
				result.Message = e.withSuffix(message)
			}
			if upstream.Type != "" && upstreamTypeRecognized(upstream.Type) {
				result.Type = upstream.Type
			}
			if upstream.Param != "" {
				result.Param = upstream.Param
			}
		}
	case ErrorTypeClaudeError:
		if upstream, ok := e.RelayError.(ClaudeError); ok {
			if message := common.MaskSensitiveInfo(upstream.Message); message != "" {
				result.Message = e.withSuffix(message)
			}
		}
	}
	if result.Details == nil && shouldAttachRawDetail(e.errorCode) && e.Err != nil {
		result.Details = map[string]string{"raw_message": common.MaskSensitiveInfo(e.Err.Error())}
	}
	return result
}

// publicDetails drops details for rows whose cause must stay on the server.
func (e *LazuError) publicDetails() any {
	if e.details == nil {
		return nil
	}
	if e.Wire().Audience != AudienceCustomer {
		return nil
	}
	return e.details
}

func upstreamTypeRecognized(errType string) bool {
	switch errType {
	case ErrorTypeInvalidRequest, ErrorTypeAuthentication, ErrorTypePermission,
		ErrorTypeNotFound, ErrorTypeRateLimit, ErrorTypeAPI, ErrorTypeOverloaded,
		ErrorTypeAPIConnection, ErrorTypeTimeout, ErrorTypeContentPolicy,
		ErrorTypeInsufficientQuota:
		return true
	}
	return false
}

func (e *LazuError) catalogMessage() string {
	return e.Wire().Message
}

// ClientMessage is the text a client actually displays, guidance included.
func (e *LazuError) ClientMessage() string {
	return e.withSuffix(e.catalogMessage())
}

func shouldAttachRawDetail(code ErrorCode) bool {
	switch code {
	case ErrorCodeUpstreamError,
		ErrorCodeUpstreamRateLimited,
		ErrorCodeUpstreamNetworkError,
		ErrorCodeUpstreamTimeout,
		ErrorCodeUpstreamOverloaded,
		ErrorCodeUpstreamStreamFailed:
		return true
	default:
		return false
	}
}

// ClaudeErrorType maps a Lazu error type to the closest type Anthropic's
// clients know. Anthropic has no insufficient_quota, content-policy or
// timeout types; every mapping below is one Anthropic itself documents.
func ClaudeErrorType(errType string, status int) string {
	switch errType {
	case ErrorTypeInvalidRequest, ErrorTypeContentPolicy, ErrorTypeConflict:
		if status == 413 {
			return "request_too_large"
		}
		return ErrorTypeInvalidRequest
	case ErrorTypeAuthentication, ErrorTypePermission, ErrorTypeNotFound,
		ErrorTypeRateLimit, ErrorTypeOverloaded, ErrorTypeAPI:
		return errType
	case ErrorTypeInsufficientQuota:
		return "billing_error"
	case ErrorTypeTimeout, ErrorTypeAPIConnection:
		return ErrorTypeAPI
	}
	return ErrorTypeAPI
}

func (e *LazuError) ToClaudeError() ClaudeError {
	w := e.Wire()
	result := ClaudeError{
		Type:    ClaudeErrorType(w.Type, w.Status),
		Message: e.withSuffix(w.Message),
		Code:    string(w.Code),
		Lazu:    w.ID,
		Docs:    w.Docs,
		Details: e.publicDetails(),
	}
	if e.hidesCause() {
		return result
	}
	switch e.errorType {
	case ErrorTypeClaudeError:
		if upstream, ok := e.RelayError.(ClaudeError); ok {
			if message := common.MaskSensitiveInfo(upstream.Message); message != "" {
				result.Message = e.withSuffix(message)
			}
			if upstream.Type != "" && upstream.Type != "upstream_error" {
				result.Type = upstream.Type
			}
		}
	case ErrorTypeOpenAIError:
		if upstream, ok := e.RelayError.(OpenAIError); ok {
			if message := common.MaskSensitiveInfo(upstream.Message); message != "" {
				result.Message = e.withSuffix(message)
			}
		}
	}
	return result
}

type LazuErrorOption func(*LazuError)

func NewError(err error, errorCode ErrorCode, params ...any) *LazuError {
	args, ops := splitErrorParams(params)
	var newErr *LazuError
	// 保留深层传递的 new err
	if errors.As(err, &newErr) {
		newErr.templateArgs = append(newErr.templateArgs, args...)
		for _, op := range ops {
			op(newErr)
		}
		return newErr
	}
	e := &LazuError{
		Err:          err,
		RelayError:   nil,
		templateArgs: args,
		errorType:    ErrorTypeLazuError,
		StatusCode:   StatusForCode(errorCode, http.StatusInternalServerError),
		errorCode:    errorCode,
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func NewOpenAIError(err error, errorCode ErrorCode, statusCode int, ops ...LazuErrorOption) *LazuError {
	var nested *LazuError
	if errors.As(err, &nested) {
		for _, op := range ops {
			op(nested)
		}
		return nested
	}
	return NewErrorWithStatusCode(err, errorCode, statusCode, optionsToAny(ops)...)
}

func NewErrorWithStatusCode(err error, errorCode ErrorCode, statusCode int, params ...any) *LazuError {
	args, ops := splitErrorParams(params)
	e := &LazuError{
		Err:          err,
		RelayError:   nil,
		templateArgs: args,
		errorType:    ErrorTypeLazuError,
		StatusCode:   statusCode,
		errorCode:    errorCode,
	}
	for _, op := range ops {
		op(e)
	}

	return e
}

func splitErrorParams(params []any) ([]any, []LazuErrorOption) {
	args := make([]any, 0, len(params))
	ops := make([]LazuErrorOption, 0)
	for _, param := range params {
		switch v := param.(type) {
		case LazuErrorOption:
			ops = append(ops, v)
		case func(*LazuError):
			ops = append(ops, LazuErrorOption(v))
		default:
			args = append(args, param)
		}
	}
	return args, ops
}

func optionsToAny(ops []LazuErrorOption) []any {
	params := make([]any, 0, len(ops))
	for _, op := range ops {
		params = append(params, op)
	}
	return params
}

func WithOpenAIError(openAIError OpenAIError, statusCode int, ops ...LazuErrorOption) *LazuError {
	code, ok := openAIError.Code.(string)
	if !ok {
		if openAIError.Code != nil {
			code = fmt.Sprintf("%v", openAIError.Code)
		} else {
			code = "unknown_error"
		}
	}
	if openAIError.Type == "" {
		openAIError.Type = "upstream_error"
	}
	e := &LazuError{
		RelayError: openAIError,
		errorType:  ErrorTypeOpenAIError,
		StatusCode: statusCode,
		Err:        errors.New(openAIError.Message),
		errorCode:  ErrorCode(code),
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func WithClaudeError(claudeError ClaudeError, statusCode int, ops ...LazuErrorOption) *LazuError {
	if claudeError.Type == "" {
		claudeError.Type = "upstream_error"
	}
	e := &LazuError{
		RelayError: claudeError,
		errorType:  ErrorTypeClaudeError,
		StatusCode: statusCode,
		Err:        errors.New(claudeError.Message),
		errorCode:  ErrorCode(claudeError.Type),
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func IsChannelError(err *LazuError) bool {
	if err == nil {
		return false
	}
	return strings.HasPrefix(string(err.errorCode), "channel:")
}

func IsSkipRetryError(err *LazuError) bool {
	if err == nil {
		return false
	}

	return err.skipRetry
}

func ErrOptionWithSkipRetry() LazuErrorOption {
	return func(e *LazuError) {
		e.skipRetry = true
	}
}

func ErrOptionWithNoRecordErrorLog() LazuErrorOption {
	return func(e *LazuError) {
		e.recordErrorLog = common.GetPointer(false)
	}
}

func ErrOptionWithHideErrMsg(replaceStr string) LazuErrorOption {
	return func(e *LazuError) {
		if common.DebugEnabled {
			fmt.Printf("ErrOptionWithHideErrMsg: %s, origin error: %s", replaceStr, e.Err)
		}
		e.Err = errors.New(replaceStr)
	}
}

func ErrOptionWithDetails(details any) LazuErrorOption {
	return func(e *LazuError) {
		e.details = details
	}
}

func ErrOptionWithErrorCode(code ErrorCode) LazuErrorOption {
	return func(e *LazuError) {
		e.errorCode = code
		if openAIError, ok := e.RelayError.(OpenAIError); ok {
			openAIError.Code = code
			e.RelayError = openAIError
		}
	}
}

func IsRecordErrorLog(e *LazuError) bool {
	if e == nil {
		return false
	}
	if e.recordErrorLog == nil {
		// default to true if not set
		return true
	}
	return *e.recordErrorLog
}
