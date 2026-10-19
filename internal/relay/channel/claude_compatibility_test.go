package channel

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	common2 "github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

const compatPlainBody = `{"model":"claude-sonnet-5","system":"top-level instructions","messages":[{"role":"user","content":"hello"}],"max_tokens":64,"future":{"keep":false}}`

func compatContext(body string) (*gin.Context, *relaycommon.RelayInfo) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Add("anthropic-beta", "claude-code-20250219, "+legacySystemBeta)
	c.Request.Header.Add("anthropic-beta", "effort-2025-11-24,"+legacySystemBeta)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeAnthropic, UpstreamModelName: "claude-sonnet-5", ChannelId: 7,
		ChannelOtherSettings: dto.ChannelOtherSettings{ClaudeOmitUnusedSystemBetaModels: []string{"claude-sonnet-5"}},
	}}
	return c, info
}

func TestClaudeCompatibilityRequestShapes(t *testing.T) {
	cases := []struct{ name, body, reason string }{
		{"plain", compatPlainBody, ""},
		{"tool continuation", `{"model":"claude-sonnet-5","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"opaque","signature":"preserve"},{"type":"tool_use","id":"t1","name":"read","input":{"role":"system"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"role system is only tool data"}]}]}`, ""},
		{"system text", `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hello"},{"role":"system","content":"new instruction"}]}`, "mid_conversation_system_used"},
		{"system effort only", `{"model":"claude-sonnet-5","messages":[{"role":"system","content":[],"output_config":{"effort":"high"}}]}`, "mid_conversation_system_used"},
		{"system tool changes", `{"model":"claude-sonnet-5","messages":[{"role":"system","content":[{"type":"tool_removal","tool":{"type":"tool_reference","name":"read"}}]}]}`, "mid_conversation_system_used"},
		{"turn scoped system", `{"model":"claude-sonnet-5","messages":[{"role":"system","content":"note","clear_at":"next_user_message"}]}`, "mid_conversation_system_used"},
		{"unknown role", `{"model":"claude-sonnet-5","messages":[{"role":"developer","content":"hi"}]}`, "unknown_message_role"},
		{"unknown field", `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hi","future":true}]}`, "unknown_message_field"},
		{"null content", `{"model":"claude-sonnet-5","messages":[{"role":"user","content":null}]}`, "unknown_message_content"},
		{"null messages", `{"model":"claude-sonnet-5","messages":null}`, "unknown_message_shape"},
		{"empty messages", `{"model":"claude-sonnet-5","messages":[]}`, "unknown_message_shape"},
		{"duplicate role", `{"model":"claude-sonnet-5","messages":[{"role":"system","role":"user","content":"hi"}]}`, "ambiguous_message_shape"},
		{"escaped duplicate role", `{"model":"claude-sonnet-5","messages":[{"role":"system","r\u006fle":"user","content":"hi"}]}`, "ambiguous_message_shape"},
		{"duplicate messages", `{"model":"claude-sonnet-5","messages":[],"messages":[{"role":"user","content":"hi"}]}`, "ambiguous_original_body"},
		{"malformed", `{"model":"claude-sonnet-5","messages":`, "ambiguous_original_body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, info := compatContext(tc.body)
			req := httptest.NewRequest(http.MethodPost, "http://upstream.test/v1/messages", strings.NewReader(tc.body))
			req.Header = c.Request.Header.Clone()
			originalHeaders := c.Request.Header.Clone()
			err := applyClaudeSystemBetaCompatibility(c, req, info)
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				if hasBeta(req.Header, legacySystemBeta) {
					t.Fatal("beta survived")
				}
				if got := req.Header.Get("anthropic-beta"); got != "claude-code-20250219,effort-2025-11-24" {
					t.Fatalf("other flags lost: %s", got)
				}
				wire, _ := io.ReadAll(req.Body)
				if string(wire) != tc.body {
					t.Fatal("outbound body changed")
				}
			} else {
				var le *types.LazuError
				if !errors.As(err, &le) || !strings.Contains(le.Error(), tc.reason) || le.StatusCode != 400 || !types.IsSkipRetryError(le) {
					t.Fatalf("want safe rejection %s, got %v", tc.reason, err)
				}
				if common2.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) {
					t.Fatal("local failure marked provider attempted")
				}
			}
			if !reflect.DeepEqual(c.Request.Header, originalHeaders) {
				t.Fatal("client headers mutated")
			}
			original, _ := common2.GetRequestBody(c)
			if string(original) != tc.body {
				t.Fatal("client body mutated")
			}
		})
	}
}

