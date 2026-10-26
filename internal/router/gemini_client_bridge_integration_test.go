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

// A Gemini client (the recorded Gemini CLI request) is parsed into the IR and
// planned like the other text-generation clients: native on a Gemini channel,
// where the upstream gets the client's own body, and bridged elsewhere, where
// the upstream gets its own protocol and the client a Gemini reply.
func TestGeminiClientIsPlannedOnEveryTextChannel(t *testing.T) {
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
		path, query string
		raw         []byte
		body        map[string]any
	}
	var (
		mu   sync.Mutex
		hits = map[string][]hit{}
	)
	recordings := map[string]string{
		"oa-text":   "deepseek/chat_text.sse",
		"oa-tool":   "deepseek/chat_tool_call.sse",
		"cl-text":   "anthropic/text.sse",
		"gm-native": "gemini/text.sse",
	}
	upstream := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var request map[string]any
			_ = json.Unmarshal(raw, &request)
			mu.Lock()
			hits[name] = append(hits[name], hit{path: r.URL.Path, query: r.URL.RawQuery, raw: raw, body: request})
			mu.Unlock()
			modelName, _ := request["model"].(string)
			if name == "gemini" {
				modelName = strings.Split(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], ":")[0]
			}
			if stream, _ := request["stream"].(bool); name == "openai" && !stream {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"c1","object":"chat.completion","model":"oa-text","choices":[{"index":0,"message":{"role":"assistant","content":"Whole reply."},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, fixtures.Raw(t, recordings[modelName]))
		}))
	}
	openai, claude, gemini := upstream("openai"), upstream("claude"), upstream("gemini")
	defer openai.Close()
	defer claude.Close()
	defer gemini.Close()
	for _, server := range []*httptest.Server{openai, claude, gemini} {
		allowLocalRelayUpstream(t, server.URL)
	}

	if err := model.CoreDB().Create(&model.User{Id: 995, Username: "gemini-client", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.Token{Id: 995, UserId: 995, Key: "gemini-client-key", Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1)}
	if err := key.Insert(); err != nil {
		t.Fatal(err)
	}
	noBan := 0
	for _, source := range []struct {
		id          int
		channelType int
		models      []string
		url         string
	}{
		{990, constant.ChannelTypeOpenAI, []string{"oa-text", "oa-tool"}, openai.URL},
		{991, constant.ChannelTypeAnthropic, []string{"cl-text"}, claude.URL},
		{992, constant.ChannelTypeGemini, []string{"gm-native"}, gemini.URL},
	} {
		url := source.url
		channel := model.Channel{
			Id: source.id, Type: source.channelType, Key: "local-upstream", Status: common.ChannelStatusEnabled,
			Name: source.models[0], Models: strings.Join(source.models, ","), Group: model.ChannelLaneDirect, BaseURL: &url, AutoBan: &noBan,
		}
		if err := model.CoreDB().Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		for _, name := range source.models {
			if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneDirect, Model: name, Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			if err := model.CoreDB().Create(&model.Model{ModelName: name, Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}).Error; err != nil {
				t.Fatal(err)
			}
			inputPrice, outputPrice := 1.0, 5.0
			if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: name, ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()

	cliRequest := fixtures.ClientRequest(t, "gemini_cli/tool_turn.json")
	send := func(path string, body []byte, wantStatus int) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, gateway.URL+path, bytes.NewReader(body))
		req.Header.Set("x-goog-api-key", "sk-"+key.Key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := gateway.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s: status=%d body=%s", path, resp.StatusCode, raw)
		}
		return string(raw)
	}
	lastHit := func(name string) hit {
		mu.Lock()
		defer mu.Unlock()
		return hits[name][len(hits[name])-1]
	}

	t.Run("openai channel stream", func(t *testing.T) {
		got := send("/v1beta/models/oa-text:streamGenerateContent?alt=sse", cliRequest, http.StatusOK)
		if !strings.Contains(got, `"candidates"`) || !strings.Contains(got, "Hello") || !strings.Contains(got, `"finishReason":"STOP"`) || !strings.Contains(got, `"usageMetadata"`) {
			t.Fatalf("Gemini stream: %s", got)
		}
		if strings.Contains(got, "[DONE]") || strings.Contains(got, "chat.completion") {
			t.Fatalf("a Gemini client must not see Chat framing: %s", got)
		}
		upstreamHit := lastHit("openai")
		if upstreamHit.path != "/v1/chat/completions" {
			t.Fatalf("upstream path %s", upstreamHit.path)
		}
		tools, _ := upstreamHit.body["tools"].([]any)
		if len(tools) != 12 {
			t.Fatalf("the CLI's 12 tools must reach the upstream: %d", len(tools))
		}
		for _, tool := range tools {
			function, _ := tool.(map[string]any)["function"].(map[string]any)
			if _, ok := function["parameters"]; !ok {
				t.Fatalf("tool %v lost its schema", function["name"])
			}
		}
		if stream, _ := upstreamHit.body["stream"].(bool); !stream {
			t.Fatalf("upstream body must stream: %v", upstreamHit.body)
		}
	})

	t.Run("openai channel tool call", func(t *testing.T) {
		got := send("/v1beta/models/oa-tool:streamGenerateContent?alt=sse", cliRequest, http.StatusOK)
		if strings.Count(got, `"functionCall"`) != 1 || !strings.Contains(got, `"name":"get_weather"`) || !strings.Contains(got, "Paris") {
			t.Fatalf("a call is sent whole, once: %s", got)
		}
	})

	t.Run("openai channel whole reply", func(t *testing.T) {
		got := send("/v1/models/oa-text:generateContent", cliRequest, http.StatusOK)
		var reply struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			UsageMetadata struct {
				PromptTokenCount int `json:"promptTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := json.Unmarshal([]byte(got), &reply); err != nil || len(reply.Candidates) != 1 || reply.Candidates[0].Content.Parts[0].Text != "Whole reply." || reply.Candidates[0].FinishReason != "STOP" || reply.UsageMetadata.PromptTokenCount != 5 {
			t.Fatalf("whole reply: %s (%v)", got, err)
		}
	})

	t.Run("claude channel stream", func(t *testing.T) {
		got := send("/v1beta/models/cl-text:streamGenerateContent?alt=sse", cliRequest, http.StatusOK)
		if !strings.Contains(got, `"candidates"`) || !strings.Contains(got, "Hello there") {
			t.Fatalf("Gemini stream from Messages: %s", got)
		}
		if upstreamHit := lastHit("claude"); upstreamHit.path != "/v1/messages" {
			t.Fatalf("upstream path %s", upstreamHit.path)
		} else if _, ok := upstreamHit.body["messages"]; !ok {
			t.Fatalf("upstream body is not Messages: %v", upstreamHit.body)
		}
	})

	t.Run("gemini channel native", func(t *testing.T) {
		got := send("/v1beta/models/gm-native:streamGenerateContent?alt=sse", cliRequest, http.StatusOK)
		if !strings.Contains(got, "Hello, nice to meet you") {
			t.Fatalf("native stream: %s", got)
		}
		upstreamHit := lastHit("gemini")
		if !strings.HasSuffix(upstreamHit.path, "/models/gm-native:streamGenerateContent") {
			t.Fatalf("upstream path %s", upstreamHit.path)
		}
		// the client's own declarations, parametersJsonSchema included
		if !bytes.Contains(upstreamHit.raw, []byte(`"parametersJsonSchema"`)) || bytes.Contains(upstreamHit.raw, []byte(`"messages"`)) {
			t.Fatalf("native body changed: %s", upstreamHit.raw)
		}
	})

	t.Run("bridge refuses what it cannot carry", func(t *testing.T) {
		body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"cachedContent":"cachedContents/abc"}`)
		got := send("/v1beta/models/oa-text:generateContent", body, http.StatusBadRequest)
		if !strings.Contains(got, "cachedContent") {
			t.Fatalf("the refusal names the field: %s", got)
		}
	})
}
