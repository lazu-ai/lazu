package channel

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	common2 "github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

// Shaped like a Claude Code 2.1.276 request: cached system blocks, a tool whose
// schema mentions cache_control as caller data, a trailing system turn, and the
// default keep-all context_management.
const bedrockCCBody = `{"model":"claude-sonnet-5","messages":[{"role":"user","content":[{"type":"text","text":"Read a.txt"}]},{"role":"system","content":[{"type":"text","text":"<system-reminder>env</system-reminder>","cache_control":{"type":"ephemeral"}}]}],"system":[{"type":"text","text":"You are a Claude agent","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"Write","input_schema":{"type":"object","properties":{"cache_control":{"type":"string"}}}}],"metadata":{"user_id":"u"},"max_tokens":64000,"thinking":{"type":"adaptive","display":"omitted"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"output_config":{"effort":"high"},"stream":true}`

const bedrockCCBodyWithoutContext = `{"model":"claude-sonnet-5","messages":[{"role":"user","content":[{"type":"text","text":"Read a.txt"}]},{"role":"system","content":[{"type":"text","text":"<system-reminder>env</system-reminder>","cache_control":{"type":"ephemeral"}}]}],"system":[{"type":"text","text":"You are a Claude agent","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"Write","input_schema":{"type":"object","properties":{"cache_control":{"type":"string"}}}}],"metadata":{"user_id":"u"},"max_tokens":64000,"thinking":{"type":"adaptive","display":"omitted"},"output_config":{"effort":"high"},"stream":true}`

const bedrockCCBetas = "claude-code-20250219,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24"

func bedrockContext(body string, scope, context []string) (*gin.Context, *relaycommon.RelayInfo, *http.Request) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("anthropic-beta", bedrockCCBetas)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeAnthropic, UpstreamModelName: "claude-sonnet-5", ChannelId: 48,
		ChannelOtherSettings: dto.ChannelOtherSettings{
			ClaudeOmitUnusedCacheScopeBetaModels:     scope,
			ClaudeOmitKeepAllContextManagementModels: context,
		},
	}}
	req := httptest.NewRequest(http.MethodPost, "http://upstream.test/v1/messages", strings.NewReader(body))
	req.Header = c.Request.Header.Clone()
	return c, info, req
}

func wireBody(t *testing.T, req *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if req.ContentLength != int64(len(body)) {
		t.Fatalf("content length %d, body %d", req.ContentLength, len(body))
	}
	return string(body)
}

func TestClaudeBedrockCompatibilityClaudeCodeRequest(t *testing.T) {
	sonnet := []string{"claude-sonnet-5"}
	for _, tc := range []struct {
		name           string
		scope, context []string
		wantBetas      string
		wantBody       string
	}{
		{"both actions", sonnet, sonnet,
			"claude-code-20250219,interleaved-thinking-2025-05-14,mid-conversation-system-2026-04-07,effort-2025-11-24",
			bedrockCCBodyWithoutContext},
		{"scope only", sonnet, nil,
			"claude-code-20250219,interleaved-thinking-2025-05-14,context-management-2025-06-27,mid-conversation-system-2026-04-07,effort-2025-11-24",
			bedrockCCBody},
		{"context only", nil, sonnet,
			"claude-code-20250219,interleaved-thinking-2025-05-14,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24",
			bedrockCCBodyWithoutContext},
		{"disabled", nil, nil, bedrockCCBetas, bedrockCCBody},
		{"other model", []string{"claude-opus-5"}, []string{"claude-opus-5"}, bedrockCCBetas, bedrockCCBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info, req := bedrockContext(bedrockCCBody, tc.scope, tc.context)
			originalHeaders := c.Request.Header.Clone()
			if err := applyClaudeBedrockCompatibility(c, req, info); err != nil {
				t.Fatal(err)
			}
			if got := req.Header.Get("anthropic-beta"); got != tc.wantBetas {
				t.Fatalf("betas = %s", got)
			}
			if got := wireBody(t, req); got != tc.wantBody {
				t.Fatalf("body = %s", got)
			}
			if !reflect.DeepEqual(c.Request.Header, originalHeaders) {
				t.Fatal("client headers mutated")
			}
			if original, _ := common2.GetRequestBody(c); string(original) != bedrockCCBody {
				t.Fatal("client body mutated")
			}
		})
	}
}

