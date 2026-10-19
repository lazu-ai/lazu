package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
)

// fakeClaudeCodePool admits only what a Claude Code subscription pool admits:
// a claude-cli User-Agent and Claude Code's metadata.user_id.
func fakeClaudeCodePool(t *testing.T, seen *[]http.Header, bodies *[]map[string]any, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		mu.Lock()
		*seen = append(*seen, request.Header.Clone())
		*bodies = append(*bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasPrefix(request.UserAgent(), "claude-cli/") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"permission_error","message":"only the official Claude CLI"}}`))
			return
		}
		metadata, _ := body["metadata"].(map[string]any)
		userID, _ := metadata["user_id"].(string)
		var identity map[string]string
		if json.Unmarshal([]byte(userID), &identity) != nil || identity["device_id"] == "" || identity["session_id"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"illegal request"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-sonnet-5-5",
			"content":     []any{map[string]any{"type": "text", "text": "OK"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 12, "output_tokens": 2},
		})
	}))
}

// testClaudeCodeProbeIdentity stands in for probe_identities.claude_code in
// config.yaml. The public template configures none.
var testClaudeCodeProbeIdentity = common.ProbeIdentityConfig{
	UserAgent: "claude-cli/9.0.0 (external, cli)",
	Headers:   map[string]string{"x-app": "cli"},
	Metadata:  `{"user_id":"{\"account_uuid\":\"\",\"device_id\":\"{{device_id}}\",\"session_id\":\"{{session_id}}\"}"}`,
}

// A probe against a Claude Code-only source presents the configured probe
// identity, so a pool that admits nothing else answers. Without a configured
// identity, or without the flag, the probe goes out as-is and is refused.
func TestProbeReachesClaudeCodeOnlySource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitTokenEncoders()
	originalIdentities := common.AppConfig.ProbeIdentities
	t.Cleanup(func() { common.AppConfig.ProbeIdentities = originalIdentities })
	for _, tc := range []struct {
		name       string
		settings   string
		configured bool
		wantOK     bool
		wantUA     string
		wantCalls  int
	}{
		{"configured identity", `{"protocol_dialect":"anthropic","claude_code_client_only":true}`, true, true, testClaudeCodeProbeIdentity.UserAgent, 1},
		{"per-source user agent", `{"protocol_dialect":"anthropic","claude_code_client_only":true,"claude_code_user_agent":"claude-cli/9.9.9 (external, cli)"}`, true, true, "claude-cli/9.9.9 (external, cli)", 1},
		// The source's client restriction refuses the bare probe before any
		// byte leaves the gateway.
		{"no identity configured", `{"protocol_dialect":"anthropic","claude_code_client_only":true}`, false, false, "", 0},
		{"unflagged source", `{"protocol_dialect":"anthropic"}`, true, false, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.AppConfig.ProbeIdentities = nil
			if tc.configured {
				common.AppConfig.ProbeIdentities = map[string]common.ProbeIdentityConfig{
					common.ProbeClientClaudeCode: testClaudeCodeProbeIdentity,
				}
			}
			db := setupMediaJobControllerTestDB(t)
			seedMediaJobControllerAuth(t, db)
			var (
				mu     sync.Mutex
				seen   []http.Header
				bodies []map[string]any
			)
			upstream := fakeClaudeCodePool(t, &seen, &bodies, &mu)
			defer upstream.Close()
			baseURL := upstream.URL
			channel := &model.Channel{
				Id: 87, Type: 14, Key: "pool-key", Status: common.ChannelStatusEnabled, Name: "pool",
				Models: "claude-sonnet-5-5", Group: model.ChannelLaneAgent, BaseURL: &baseURL, OtherSettings: tc.settings,
			}
			if err := db.Create(channel).Error; err != nil {
				t.Fatalf("seed channel: %v", err)
			}
			input, output := 0.69, 3.45
			if err := db.Create(&model.ModelSellPrice{
				ModelName: "claude-sonnet-5-5", ChannelGroup: model.ChannelLaneAgent, InputPerMtok: &input, OutputPerMtok: &output,
				Active: true, PricingVersion: 1, Source: "test",
			}).Error; err != nil {
				t.Fatalf("seed price: %v", err)
			}
			model.RefreshPricing()

			request := &dto.GeneralOpenAIRequest{Model: "claude-sonnet-5-5", Messages: []dto.Message{{Role: "user", Content: "hi"}}, MaxTokens: 16}
			result := testChannelForLaneWithRequest(t.Context(), channel, "claude-sonnet-5-5", "", model.ChannelLaneAgent, false, request)
			ok := result.localErr == nil && result.lazuErr == nil
			if ok != tc.wantOK {
				t.Fatalf("probe ok = %t (%v / %v), want %t", ok, result.localErr, result.lazuErr, tc.wantOK)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(seen) != tc.wantCalls {
				t.Fatalf("upstream calls = %d, want %d", len(seen), tc.wantCalls)
			}
			if tc.wantCalls == 0 {
				return
			}
			if tc.wantOK {
				if got := seen[0].Get("User-Agent"); got != tc.wantUA {
					t.Fatalf("User-Agent = %q, want %q", got, tc.wantUA)
				}
				if seen[0].Get("x-app") != "cli" {
					t.Fatal("x-app missing")
				}
			} else if strings.HasPrefix(seen[0].Get("User-Agent"), "claude-cli/") {
				t.Fatal("the probe must not present a client identity here")
			} else if _, has := bodies[0]["metadata"]; has {
				t.Fatal("the probe must not carry identity metadata here")
			}
		})
	}
}
