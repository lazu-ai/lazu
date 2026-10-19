package types

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

var lzNumber = regexp.MustCompile(`^LZ-[1-9]\d{3}$`)

// TestCatalogIsWellFormed pins the rules every row must follow. A row is
// either public (an LZ number, an audience, a type) or internal (Public names
// a public row). Numbers are unique, templates are English, and a Public
// pointer always lands on a public row.
func TestCatalogIsWellFormed(t *testing.T) {
	t.Parallel()
	allowedTypes := map[string]bool{
		ErrorTypeInvalidRequest: true, ErrorTypeAuthentication: true, ErrorTypePermission: true,
		ErrorTypeNotFound: true, ErrorTypeRateLimit: true, ErrorTypeAPI: true,
		ErrorTypeOverloaded: true, ErrorTypeAPIConnection: true, ErrorTypeTimeout: true,
		ErrorTypeContentPolicy: true, ErrorTypeInsufficientQuota: true, ErrorTypeConflict: true,
	}
	allowedFallback := map[FallbackCategory]bool{
		"": true, FallbackNone: true, FallbackGeneral: true, FallbackContentPolicy: true, FallbackContextWindow: true,
	}
	seen := map[string]ErrorCode{}
	for code, spec := range Catalog {
		if strings.Contains(string(code), " ") || strings.ToLower(string(code)) != string(code) {
			t.Errorf("%q: codes are lower snake_case", code)
		}
		for _, r := range spec.Template {
			if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
				t.Errorf("%q: template must be English: %s", code, spec.Template)
				break
			}
		}
		if spec.Template == "" {
			t.Errorf("%q: empty template", code)
		}
		if !allowedTypes[spec.Type] {
			t.Errorf("%q: type %q is not a Lazu error type", code, spec.Type)
		}
		if !allowedFallback[spec.FallbackCat] {
			t.Errorf("%q: invalid fallback category %q", code, spec.FallbackCat)
		}
		if spec.HTTPStatus < 400 || spec.HTTPStatus > 599 {
			t.Errorf("%q: HTTPStatus %d is not an error status", code, spec.HTTPStatus)
		}
		if spec.Public != "" {
			if spec.ID != "" {
				t.Errorf("%q: internal row must not carry a number (%s)", code, spec.ID)
			}
			target, ok := Catalog[spec.Public]
			if !ok || target.Public != "" || target.ID == "" {
				t.Errorf("%q: Public %q is not a public row", code, spec.Public)
			}
			continue
		}
		if !lzNumber.MatchString(spec.ID) {
			t.Errorf("%q: public row needs an LZ-#### number, got %q", code, spec.ID)
		}
		if other, dup := seen[spec.ID]; dup {
			t.Errorf("%s is used by both %q and %q", spec.ID, code, other)
		}
		seen[spec.ID] = code
		switch spec.Audience {
		case AudienceCustomer, AudienceOperator, AudienceInternal:
		default:
			t.Errorf("%q: public row needs an audience", code)
		}
	}
}

func TestRenderTemplate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		template string
		details  map[string]any
		want     string
	}{
		{"Model {model|requested} is missing.", map[string]any{"model": "gpt-4o"}, "Model gpt-4o is missing."},
		{"Model {model|requested} is missing.", nil, "Model requested is missing."},
		{"Model {model|requested} is missing.", map[string]any{"model": ""}, "Model requested is missing."},
		{"Wait {seconds} s.", map[string]any{"seconds": 30}, "Wait 30 s."},
		{"Plain text.", map[string]any{"x": 1}, "Plain text."},
		{"{reason|Default sentence.}", map[string]any{"reason": "bad json"}, "bad json"},
	}
	for _, tc := range cases {
		if got := RenderTemplate(tc.template, tc.details); got != tc.want {
			t.Errorf("RenderTemplate(%q) = %q, want %q", tc.template, got, tc.want)
		}
	}
}

