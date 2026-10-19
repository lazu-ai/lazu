package types

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Error types are the single answer to "what should the client do next".
// The first eleven mirror OpenAI / Anthropic and are part of the public
// contract; conflict_error exists for the console only.
const (
	ErrorTypeInvalidRequest    = "invalid_request_error"
	ErrorTypeAuthentication    = "authentication_error"
	ErrorTypePermission        = "permission_error"
	ErrorTypeNotFound          = "not_found_error"
	ErrorTypeRateLimit         = "rate_limit_error"
	ErrorTypeAPI               = "api_error"
	ErrorTypeOverloaded        = "overloaded_error"
	ErrorTypeAPIConnection     = "api_connection_error"
	ErrorTypeTimeout           = "timeout_error"
	ErrorTypeContentPolicy     = "content_policy_violation_error"
	ErrorTypeInsufficientQuota = "insufficient_quota"
	ErrorTypeConflict          = "conflict_error"
)

type FallbackCategory string

const (
	FallbackNone          FallbackCategory = "none"
	FallbackGeneral       FallbackCategory = "general"
	FallbackContentPolicy FallbackCategory = "content_policy"
	FallbackContextWindow FallbackCategory = "context_window"
)

// Audience says who can act on an error. Customer errors are explained to
// the caller; operator errors are shown to customers only as a neutral
// "unavailable" sentence and carry their detail in logs and operator pages;
// internal errors never explain their cause outside the server.
type Audience string

const (
	AudienceCustomer Audience = "customer"
	AudienceOperator Audience = "operator"
	AudienceInternal Audience = "internal"
)

// ErrorDocsBaseURL is where every LZ-#### number is documented. The anchor is
// the lower-cased number, e.g. https://lazu.ai/docs/errors#lz-3101.
const ErrorDocsBaseURL = "https://lazu.ai/docs/errors"

// ErrorSpec is one row of the error catalog.
//
// There are two kinds of rows:
//
//   - Public rows carry an ID (LZ-####). Their code is what clients see on
//     the wire and what the console, docs and locales are keyed by.
//   - Internal rows set Public to another row's code. They exist because the
//     gateway's retry policy, channel breaker and logs need to tell apart
//     failures a client cannot act on differently (a marshal failure and a
//     database failure are both "internal_error" to a caller). Everything on
//     the wire — code, type, message, number, status — comes from the public
//     row; HTTPStatus, Retry and FallbackCat of the internal row keep driving
//     server-side decisions exactly as before.
type ErrorSpec struct {
	// ID is the Lazu error number (LZ-####). Required on public rows,
	// forbidden on internal rows.
	ID string
	// Public names the row a client sees instead of this one.
	Public ErrorCode
	// HTTPStatus is the status the server reasons with (retry classes,
	// breaker eligibility). For public rows it is also the wire status unless
	// WireStatus overrides it.
	HTTPStatus int
	// WireStatus, when set, replaces whatever status the runtime carried when
	// the error is written to a client. It exists for errors whose runtime
	// status is inherited from an upstream and would mislead a caller, such as
	// an upstream 401 caused by Lazu's own provider key.
	WireStatus int
	Type       string
	Audience   Audience
	// Template is English. `{name}` is filled from the error's details and
	// `{name|fallback}` falls back to the text after the bar when the detail
	// is absent.
	Template    string
	Retry       bool
	FallbackCat FallbackCategory
}

var (
	catalogMu sync.RWMutex
	// Catalog maps every ErrorCode to its spec. Gateway rows are declared
	// below; console domains register theirs from error_catalog_*.go.
	Catalog = map[ErrorCode]ErrorSpec{}
)

// RegisterErrors adds a domain's rows to the catalog. It panics on a
// duplicate code so two domains can never silently disagree about one.
func RegisterErrors(entries map[ErrorCode]ErrorSpec) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	for code, spec := range entries {
		if _, exists := Catalog[code]; exists {
			panic(fmt.Sprintf("error code %q registered twice", code))
		}
		Catalog[code] = spec
	}
}

func lookupSpec(code ErrorCode) (ErrorSpec, bool) {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	spec, ok := Catalog[code]
	return spec, ok
}

// PublicCode resolves an internal code to the code a client sees.
func PublicCode(code ErrorCode) ErrorCode {
	spec, ok := lookupSpec(code)
	if !ok {
		return ErrorCodeInternalError
	}
	if spec.Public != "" {
		return spec.Public
	}
	return code
}