func TestClaudeCompatibilityScopeAndConflicts(t *testing.T) {
	for _, tc := range []struct {
		name             string
		change           func(*gin.Context, *relaycommon.RelayInfo, *http.Request)
		reject, filtered bool
	}{
		{"disabled", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {
			i.ChannelOtherSettings.ClaudeOmitUnusedSystemBetaModels = nil
		}, false, false},
		{"different model", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {
			i.ChannelOtherSettings.ClaudeOmitUnusedSystemBetaModels = []string{"claude-opus-5"}
		}, false, false},
		{"different protocol", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {
			i.RelayFormat = types.RelayFormatOpenAI
		}, false, false},
		{"different channel type", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {
			i.ChannelType = constant.ChannelTypeOpenAI
		}, false, false},
		{"wildcard", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {
			i.ChannelOtherSettings.ClaudeOmitUnusedSystemBetaModels = []string{"claude-*"}
		}, true, false},
		{"header conflict", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {
			i.HeadersOverride = map[string]interface{}{"Anthropic-Beta": legacySystemBeta}
		}, true, false},
		{"passthrough model mismatch", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {
			i.UpstreamModelName = "claude-sonnet-5-mapped"
		}, true, false},
		{"outbound introduced system", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`{"model":"claude-sonnet-5","messages":[{"role":"system","content":"unsafe"}]}`))
		}, true, false},
		{"plain", func(c *gin.Context, i *relaycommon.RelayInfo, r *http.Request) {}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, i := compatContext(compatPlainBody)
			r := httptest.NewRequest("POST", "http://upstream.test", strings.NewReader(compatPlainBody))
			r.Header = c.Request.Header.Clone()
			tc.change(c, i, r)
			err := applyClaudeSystemBetaCompatibility(c, r, i)
			if (err != nil) != tc.reject {
				t.Fatalf("err=%v reject=%v", err, tc.reject)
			}
			if err == nil && hasBeta(r.Header, legacySystemBeta) == tc.filtered {
				t.Fatal("incorrect scope/filtering")
			}
		})
	}
}

type compatHTTPAdaptor struct {
	failingWSSAdaptor
	url string
}

func (a compatHTTPAdaptor) GetRequestURL(*relaycommon.RelayInfo) (string, error) { return a.url, nil }
func (a compatHTTPAdaptor) SetupRequestHeader(c *gin.Context, h *http.Header, i *relaycommon.RelayInfo) error {
	SetupApiRequestHeader(i, c, h)
	// Simulate a later writer restoring the beta. The final action must still win.
	h.Add("anthropic-beta", legacySystemBeta)
	h.Set("x-api-key", "upstream-only")
	return nil
}

func TestClaudeCompatibilityFinalTransportAndAttemptIsolation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if hasBeta(r.Header, legacySystemBeta) != (calls == 2) {
			t.Error("wrong final header for attempt")
		}
		if r.Header.Get("x-api-key") != "upstream-only" {
			t.Error("upstream auth changed")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, []byte(compatPlainBody)) {
			t.Error("wire body changed")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	c, i := compatContext(compatPlainBody)
	c.Request.Header.Set("x-api-key", "client-only")
	a := compatHTTPAdaptor{url: server.URL}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt == 1 {
			i.ChannelOtherSettings.ClaudeOmitUnusedSystemBetaModels = nil
		}
		resp, err := DoApiRequest(a, c, i, strings.NewReader(compatPlainBody))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
	if !hasBeta(c.Request.Header, legacySystemBeta) {
		t.Fatal("next attempt lost original beta")
	}
	c, i = compatContext(`{"model":"claude-sonnet-5","messages":[{"role":"system","content":"used"}]}`)
	_, err := DoApiRequest(a, c, i, strings.NewReader(compatPlainBody))
	if err == nil || calls != 2 {
		t.Fatalf("used feature sent upstream: calls=%d err=%v", calls, err)
	}
	if common2.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) {
		t.Fatal("local rejection recorded upstream call")
	}
}
