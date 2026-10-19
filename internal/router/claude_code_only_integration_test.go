package router

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

// A Claude Code subscription pool (observed 2026-10-01) admits only the
// official client: a claude-cli User-Agent plus Claude Code's metadata. Marked
// Claude Code-only, such a source must receive Claude Code's own requests with
// their client headers intact, and never a request from any other client.
func TestClaudeCodeOnlySourceGetsOnlyClaudeCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()
	oldLog, oldSQLite, oldMemory := model.ConfiguredTelemetryDB(), common.UsingSQLite, common.MemoryCacheEnabled
	model.SetTelemetryDB(model.CoreDB())
	common.UsingSQLite = true
	common.MemoryCacheEnabled = false
	defer func() {
		drainRelayDiagnosticFixture(t)
		model.SetTelemetryDB(oldLog)
		common.UsingSQLite = oldSQLite
		common.MemoryCacheEnabled = oldMemory
	}()
	if err := model.CoreDB().AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().AutoMigrate(&model.Log{}, &model.WalletLedger{}, &model.RoutingDecision{}, &model.RoutingOutcome{}, &model.RoutingAttemptOutcome{}, &model.TelemetryEventReceipt{}); err != nil {
		t.Fatal(err)
	}

	type hit struct {
		upstream string
		header   http.Header
		metadata string
	}
	var (
		mu   sync.Mutex
		hits []hit
	)
	upstream := func(name string, poolRules bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Metadata struct {
					UserID string `json:"user_id"`
				} `json:"metadata"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			hits = append(hits, hit{upstream: name, header: r.Header.Clone(), metadata: body.Metadata.UserID})
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if poolRules && (!strings.HasPrefix(r.UserAgent(), "claude-cli/") || body.Metadata.UserID == "") {
				w.WriteHeader(http.StatusForbidden)
				io.WriteString(w, `{"type":"error","error":{"type":"permission_error","message":"only the official Claude CLI"}}`)
				return
			}
			io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"cc-fixture","content":[{"type":"text","text":"from `+name+`"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2}}`)
		}))
	}
	pool := upstream("pool", true)
	defer pool.Close()
	open := upstream("open", false)
	defer open.Close()
	allowLocalRelayUpstream(t, pool.URL)
	allowLocalRelayUpstream(t, open.URL)

	if err := model.CoreDB().Create(&model.User{Id: 994, Username: "cc-only", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.Token{
		Id: 994, UserId: 994, Key: "cc-only-key", Group: model.ChannelLaneDirect,
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1),
		ModelLanes: model.TokenModelLaneMap{"cc-fixture": model.ChannelLaneAgent},
	}
	if err := key.Insert(); err != nil {
		t.Fatal(err)
	}
	// The pool outranks the open source, so Claude Code always lands on it and
	// any other client lands on it too unless the restriction holds.
	high, low, noBan := int64(10), int64(0), 0
	for _, source := range []struct {
		id       int
		name     string
		url      string
		priority *int64
		settings string
	}{
		{994, "pool", pool.URL, &high, `{"protocol_dialect":"anthropic","claude_code_client_only":true}`},
		{995, "open", open.URL, &low, `{"protocol_dialect":"anthropic"}`},
	} {
		url := source.url
		channel := model.Channel{
			Id: source.id, Type: constant.ChannelTypeAnthropic, Key: "local-upstream", Status: common.ChannelStatusEnabled,
			Name: source.name, Models: "cc-fixture", Group: model.ChannelLaneAgent,
			BaseURL: &url, Priority: source.priority, AutoBan: &noBan, OtherSettings: source.settings,
		}
		if err := model.CoreDB().Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneAgent, Model: "cc-fixture", Enabled: true, Priority: source.priority}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := model.CoreDB().Create(&model.Model{ModelName: "cc-fixture", Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
		t.Fatal(err)
	}
	inputPrice, outputPrice := 1.0, 5.0
	if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: "cc-fixture", ChannelGroup: model.ChannelLaneAgent, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()

	send := func(userAgent string) string {
		t.Helper()
		body := `{"model":"cc-fixture","max_tokens":16,"metadata":{"user_id":"{\"device_id\":\"d\",\"account_uuid\":\"\",\"session_id\":\"s\"}"},"messages":[{"role":"user","content":"hi"}]}`
		req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/messages?beta=true", bytes.NewReader([]byte(body)))
		req.Header.Set("x-api-key", "sk-"+key.Key)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("x-app", "cli")
		req.Header.Set("X-Claude-Code-Session-Id", "session-1")
		resp, err := gateway.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", userAgent, resp.StatusCode, raw)
		}
		return string(raw)
	}

	if got := send("claude-cli/2.1.276 (external, cli)"); !strings.Contains(got, "from pool") {
		t.Fatalf("Claude Code was not served by the pool: %s", got)
	}
	mu.Lock()
	first := hits[len(hits)-1]
	mu.Unlock()
	if first.upstream != "pool" || first.header.Get("User-Agent") != "claude-cli/2.1.276 (external, cli)" ||
		first.header.Get("X-Claude-Code-Session-Id") != "session-1" || first.header.Get("x-app") != "cli" || first.metadata == "" {
		t.Fatalf("pool saw %+v", first)
	}
	if first.header.Get("x-api-key") != "local-upstream" {
		t.Fatal("the pool must receive the source's key, not the customer's")
	}

	if got := send("Anthropic/Python 0.40.0"); !strings.Contains(got, "from open") {
		t.Fatalf("another client was not served by the open source: %s", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, h := range hits[1:] {
		if h.upstream == "pool" {
			t.Fatalf("a non-Claude Code request reached the pool: %+v", h)
		}
	}
}