// WireError is everything a writer needs to put an error on the wire.
type WireError struct {
	Status   int
	Type     string
	Code     ErrorCode
	Message  string
	ID       string
	Docs     string
	Audience Audience
	// Known is false when the code had no catalog row and was rendered as
	// internal_error.
	Known bool
}

// RenderWire resolves a code to its public face. runtimeStatus is the status
// the error carried at runtime (0 when unknown); it is honoured only for
// public rows without a WireStatus, which keeps upstream pass-through
// statuses such as a provider's 404 intact.
func RenderWire(code ErrorCode, details map[string]any, runtimeStatus int) WireError {
	spec, ok := lookupSpec(code)
	known := ok
	if !ok {
		code = ErrorCodeInternalError
		spec, _ = lookupSpec(code)
		runtimeStatus = 0
	}
	public := code
	pspec := spec
	status := runtimeStatus
	if spec.Public != "" {
		public = spec.Public
		pspec, _ = lookupSpec(public)
		status = 0
	}
	switch {
	case spec.WireStatus != 0:
		status = spec.WireStatus
	case pspec.WireStatus != 0:
		status = pspec.WireStatus
	case status == 0:
		status = pspec.HTTPStatus
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}
	return WireError{
		Status:   status,
		Type:     pspec.Type,
		Code:     public,
		Message:  RenderTemplate(pspec.Template, details),
		ID:       pspec.ID,
		Docs:     ErrorDocsURL(pspec.ID),
		Audience: pspec.Audience,
		Known:    known,
	}
}

// ErrorDocsURL links an LZ number to its row in the docs reference.
func ErrorDocsURL(id string) string {
	if id == "" {
		return ""
	}
	return ErrorDocsBaseURL + "#" + strings.ToLower(id)
}

// RenderError keeps the historical four-value signature for writers that
// only have a code: wire status, wire type, English message, public code.
func RenderError(code ErrorCode, args ...any) (httpStatus int, errType string, message string, codeStr string) {
	w := RenderWire(code, detailsFromArgs(args), 0)
	return w.Status, w.Type, w.Message, string(w.Code)
}

// CatalogMessage renders the row's own template, not its public face. It is
// for logs and internal diagnostics, where the precise failure matters.
func CatalogMessage(code ErrorCode, details map[string]any) string {
	spec, ok := lookupSpec(code)
	if !ok {
		return string(code)
	}
	return RenderTemplate(spec.Template, details)
}

func detailsFromArgs(args []any) map[string]any {
	for _, arg := range args {
		if m, ok := arg.(map[string]any); ok {
			return m
		}
	}
	return nil
}

var placeholderPattern = regexp.MustCompile(`\{([a-z_]+)(?:\|([^}]*))?\}`)

// RenderTemplate fills `{name}` and `{name|fallback}` placeholders.
func RenderTemplate(template string, details map[string]any) string {
	if !strings.Contains(template, "{") {
		return template
	}
	return placeholderPattern.ReplaceAllStringFunc(template, func(token string) string {
		parts := placeholderPattern.FindStringSubmatch(token)
		if value, ok := details[parts[1]]; ok && value != nil && fmt.Sprint(value) != "" {
			return fmt.Sprint(value)
		}
		return parts[2]
	})
}

// StatusForCode is the status the server reasons with for a code.
func StatusForCode(code ErrorCode, fallback int) int {
	if spec, ok := lookupSpec(code); ok && spec.HTTPStatus != 0 {
		return spec.HTTPStatus
	}
	if fallback != 0 {
		return fallback
	}
	return http.StatusInternalServerError
}

func ShouldRetry(code ErrorCode) bool {
	if spec, ok := lookupSpec(code); ok {
		return spec.Retry
	}
	return false
}

func GetFallbackCategory(code ErrorCode) FallbackCategory {
	if spec, ok := lookupSpec(code); ok && spec.FallbackCat != "" {
		return spec.FallbackCat
	}
	return FallbackNone
}

// PublicCatalogCodes lists every public code, sorted by LZ number.
func PublicCatalogCodes() []ErrorCode {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	codes := make([]ErrorCode, 0, len(Catalog))
	for code, spec := range Catalog {
		if spec.Public == "" {
			codes = append(codes, code)
		}
	}
	sort.Slice(codes, func(i, j int) bool {
		return Catalog[codes[i]].ID < Catalog[codes[j]].ID
	})
	return codes
}

const unavailableMessage = "This model is temporarily unavailable. The request was not completed; retry later."

func init() {
	RegisterErrors(gatewayCatalog)
}