func TestRenderWireStatusRules(t *testing.T) {
	t.Parallel()

	// Public row without WireStatus honours the runtime status: an upstream
	// 404 stays a 404.
	if w := RenderWire(ErrorCodeUpstreamError, nil, http.StatusNotFound); w.Status != http.StatusNotFound || w.Code != ErrorCodeUpstreamError {
		t.Fatalf("upstream pass-through: %+v", w)
	}
	// Internal row resolves to its public row, including the public status.
	w := RenderWire(ErrorCodeTokenIpDenied, nil, 0)
	if w.Code != ErrorCodeIPNotAllowed || w.Status != http.StatusForbidden || w.ID != "LZ-2302" {
		t.Fatalf("internal→public: %+v", w)
	}
	// Lazu's own provider key rejected upstream must not look like the
	// caller's credentials: never 401/403 on the wire.
	w = RenderWire(ErrorCodeChannelInvalidKey, nil, http.StatusUnauthorized)
	if w.Status != http.StatusBadGateway || w.Type != ErrorTypeAPI || w.Code != ErrorCodeUpstreamAuthFailed {
		t.Fatalf("channel invalid key leaked caller-auth semantics: %+v", w)
	}
	// Server-side status is untouched for retry and breaker decisions.
	if StatusForCode(ErrorCodeChannelInvalidKey, 0) != http.StatusForbidden {
		t.Fatal("internal status of channel:invalid_key changed")
	}
	// WireStatus on a public row wins over the runtime status.
	if w := RenderWire(ErrorCodeUpstreamOverloaded, nil, http.StatusBadGateway); w.Status != http.StatusServiceUnavailable {
		t.Fatalf("upstream_overloaded wire status: %d", w.Status)
	}
	if w := RenderWire(ErrorCodePricingNotConfigured, nil, 0); w.Status != http.StatusServiceUnavailable || w.Type != ErrorTypeAPI {
		t.Fatalf("pricing_not_configured: %+v", w)
	}
	if StatusForCode(ErrorCodePricingNotConfigured, 0) != http.StatusForbidden {
		t.Fatal("internal status of pricing_not_configured changed")
	}
	// Unknown codes never reach the wire as themselves.
	if w := RenderWire("nonexistent_code_xyz", nil, 418); w.Code != ErrorCodeInternalError || w.Status != http.StatusInternalServerError || w.Known {
		t.Fatalf("unknown code: %+v", w)
	}
}

func TestRenderErrorUsesPublicFace(t *testing.T) {
	t.Parallel()
	status, errType, msg, code := RenderError(ErrorCodeTokenModelDenied, map[string]any{"model": "gpt-4o"})
	if status != http.StatusForbidden || errType != ErrorTypePermission || code != "model_not_allowed" {
		t.Fatalf("got %d %s %s", status, errType, code)
	}
	if msg != "This API key is not allowed to use model gpt-4o." {
		t.Fatalf("message: %q", msg)
	}
	if got := ErrorDocsURL("LZ-3101"); got != "https://lazu.ai/docs/errors#lz-3101" {
		t.Fatalf("docs url: %s", got)
	}
}

func TestRetryAndFallbackHelpers(t *testing.T) {
	t.Parallel()
	if !ShouldRetry(ErrorCodeUpstreamRateLimited) {
		t.Fatal("expected upstream_rate_limited to be retryable")
	}
	if ShouldRetry(ErrorCodeClientCanceled) {
		t.Fatal("expected client_canceled not to be retryable")
	}
	if GetFallbackCategory(ErrorCodePromptBlocked) != FallbackContentPolicy {
		t.Fatalf("expected prompt_blocked content-policy fallback")
	}
	if GetFallbackCategory(ErrorCodeContextLengthExceeded) != FallbackContextWindow {
		t.Fatalf("expected context_length_exceeded context-window fallback")
	}
	if GetFallbackCategory(ErrorCodeInvalidRequest) != FallbackNone {
		t.Fatalf("expected invalid_request no fallback")
	}
}

func TestNewOpenAIErrorPreservesNestedLazuError(t *testing.T) {
	t.Parallel()

	canceled := NewError(
		errors.New("request canceled by client"),
		ErrorCodeClientCanceled,
		ErrOptionWithSkipRetry(),
		ErrOptionWithNoRecordErrorLog(),
	)
	wrapped := NewOpenAIError(
		canceled,
		ErrorCodeAPIConnectionError,
		http.StatusInternalServerError,
	)

	if wrapped != canceled {
		t.Fatal("expected the nested LazuError to be preserved")
	}
	if wrapped.GetErrorCode() != ErrorCodeClientCanceled {
		t.Fatalf("error code=%q want %q", wrapped.GetErrorCode(), ErrorCodeClientCanceled)
	}
	if wrapped.StatusCode != StatusClientClosedRequest {
		t.Fatalf("status=%d want %d", wrapped.StatusCode, StatusClientClosedRequest)
	}
	if !IsSkipRetryError(wrapped) {
		t.Fatal("expected client cancellation to skip retry")
	}
	if IsRecordErrorLog(wrapped) {
		t.Fatal("expected client cancellation not to create an upstream error log")
	}
}

func TestToOpenAIErrorSpeaksForInternalRows(t *testing.T) {
	t.Parallel()
	err := NewError(errors.New("上游返回中文错误"), ErrorCodeBadResponseBody).ToOpenAIError()
	if err.Message != "The model returned a response that could not be used." {
		t.Fatalf("unexpected message: %q", err.Message)
	}
	for _, r := range err.Message {
		if unicode.Is(unicode.Han, r) {
			t.Fatalf("message leaked Chinese rune %q", r)
		}
	}
	if err.Type != ErrorTypeAPI || err.Code != "upstream_invalid_response" || err.Lazu != "LZ-6002" {
		t.Fatalf("wire face: %+v", err)
	}
	if err.Docs != "https://lazu.ai/docs/errors#lz-6002" {
		t.Fatalf("docs: %s", err.Docs)
	}
}