func TestClaudeBedrockCompatibilityRefusesUsedFeatures(t *testing.T) {
	sonnet := []string{"claude-sonnet-5"}
	for _, tc := range []struct {
		name, body, reason string
	}{
		{"cache scope in system", strings.Replace(bedrockCCBody, `"You are a Claude agent","cache_control":{"type":"ephemeral"}`, `"You are a Claude agent","cache_control":{"type":"ephemeral","scope":"workspace"}`, 1), "cache_scope_used"},
		{"cache scope in tool result", strings.Replace(bedrockCCBody, `{"type":"text","text":"Read a.txt"}`, `{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","scope":"org"}}]}`, 1), "cache_scope_used"},
		{"non-object cache control", strings.Replace(bedrockCCBody, `"cache_control":{"type":"ephemeral"}}],"tools"`, `"cache_control":"ephemeral"}],"tools"`, 1), "ambiguous_cache_control"},
		{"clear tool uses", strings.Replace(bedrockCCBody, `{"type":"clear_thinking_20251015","keep":"all"}`, `{"type":"clear_tool_uses_20250919"}`, 1), "context_management_used"},
		{"keep last turns", strings.Replace(bedrockCCBody, `"keep":"all"`, `"keep":{"type":"thinking_turns","value":1}`, 1), "context_management_used"},
		{"extra edit field", strings.Replace(bedrockCCBody, `"keep":"all"`, `"keep":"all","trigger":{"type":"input_tokens","value":1}`, 1), "context_management_used"},
		{"extra context field", strings.Replace(bedrockCCBody, `{"edits":[`, `{"compaction":true,"edits":[`, 1), "context_management_used"},
		{"context not object", strings.Replace(bedrockCCBody, `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`, `"all"`, 1), "ambiguous_context_management"},
		{"duplicate context key", strings.Replace(bedrockCCBody, `"stream":true`, `"stream":true,"context_management":{}`, 1), "ambiguous_original_body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info, req := bedrockContext(tc.body, sonnet, sonnet)
			err := applyClaudeBedrockCompatibility(c, req, info)
			var le *types.LazuError
			if !errors.As(err, &le) || !strings.Contains(le.Error(), tc.reason) || le.StatusCode != 400 || !types.IsSkipRetryError(le) {
				t.Fatalf("want rejection %s, got %v", tc.reason, err)
			}
			if common2.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) {
				t.Fatal("local failure marked provider attempted")
			}
		})
	}
}

func TestClaudeBedrockCompatibilityScopeAndConflicts(t *testing.T) {
	sonnet := []string{"claude-sonnet-5"}
	t.Run("context beta without body field", func(t *testing.T) {
		c, info, req := bedrockContext(bedrockCCBodyWithoutContext, nil, sonnet)
		if err := applyClaudeBedrockCompatibility(c, req, info); err != nil {
			t.Fatal(err)
		}
		if hasBeta(req.Header, contextManagementBeta) {
			t.Fatal("unused context beta survived")
		}
		if wireBody(t, req) != bedrockCCBodyWithoutContext {
			t.Fatal("body changed")
		}
	})
	t.Run("scope beta absent", func(t *testing.T) {
		c, info, req := bedrockContext(bedrockCCBody, sonnet, nil)
		req.Header.Set("anthropic-beta", "claude-code-20250219")
		if err := applyClaudeBedrockCompatibility(c, req, info); err != nil {
			t.Fatal(err)
		}
		if req.Header.Get("anthropic-beta") != "claude-code-20250219" || wireBody(t, req) != bedrockCCBody {
			t.Fatal("request changed without the beta")
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*relaycommon.RelayInfo, *http.Request)
		reason string
	}{
		{"wildcard", func(i *relaycommon.RelayInfo, r *http.Request) {
			i.ChannelOtherSettings.ClaudeOmitUnusedSystemBetaModels = []string{"claude-*"}
		}, "invalid_model_scope"},
		{"scope header override", func(i *relaycommon.RelayInfo, r *http.Request) {
			i.HeadersOverride = map[string]interface{}{"Anthropic-Beta": cacheScopeBeta}
		}, "header_override_conflict"},
		{"context header override", func(i *relaycommon.RelayInfo, r *http.Request) {
			i.HeadersOverride = map[string]interface{}{"anthropic-beta": contextManagementBeta}
		}, "header_override_conflict"},
		{"passthrough model mismatch", func(i *relaycommon.RelayInfo, r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(strings.Replace(bedrockCCBody, "claude-sonnet-5", "claude-sonnet-5-mapped", 1)))
		}, "outbound_model_mismatch"},
		{"outbound introduced scope", func(i *relaycommon.RelayInfo, r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(strings.Replace(bedrockCCBody, `"cache_control":{"type":"ephemeral"}`, `"cache_control":{"type":"ephemeral","scope":"x"}`, 1)))
		}, "cache_scope_used"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info, req := bedrockContext(bedrockCCBody, sonnet, sonnet)
			tc.change(info, req)
			err := applyClaudeBedrockCompatibility(c, req, info)
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("want %s, got %v", tc.reason, err)
			}
		})
	}
	t.Run("different protocol", func(t *testing.T) {
		c, info, req := bedrockContext(bedrockCCBody, sonnet, sonnet)
		info.RelayFormat = types.RelayFormatOpenAI
		if err := applyClaudeBedrockCompatibility(c, req, info); err != nil || req.Header.Get("anthropic-beta") != bedrockCCBetas {
			t.Fatalf("non-Claude relay touched: %v", err)
		}
	})
}

