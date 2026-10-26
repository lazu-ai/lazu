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
	"github.com/lazu-ai/lazu/internal/protocol/textgen/fixtures"
	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/gin-gonic/gin"
)

// With the legacy Claude-client request converter gone, a Claude client on an
// OpenAI or Gemini channel is served only through the hub's Chat bridge. Each
// upstream answers with a recorded stream; the gateway must send the upstream
// its own request shape and the client a Messages stream.
func TestClaudeClientIsBridgedOnOpenAIAndGeminiChannels(t *testing.T) {
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
		path string
		body map[string]any
	}
	var (
		mu   sync.Mutex
		hits = map[string][]hit{}
	)
	upstream := func(name, recording string) *httptest.Server {
		body := fixtures.Raw(t, recording)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			mu.Lock()
			hits[name] = append(hits[name], hit{path: r.URL.Path, body: request})
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, body)
		}))
	}
	gemini := upstream("gemini", "gemini/text.sse")
	defer gemini.Close()
	openai := upstream("openai", "deepseek/chat_text.sse")
	defer openai.Close()
	allowLocalRelayUpstream(t, gemini.URL)
	allowLocalRelayUpstream(t, openai.URL)

	if err := model.CoreDB().Create(&model.User{Id: 996, Username: "bridge", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.Token{Id: 996, UserId: 996, Key: "bridge-key", Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1)}
	if err := key.Insert(); err != nil {
		t.Fatal(err)
	}
	noBan := 0
	for _, source := range []struct {
		id          int
		channelType int
		model       string
		url         string
	}{
		{996, constant.ChannelTypeGemini, "gemini-3.5-flash", gemini.URL},
		{997, constant.ChannelTypeOpenAI, "deepseek-flash", openai.URL},
	} {
		url := source.url
		channel := model.Channel{
			Id: source.id, Type: source.channelType, Key: "local-upstream", Status: common.ChannelStatusEnabled,
			Name: source.model, Models: source.model, Group: model.ChannelLaneDirect, BaseURL: &url, AutoBan: &noBan,
		}
		if err := model.CoreDB().Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneDirect, Model: source.model, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		if err := model.CoreDB().Create(&model.Model{ModelName: source.model, Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
			t.Fatal(err)
		}
		inputPrice, outputPrice := 1.0, 5.0
		if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: source.model, ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()

	send := func(modelName string) string {
		t.Helper()
		body := `{"model":"` + modelName + `","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Say hello in five words."}]}`
		req, _ := http.NewRequest(http.MethodPost, gateway.URL+"/v1/messages", bytes.NewReader([]byte(body)))
		req.Header.Set("x-api-key", "sk-"+key.Key)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")
		resp, err := gateway.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", modelName, resp.StatusCode, raw)
		}
		return string(raw)
	}

	for _, tc := range []struct {
		upstream, model, wantText string
		wantShape                 string
	}{
		{"gemini", "gemini-3.5-flash", "Hello, nice to meet you", "contents"},
		{"openai", "deepseek-flash", "", "messages"},
	} {
		got := send(tc.model)
		if !strings.Contains(got, "event: message_start") || !strings.Contains(got, "content_block_delta") || !strings.Contains(got, "event: message_stop") {
			t.Fatalf("%s: not a Messages stream: %s", tc.upstream, got)
		}
		if tc.wantText != "" && !strings.Contains(got, tc.wantText) {
			t.Fatalf("%s: answer text missing: %s", tc.upstream, got)
		}
		mu.Lock()
		seen := hits[tc.upstream]
		mu.Unlock()
		if len(seen) != 1 {
			t.Fatalf("%s: upstream hits = %d", tc.upstream, len(seen))
		}
		if _, ok := seen[0].body[tc.wantShape]; !ok {
			t.Fatalf("%s: upstream request has no %q: %v (path %s)", tc.upstream, tc.wantShape, seen[0].body, seen[0].path)
		}
	}
}