func TestInternalCausesStayOnTheServer(t *testing.T) {
	t.Parallel()
	err := NewError(errors.New(`pq: relation "users" does not exist`), ErrorCodeQueryDataError)
	openAI := err.ToOpenAIError()
	claude := err.ToClaudeError()
	for _, text := range []string{openAI.Message, claude.Message} {
		if strings.Contains(text, "relation") || strings.Contains(text, "Database") {
			t.Fatalf("internal cause leaked: %q", text)
		}
	}
	if openAI.Code != "internal_error" || claude.Code != "internal_error" || claude.Lazu != "LZ-9001" {
		t.Fatalf("internal row face: %+v %+v", openAI, claude)
	}
}

func TestUpstreamProviderCodeIsKept(t *testing.T) {
	t.Parallel()
	err := WithOpenAIError(OpenAIError{Type: ErrorTypeInvalidRequest, Code: "context_length_exceeded_upstream", Message: "too long"}, http.StatusBadRequest)
	got := err.ToOpenAIError()
	if got.Code != "context_length_exceeded_upstream" || got.Message != "too long" || got.Type != ErrorTypeInvalidRequest {
		t.Fatalf("upstream pass-through changed: %+v", got)
	}
	if err.WireStatus() != http.StatusBadRequest {
		t.Fatalf("status: %d", err.WireStatus())
	}
}

func TestClaudeErrorUsesAnthropicTypes(t *testing.T) {
	t.Parallel()
	cases := map[ErrorCode]string{
		ErrorCodeInsufficientQuota:      "billing_error",
		ErrorCodeUpstreamOverloaded:     ErrorTypeOverloaded,
		ErrorCodeUpstreamTimeout:        ErrorTypeAPI,
		ErrorCodePromptBlocked:          ErrorTypeInvalidRequest,
		ErrorCodeRequestBodyTooLarge:    "request_too_large",
		ErrorCodeInvalidAPIKey:          ErrorTypeAuthentication,
		ErrorCodeChannelInvalidKey:      ErrorTypeAPI,
		ErrorCodeTokenRateLimitExceeded: ErrorTypeRateLimit,
	}
	for code, want := range cases {
		got := NewError(errors.New("x"), code).ToClaudeError()
		if got.Type != want {
			t.Errorf("%s: claude type %q, want %q", code, got.Type, want)
		}
		if got.Type == "lazu_error" {
			t.Errorf("%s: lazu_error is not an Anthropic type", code)
		}
	}
}

func TestKeyBudgetDiagnosticKeepsStatusAndExplainsReservation(t *testing.T) {
	err := NewError(errors.New("internal budget failure"), ErrorCodeKeyBudgetExceeded, ErrOptionWithSkipRetry())
	if err.StatusCode != http.StatusTooManyRequests || !IsSkipRetryError(err) {
		t.Fatal("budget policy changed")
	}
	if !strings.Contains(err.ToOpenAIError().Message, "reservation") || !strings.Contains(err.ToClaudeError().Message, "No upstream request was sent") {
		t.Fatal("missing actionable budget diagnostic")
	}
}

func TestAPIErrorComparesByCode(t *testing.T) {
	t.Parallel()
	sentinel := Err(ErrorCodeResourceNotFound)
	got := WrapErr(errors.New("record not found"), ErrorCodeResourceNotFound, D{"resource": "user"})
	if !errors.Is(got, sentinel) {
		t.Fatal("errors.Is should match by code")
	}
	if ErrCode(got) != ErrorCodeResourceNotFound {
		t.Fatal("ErrCode")
	}
	if w := got.Wire(); w.Message != "The requested user was not found." || w.Status != http.StatusNotFound {
		t.Fatalf("wire: %+v", w)
	}
	// The innermost coded error wins over a later wrap.
	outer := WrapErr(got, ErrorCodeInternalError)
	if outer.Code != ErrorCodeResourceNotFound {
		t.Fatal("WrapErr must keep the inner code")
	}
}

func TestPublicDetailsRespectResolvedAudience(t *testing.T) {
	for _, code := range []ErrorCode{ErrorCodeUpstreamAuthFailed, ErrorCodeChannelInvalidKey, ErrorCodeUpstreamBillingFailed, ErrorCodeInternalError, ErrorCodeQueryDataError, "uncatalogued_local_error"} {
		t.Run(string(code), func(t *testing.T) {
			err := NewError(errors.New("private cause"), code, ErrOptionWithDetails(D{"channel_type": 14, "reason": "private diagnostic"}))
			if err.ToOpenAIError().Details != nil || err.ToClaudeError().Details != nil {
				t.Fatal("non-customer details leaked")
			}
		})
	}
	err := NewError(nil, ErrorCodeTokenModelDenied, ErrOptionWithDetails(D{"model": "example-model"}))
	if err.ToOpenAIError().Details == nil || err.ToClaudeError().Details == nil {
		t.Fatal("customer guidance was lost")
	}
}