type bedrockHTTPAdaptor struct {
	failingWSSAdaptor
	url string
}

func (a bedrockHTTPAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) { return a.url, nil }
func (a bedrockHTTPAdaptor) SetupRequestHeader(c *gin.Context, h *http.Header, i *relaycommon.RelayInfo) error {
	SetupApiRequestHeader(i, c, h)
	h.Set("x-api-key", "upstream-only")
	return nil
}

func TestClaudeBedrockCompatibilityFinalTransport(t *testing.T) {
	var gotBetas, gotBody string
	var gotLength int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBetas = r.Header.Get("anthropic-beta")
		gotLength, _ = strconv.ParseInt(r.Header.Get("Content-Length"), 10, 64)
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
	}))
	defer server.Close()
	sonnet := []string{"claude-sonnet-5"}
	c, info, _ := bedrockContext(bedrockCCBody, sonnet, sonnet)
	resp, err := DoApiRequest(bedrockHTTPAdaptor{url: server.URL}, c, info, strings.NewReader(bedrockCCBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if hasBeta(http.Header{"Anthropic-Beta": {gotBetas}}, cacheScopeBeta) || hasBeta(http.Header{"Anthropic-Beta": {gotBetas}}, contextManagementBeta) {
		t.Fatalf("betas reached upstream: %s", gotBetas)
	}
	if gotBody != bedrockCCBodyWithoutContext || gotLength != int64(len(bedrockCCBodyWithoutContext)) {
		t.Fatalf("wire body %d bytes (length header %d): %s", len(gotBody), gotLength, gotBody)
	}
	if c.Request.Header.Get("anthropic-beta") != bedrockCCBetas {
		t.Fatal("next attempt would lose the caller's betas")
	}
}

// Shaped like turn 2 of a Claude Code 2.1.276 session: every system turn sits
// right after a user turn (text or tool results), as a string or text blocks.
const foldTurnBody = `{"model":"claude-opus-5","messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>a</system-reminder>"},{"type":"text","text":"fix calc.py"}]},{"role":"system","content":"<system-reminder>env & date</system-reminder>"},{"role":"assistant","content":[{"type":"text","text":"reading"},{"type":"tool_use","id":"t1","name":"Read","input":{"role":"system"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"}]},{"role":"system","content":[{"type":"text","text":"<system-reminder>todo</system-reminder>","cache_control":{"type":"ephemeral"}}]}],"max_tokens":64000,"stream":true}`

const foldTurnWant = `{"model":"claude-opus-5","messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>a</system-reminder>"},{"type":"text","text":"fix calc.py"},{"type":"text","text":"<system-reminder>env & date</system-reminder>"}]},{"role":"assistant","content":[{"type":"text","text":"reading"},{"type":"tool_use","id":"t1","name":"Read","input":{"role":"system"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"},{"type":"text","text":"<system-reminder>todo</system-reminder>","cache_control":{"type":"ephemeral"}}]}],"max_tokens":64000,"stream":true}`

const foldTurnBetas = "claude-code-20250219,thinking-token-count-2026-05-13,mid-conversation-system-2026-04-07,mid-conversation-tool-changes-2026-07-01,effort-2025-11-24"

func foldContext(body string) (*gin.Context, *relaycommon.RelayInfo, *http.Request) {
	c, info, _ := bedrockContext(body, nil, nil)
	c.Request.Header.Set("anthropic-beta", foldTurnBetas)
	info.UpstreamModelName = "claude-opus-5"
	opus := []string{"claude-opus-5"}
	info.ChannelOtherSettings = dto.ChannelOtherSettings{ClaudeFoldSystemTurnsModels: opus, ClaudeOmitThinkingTokenCountBetaModels: opus}
	req := httptest.NewRequest(http.MethodPost, "http://upstream.test/v1/messages", strings.NewReader(body))
	req.Header = c.Request.Header.Clone()
	return c, info, req
}

func TestClaudeBedrockCompatibilityFoldsSystemTurns(t *testing.T) {
	c, info, req := foldContext(foldTurnBody)
	if err := applyClaudeBedrockCompatibility(c, req, info); err != nil {
		t.Fatal(err)
	}
	if got := wireBody(t, req); got != foldTurnWant {
		t.Fatalf("body = %s", got)
	}
	if got := req.Header.Get("anthropic-beta"); got != "claude-code-20250219,effort-2025-11-24" {
		t.Fatalf("betas = %s", got)
	}
	if original, _ := common2.GetRequestBody(c); string(original) != foldTurnBody {
		t.Fatal("client body mutated")
	}

	// Deterministic: the same history folds to the same bytes, so the cached
	// prefix stays stable from turn to turn.
	c2, info2, req2 := foldContext(foldTurnBody)
	if err := applyClaudeBedrockCompatibility(c2, req2, info2); err != nil || wireBody(t, req2) != foldTurnWant {
		t.Fatal("fold is not deterministic")
	}

	t.Run("no system turns keeps body, drops unused betas", func(t *testing.T) {
		body := `{"model":"claude-opus-5","messages":[{"role":"user","content":"hi"}]}`
		c, info, req := foldContext(body)
		if err := applyClaudeBedrockCompatibility(c, req, info); err != nil {
			t.Fatal(err)
		}
		if wireBody(t, req) != body || req.Header.Get("anthropic-beta") != "claude-code-20250219,effort-2025-11-24" {
			t.Fatal("unexpected rewrite")
		}
	})
	t.Run("thinking count only", func(t *testing.T) {
		c, info, req := foldContext(foldTurnBody)
		info.ChannelOtherSettings.ClaudeFoldSystemTurnsModels = nil
		if err := applyClaudeBedrockCompatibility(c, req, info); err != nil {
			t.Fatal(err)
		}
		if wireBody(t, req) != foldTurnBody || hasBeta(req.Header, thinkingCountBeta) || !hasBeta(req.Header, legacySystemBeta) {
			t.Fatal("thinking-count action touched more than its beta")
		}
	})
}

func TestClaudeBedrockCompatibilityRefusesUnfoldableSystemTurns(t *testing.T) {
	for _, tc := range []struct{ name, messages, reason string }{
		{"system first", `[{"role":"system","content":"x"},{"role":"user","content":"hi"}]`, "system_turn_position"},
		{"system after assistant", `[{"role":"user","content":"hi"},{"role":"assistant","content":"ok"},{"role":"system","content":"x"}]`, "system_turn_position"},
		{"turn-scoped field", `[{"role":"user","content":"hi"},{"role":"system","content":"x","clear_at":"next_user_message"}]`, "system_turn_field"},
		{"per-turn effort", `[{"role":"user","content":"hi"},{"role":"system","content":[],"output_config":{"effort":"high"}}]`, "system_turn_field"},
		{"tool change", `[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"tool_removal","tool":{"type":"tool_reference","name":"Read"}}]}]`, "system_turn_content"},
		{"text block extra field", `[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"x","citations":[]}]}]`, "system_turn_content"},
		{"null system content", `[{"role":"user","content":"hi"},{"role":"system","content":null}]`, "system_turn_content"},
		{"user extra field", `[{"role":"user","content":"hi","future":true},{"role":"system","content":"x"}]`, "unknown_message_field"},
		{"duplicate role", `[{"role":"user","content":"hi"},{"role":"system","role":"user","content":"x"}]`, "ambiguous_message_shape"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"model":"claude-opus-5","messages":` + tc.messages + `}`
			c, info, req := foldContext(body)
			err := applyClaudeBedrockCompatibility(c, req, info)
			var le *types.LazuError
			if !errors.As(err, &le) || !strings.Contains(le.Error(), tc.reason) || le.StatusCode != 400 || !types.IsSkipRetryError(le) {
				t.Fatalf("want rejection %s, got %v", tc.reason, err)
			}
			if common2.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) {
				t.Fatal("local failure marked provider attempted")
			}
		})
	}
	t.Run("original has system turn the wire body lost", func(t *testing.T) {
		c, info, req := foldContext(`{"model":"claude-opus-5","messages":[{"role":"system","content":"x"}]}`)
		req.Body = io.NopCloser(strings.NewReader(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hi"}]}`))
		if err := applyClaudeBedrockCompatibility(c, req, info); err == nil || !strings.Contains(err.Error(), "system_turn_position") {
			t.Fatalf("want refusal from the original body, got %v", err)
		}
	})
	t.Run("fold beta pinned by header override", func(t *testing.T) {
		c, info, req := foldContext(foldTurnBody)
		info.HeadersOverride = map[string]interface{}{"anthropic-beta": toolChangesBeta}
		if err := applyClaudeBedrockCompatibility(c, req, info); err == nil || !strings.Contains(err.Error(), "header_override_conflict") {
			t.Fatalf("want conflict, got %v", err)
		}
	})
}