// gatewayCatalog holds the /v1 relay rows and the shared rows every surface
// uses. Console domains live in error_catalog_<domain>.go.
var gatewayCatalog = map[ErrorCode]ErrorSpec{
	// ── LZ-1xxx · the request itself ───────────────────────────────────
	ErrorCodeInvalidRequest: {
		ID: "LZ-1001", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "Invalid request: {reason|check the parameter values}.",
	},
	ErrorCodeBadRequestBody: {
		ID: "LZ-1002", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "The request body is malformed or was rejected as invalid.",
	},
	ErrorCodeModelNameRequired: {
		ID: "LZ-1003", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "The model parameter is required.",
	},
	ErrorCodeRequestBodyTooLarge: {
		ID: "LZ-1004", HTTPStatus: http.StatusRequestEntityTooLarge, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "Request body is too large.",
	},
	ErrorCodeContextLengthExceeded: {
		ID: "LZ-1005", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template:    "The input exceeds this model's context window. Shorten the conversation or choose a model with a longer context.",
		FallbackCat: FallbackContextWindow,
	},
	ErrorCodeProtocolBridgeUnsupported: {
		ID: "LZ-1006", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "{reason|This route cannot carry a feature of this request to the selected model. Use a model that supports it natively.}",
	},
	ErrorCodeCapabilityUnsupported: {
		ID: "LZ-1007", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "The selected model does not support {capability|a feature used by this request}.",
	},
	ErrorCodeConvertRequestFailed: {
		ID: "LZ-1008", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "The request could not be converted for the selected model.",
	},
	ErrorCodeMissingRequiredParameter: {
		ID: "LZ-1009", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "A required parameter is missing: {field|see the endpoint reference}.",
	},
	ErrorCodeEndpointNotSupported: {
		ID: "LZ-1010", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
		Template: "The endpoint {path|requested} is not supported.",
	},
	ErrorCodeTokenizationError: {
		ID: "LZ-1011", HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI, Audience: AudienceCustomer,
		Template: "The input tokens could not be counted.",
	},
	ErrorCodeResourceNotFound: {
		ID: "LZ-1012", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
		Template: "The requested {resource|resource} was not found.",
	},
	ErrorCodeVersionConflict: {
		ID: "LZ-1013", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
		Template: "This record changed after it was loaded. Reload it and try again.",
	},
	ErrorCodeStateConflict: {
		ID: "LZ-1014", HTTPStatus: http.StatusConflict, Type: ErrorTypeConflict, Audience: AudienceCustomer,
		Template: "This action does not apply to the record's current state.",
	},
	ErrorCodeClientCanceled: {
		ID: "LZ-1901", HTTPStatus: StatusClientClosedRequest, Type: ErrorTypeAPIConnection, Audience: AudienceCustomer,
		Template: "The request was canceled by the client.",
	},
	ErrorCodeDownstreamWriteFailed: {
		Public: ErrorCodeClientCanceled, HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPIConnection,
		Template: "Failed to write response to downstream connection",
	},

	// ── LZ-20xx · API keys ─────────────────────────────────────────────
	ErrorCodeInvalidAPIKey: {
		ID: "LZ-2001", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
		Template: "Invalid API key.",
	},
	ErrorCodeTokenExpired: {
		ID: "LZ-2002", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
		Template: "This API key has expired. Extend its expiry in the console or create a new key.",
	},
	ErrorCodeTokenDisabled: {
		ID: "LZ-2003", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
		Template: "This API key is disabled. Enable it in the console or use another key.",
	},
	ErrorCodeEphemeralCredentialDenied: {
		ID: "LZ-2004", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "This ephemeral credential cannot be used for this request: {reason|it is only valid for scoped relay requests}.",
	},

	ErrorCodeProxySignatureInvalid: {
		ID: "LZ-2005", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceInternal,
		Template: "Request origin could not be verified.",
	},

	// ── LZ-21xx · console session ──────────────────────────────────────
	ErrorCodeSessionRequired: {
		ID: "LZ-2101", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
		Template: "Sign in to continue.",
	},
	ErrorCodeSessionExpired: {
		ID: "LZ-2102", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
		Template: "Your session has expired. Sign in again.",
	},
	ErrorCodeSessionInvalid: {
		ID: "LZ-2103", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
		Template: "Your session is no longer valid. Sign in again.",
	},
	ErrorCodeSessionUserMismatch: {
		ID: "LZ-2104", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
		Template: "This page belongs to a different signed-in account. Reload the page.",
	},
	ErrorCodeAccessTokenInvalid: {
		ID: "LZ-2105", HTTPStatus: http.StatusUnauthorized, Type: ErrorTypeAuthentication, Audience: AudienceCustomer,
		Template: "The access token is invalid.",
	},

	// ── LZ-23xx · access ───────────────────────────────────────────────
	ErrorCodeAccessDenied: {
		ID: "LZ-2301", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "You do not have permission to perform this action.",
	},
	ErrorCodeIPNotAllowed: {
		ID: "LZ-2302", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "This API key does not accept requests from your IP address.",
	},
	ErrorCodeTokenIpNotAllowed: {
		Public: ErrorCodeIPNotAllowed, HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission,
		Template: "Source IP is not in the token's allowed list",
	},
	ErrorCodeTokenIpDenied: {
		Public: ErrorCodeIPNotAllowed, HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission,
		Template: "Source IP is on the token's deny list",
	},
	ErrorCodeUserBanned: {
		ID: "LZ-2303", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "This account has been disabled.",
	},
	ErrorCodeGroupAccessDenied: {
		ID: "LZ-2304", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "This account cannot use the requested lane.",
	},
	ErrorCodeChannelSelectionDenied: {
		ID: "LZ-2305", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "Only administrators can pin a request to a specific channel.",
	},
	ErrorCodeProjectAccessSuspended: {
		ID: "LZ-2306", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "Project access is suspended.",
	},
	ErrorCodeAddressNotEnabled: {
		ID: "LZ-2307", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "This API address is not enabled for your account.",
	},

	// ── LZ-3xxx · wallet and budgets ───────────────────────────────────
	ErrorCodeInsufficientQuota: {
		ID: "LZ-3101", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeInsufficientQuota, Audience: AudienceCustomer,
		Template: "Wallet balance is too low for this request. Top up to continue.",
	},
	ErrorCodeKeyBudgetExceeded: {
		ID: "LZ-3201", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeInsufficientQuota, Audience: AudienceCustomer,
		Template: "API key budget cannot cover this request's reservation; check the key budget and request output limit. No upstream request was sent.",
	},
	ErrorCodePreConsumeTokenQuotaFailed: {
		ID: "LZ-3202", HTTPStatus: http.StatusForbidden, WireStatus: http.StatusTooManyRequests, Type: ErrorTypeInsufficientQuota, Audience: AudienceCustomer,
		Template: "This API key's remaining allowance cannot cover the request. Raise the key's allowance or use another key.",
	},
	ErrorCodeMemberBudgetExceeded: {
		ID: "LZ-3301", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeInsufficientQuota, Audience: AudienceCustomer,
		Template: "Member monthly budget exceeded.",
	},
	ErrorCodeProjectBudgetExceeded: {
		ID: "LZ-3401", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeInsufficientQuota, Audience: AudienceCustomer,
		Template: "Project budget exceeded.",
	},
	ErrorCodePlatformBudgetExceeded: {
		ID: "LZ-3501", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeInsufficientQuota, Audience: AudienceCustomer,
		Template: "Platform spending budget exceeded.",
	},
	ErrorCodePricingNotConfigured: {
		ID: "LZ-3601", HTTPStatus: http.StatusForbidden, WireStatus: http.StatusServiceUnavailable, Type: ErrorTypeAPI, Audience: AudienceOperator,
		Template: "This model has no price configured for the requested lane, so it cannot be billed. Choose another model or contact the administrator.",
	},

	// ── LZ-4xxx · models and routes ────────────────────────────────────
	ErrorCodeModelNotFound: {
		ID: "LZ-4001", HTTPStatus: http.StatusNotFound, Type: ErrorTypeNotFound, Audience: AudienceCustomer,
		Template: "Model {model|requested} is not in the model catalog.",
	},
	ErrorCodeModelNotAllowed: {
		ID: "LZ-4002", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "This API key is not allowed to use model {model|requested}.",
	},
	ErrorCodeTokenNoModelAccess: {
		Public: ErrorCodeModelNotAllowed, HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission,
		Template: "Token is not authorized to access any model",
	},
	ErrorCodeTokenModelDenied: {
		Public: ErrorCodeModelNotAllowed, HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission,
		Template: "Token is not authorized to access the requested model",
	},
	ErrorCodeTokenVendorDenied: {
		ID: "LZ-4003", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "This API key is not allowed to use models from {vendor|this vendor}.",
	},
	ErrorCodeNoAvailableChannel: {
		ID: "LZ-4101", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeOverloaded, Audience: AudienceCustomer,
		Template: "No route can serve model {model|requested} right now. Retry later or choose another model.",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeChannelSelectFailed: {
		Public: ErrorCodeNoAvailableChannel, HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeOverloaded,
		Template: "Failed to select a channel for the requested model",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeInvalidChannelId: {
		ID: "LZ-4102", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest, Audience: AudienceCustomer,
		Template: "The requested channel ID is invalid.",
	},
	ErrorCodeChannelDisabled: {
		ID: "LZ-4103", HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission, Audience: AudienceCustomer,
		Template: "The requested channel is disabled.",
	},
	ErrorCodeChannelConfigInvalid: {
		ID: "LZ-4104", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeAPI, Audience: AudienceOperator,
		Template: unavailableMessage,
	},
	ErrorCodeChannelParamOverrideInvalid: {
		Public: ErrorCodeChannelConfigInvalid, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Channel has invalid parameter override config",
	},
	ErrorCodeChannelHeaderOverrideInvalid: {
		Public: ErrorCodeChannelConfigInvalid, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Channel has invalid header override config",
	},
	ErrorCodeChannelModelMappedError: {
		Public: ErrorCodeChannelConfigInvalid, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Channel model mapping failed",
	},
	ErrorCodeChannelCompatibilityInvalid: {
		Public: ErrorCodeChannelConfigInvalid, HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest,
		Template: "The selected channel has conflicting Claude compatibility settings",
	},
	ErrorCodeInvalidApiType: {
		Public: ErrorCodeChannelConfigInvalid, HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest,
		Template: "Invalid API type",
	},
	ErrorCodeChannelCompatibilityUnsupported: {
		Public: ErrorCodeProtocolBridgeUnsupported, HTTPStatus: http.StatusBadRequest, Type: ErrorTypeInvalidRequest,
		Template: "The selected channel cannot safely handle this request's mid-conversation system feature",
	},
	ErrorCodeChannelNoAvailableKeyPublic: {
		ID: "LZ-4105", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeOverloaded, Audience: AudienceOperator,
		Template: unavailableMessage,
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeChannelNoAvailableKey: {
		Public: ErrorCodeChannelNoAvailableKeyPublic, HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeOverloaded,
		Template: "Channel has no available API key configured",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodePreferredLaneUnavailable: {
		ID: "LZ-4106", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeOverloaded, Audience: AudienceCustomer,
		Template: "Model {model|requested} is not available in the lane this API key is pinned to, and automatic fallback is disabled.",
	},
	ErrorCodeGatewayTemporarilyUnavailable: {
		Public: ErrorCodeGatewayOverloaded, HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeOverloaded,
		Template: "The gateway is temporarily unavailable. Retry shortly.",
	},
	ErrorCodePromptBlocked: {
		ID: "LZ-4301", HTTPStatus: http.StatusBadRequest, Type: ErrorTypeContentPolicy, Audience: AudienceCustomer,
		Template:    "This model refused the prompt under its content policy.",
		FallbackCat: FallbackContentPolicy,
	},

	// ── LZ-5xxx · rate and capacity ────────────────────────────────────
	ErrorCodeTokenRateLimitExceeded: {
		ID: "LZ-5001", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
		Template: "This API key is sending requests too quickly. Retry after the Retry-After interval.",
	},
	ErrorCodeRequestRateLimitExceeded: {
		ID: "LZ-5002", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
		Template: "Request rate limit exceeded. Retry after the Retry-After interval.",
	},
	ErrorCodeTotalRequestRateLimitExceeded: {
		ID: "LZ-5003", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
		Template: "Total request rate limit exceeded. Retry after the Retry-After interval.",
	},
	ErrorCodeGatewayOverloaded: {
		ID: "LZ-5101", HTTPStatus: http.StatusServiceUnavailable, Type: ErrorTypeOverloaded, Audience: AudienceCustomer,
		Template: "Gateway is temporarily overloaded; retry after a delay.",
		Retry:    true, FallbackCat: FallbackNone,
	},
	ErrorCodeUpstreamRateLimited: {
		ID: "LZ-5201", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
		Template: "This model is rate-limiting requests right now. Please retry in a moment.",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeTooManyAttempts: {
		ID: "LZ-5301", HTTPStatus: http.StatusTooManyRequests, Type: ErrorTypeRateLimit, Audience: AudienceCustomer,
		Template: "Too many attempts. Wait a moment and try again.",
	},

	// ── LZ-6xxx · upstream ─────────────────────────────────────────────
	ErrorCodeUpstreamError: {
		ID: "LZ-6001", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
		Template: "The model returned an error.",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeUpstreamInvalidResponse: {
		ID: "LZ-6002", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
		Template: "The model returned a response that could not be used.",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeReadResponseBodyFailed: {
		Public: ErrorCodeUpstreamInvalidResponse, HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI,
		Template: "Failed to read upstream response body",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeBadResponse: {
		Public: ErrorCodeUpstreamInvalidResponse, HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI,
		Template: "Upstream returned a malformed response",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeBadResponseBody: {
		Public: ErrorCodeUpstreamInvalidResponse, HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI,
		Template: "Upstream returned an unparseable body",
	},
	ErrorCodeEmptyResponse: {
		Public: ErrorCodeUpstreamInvalidResponse, HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI,
		Template: "Upstream returned an empty response",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeUpstreamStreamFailed: {
		ID: "LZ-6003", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceCustomer,
		Template: "The model stopped the stream with an error. Check any partial output before retrying.",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeUpstreamTimeout: {
		ID: "LZ-6101", HTTPStatus: http.StatusGatewayTimeout, Type: ErrorTypeTimeout, Audience: AudienceCustomer,
		Template: "This model did not respond in time. Please retry.",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeChannelResponseTimeExceeded: {
		Public: ErrorCodeUpstreamTimeout, HTTPStatus: http.StatusGatewayTimeout, Type: ErrorTypeTimeout,
		Template: "Channel exceeded the maximum response time threshold",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeUpstreamNetworkError: {
		ID: "LZ-6201", HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPIConnection, Audience: AudienceCustomer,
		Template: "The connection to this model failed. Please retry.",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeAPIConnectionError: {
		Public: ErrorCodeUpstreamNetworkError, HTTPStatus: http.StatusBadGateway, Type: ErrorTypeAPIConnection,
		Template: "Failed to reach upstream API",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeUpstreamOverloaded: {
		ID: "LZ-6202", HTTPStatus: http.StatusBadGateway, WireStatus: http.StatusServiceUnavailable, Type: ErrorTypeOverloaded, Audience: AudienceCustomer,
		Template: "This model is busy right now. Please retry in a moment.",
		Retry:    true, FallbackCat: FallbackGeneral,
	},
	ErrorCodeUpstreamAuthFailed: {
		ID: "LZ-6301", HTTPStatus: http.StatusBadGateway, WireStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
		Template: unavailableMessage,
	},
	ErrorCodeChannelInvalidKey: {
		Public: ErrorCodeUpstreamAuthFailed, HTTPStatus: http.StatusForbidden, Type: ErrorTypePermission,
		Template: "Channel API key is invalid or unauthorized by upstream",
	},
	ErrorCodeUpstreamBillingFailed: {
		ID: "LZ-6302", HTTPStatus: http.StatusBadGateway, WireStatus: http.StatusBadGateway, Type: ErrorTypeAPI, Audience: AudienceOperator,
		Template: unavailableMessage,
		Retry:    true, FallbackCat: FallbackGeneral,
	},

	// ── LZ-9xxx · internal ─────────────────────────────────────────────
	ErrorCodeInternalError: {
		ID: "LZ-9001", HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI, Audience: AudienceInternal,
		Template: "The service could not complete the request. Contact support with the request ID if it persists.",
	},
	ErrorCodeNotImplemented: {
		ID: "LZ-9002", HTTPStatus: http.StatusNotImplemented, Type: ErrorTypeAPI, Audience: AudienceCustomer,
		Template: "This operation is not implemented yet.",
	},
	ErrorCodeJsonMarshalFailed: {
		Public: ErrorCodeInternalError, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Failed to serialize JSON",
	},
	ErrorCodeGetChannelFailed: {
		Public: ErrorCodeInternalError, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Failed to resolve a channel",
	},
	ErrorCodeGenRelayInfoFailed: {
		Public: ErrorCodeInternalError, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Failed to build relay context",
	},
	ErrorCodeRateLimitCheckFailed: {
		Public: ErrorCodeInternalError, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Rate limit check failed",
	},
	ErrorCodeQueryDataError: {
		Public: ErrorCodeInternalError, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Database query failed",
	},
	ErrorCodeUpdateDataError: {
		Public: ErrorCodeInternalError, HTTPStatus: http.StatusInternalServerError, Type: ErrorTypeAPI,
		Template: "Database update failed",
	},
}
