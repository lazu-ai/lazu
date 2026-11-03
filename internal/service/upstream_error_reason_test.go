package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/types"
)

func TestRelayErrorHandlerSafeReasons(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"unknown type", `{"error":{"type":"ValidationException","message":"Unexpected beta flag: mid-conversation-system-2026-04-07; secret=customer-message"}}`, "mid-conversation-system-2026-04-07"},
		{"top level", `{"message":"ValidationException: Unexpected beta flag: mid-conversation-system-2026-04-07"}`, "mid-conversation-system-2026-04-07"},
		{"nested", `{"error":{"error":{"message":"thinking enabled not supported; use adaptive"}}}`, "adaptive thinking"},
		{"arbitrary message", `{"message":"customer-message https://private.example sk-secret"}`, "The request body is malformed"},
		{"trusted type untrusted content", `{"error":{"type":"invalid_request_error","message":"customer-message","code":"customer-message","param":"customer-message","details":{"echo":"customer-message"}}}`, "The request body is malformed"},
		{"html", `<html>customer-message</html>`, "The request body is malformed"},
		{"long", `{"message":"` + strings.Repeat("customer-message", 10000) + `"}`, "The request body is malformed"},
		{"field", `{"message":"max_tokens is invalid: customer-message"}`, "max_tokens configuration"},
		{"chat tools with reasoning", `{"error":{"message":"Function tools with reasoning_effort are not supported for gpt-6-sol in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'. customer-message"}}`, "use /v1/responses to call tools"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := RelayErrorHandler(nil, &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(tc.body))}, true)
			if e == nil || e.GetErrorCode() != types.ErrorCodeBadRequestBody || e.StatusCode != 400 {
				t.Fatalf("classification changed: %v", e)
			}
			if strings.Contains(tc.want, "configuration") || strings.Contains(tc.want, "mid-conversation") || strings.Contains(tc.want, "adaptive thinking") {
				if e.ToClaudeError().Type != types.ErrorTypeInvalidRequest {
					t.Fatal("safe upstream error lost the Anthropic error type")
				}
			}
			payload, _ := json.Marshal(e.ToOpenAIError())
			claude, _ := json.Marshal(e.ToClaudeError())
			for _, text := range []string{string(payload), string(claude), e.Error()} {
				if strings.Contains(text, "customer-message") || strings.Contains(text, "private.example") {
					t.Fatalf("echo leaked: %s", text)
				}
				if !strings.Contains(text, tc.want) {
					t.Fatalf("want %q in %s", tc.want, text)
				}
			}
			detail := e.ToOpenAIError().Details.(map[string]any)
			if detail["upstream_status"] != 400 || detail["attempt_provider_attempted"] != true {
				t.Fatalf("missing evidence: %v", detail)
			}
		})
	}
}

type countedErrorBody struct {
	reads  int
	closed bool
}

func (b *countedErrorBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.reads += len(p)
	return len(p), nil
}
func (b *countedErrorBody) Close() error { b.closed = true; return nil }
func TestRelayErrorReadBound(t *testing.T) {
	b := &countedErrorBody{}
	e := RelayErrorHandler(nil, &http.Response{StatusCode: 503, Body: b}, false)
	if b.reads != maxUpstreamErrorBytes+1 || !b.closed {
		t.Fatalf("unbounded/unclosed body: %+v", b)
	}
	if e.GetErrorCode() != types.ErrorCodeUpstreamOverloaded || e.StatusCode != types.StatusForCode(types.ErrorCodeUpstreamOverloaded, 502) {
		t.Fatalf("wrong overload mapping: %+v", e)
	}
}

// Claude Code acts on these by their words: it compacts on "prompt is too
// long" and resends without a field or beta the API names.
func TestRelayErrorHandlerClaudeCodeActionableText(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		code             types.ErrorCode
	}{
		{"anthropic too long", `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 205000 tokens > 200000 maximum"}}`, "prompt is too long: 205000 tokens > 200000 maximum", types.ErrorCodeContextLengthExceeded},
		{"openai too long", `{"error":{"type":"invalid_request_error","message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 130512 tokens. customer-message"}}`, "prompt is too long: 130512 tokens > 128000 maximum", types.ErrorCodeContextLengthExceeded},
		{"other context wording", `{"error":{"message":"Invalid request: messages exceed maximum context customer-message"}}`, "prompt is too long", types.ErrorCodeContextLengthExceeded},
		{"bedrock too long", `{"message":"Input is too long for requested model. customer-message"}`, "prompt is too long", types.ErrorCodeContextLengthExceeded},
		{"extra input", `{"type":"error","error":{"type":"invalid_request_error","message":"context_management: Extra inputs are not permitted"}}`, "context_management: Extra inputs are not permitted", types.ErrorCodeBadRequestBody},
		{"extra input with echo", `{"type":"error","error":{"type":"invalid_request_error","message":"customer-message: Extra inputs are not permitted https://private.example"}}`, "The request body is malformed", types.ErrorCodeBadRequestBody},
		{"beta names", `{"type":"error","error":{"type":"invalid_request_error","message":"Unexpected value(s) ` + "`context-1m-2025-08-07`, `customer-message`" + ` for the ` + "`anthropic-beta`" + ` header. Please consult our documentation."}}`, "Unexpected value(s) `context-1m-2025-08-07` for the `anthropic-beta` header", types.ErrorCodeBadRequestBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := RelayErrorHandler(nil, &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(tc.body))}, true)
			if e == nil || e.GetErrorCode() != tc.code {
				t.Fatalf("code = %v, want %s", e, tc.code)
			}
			if e.ToClaudeError().Type != types.ErrorTypeInvalidRequest {
				t.Fatalf("type = %s", e.ToClaudeError().Type)
			}
			claude, _ := json.Marshal(e.ToClaudeError())
			payload, _ := json.Marshal(e.ToOpenAIError())
			for _, text := range []string{string(claude), string(payload)} {
				if strings.Contains(text, "customer-message") || strings.Contains(text, "private.example") {
					t.Fatalf("echo leaked: %s", text)
				}
			}
			for _, message := range []string{e.ToClaudeError().Message, e.ToOpenAIError().Message} {
				if !strings.Contains(message, tc.want) {
					t.Fatalf("want %q in %q", tc.want, message)
				}
			}
		})
	}
}
