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

// A Gemini channel serves Chat, Responses and Claude clients through the
// Gemini codec: the request is encoded for generateContent with the channel's
// policy, and the upstream's reply (a recorded stream) is decoded and written
// in the client's protocol.
func TestGeminiChannelServesBridgedClientsThroughTheCodec(t *testing.T) {
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

	recordings := map[string]string{
		"gem-text":  "gemini/text.sse",
		"gem-tool":  "gemini/tool_call.sse",
		"gem-think": "gemini/thinking.sse",
		"gem-image": "gemini/image_output.sse",
	}
	type hit struct {
		path, query string
		body        map[string]any
	}
	var (
		mu   sync.Mutex
		hits []hit
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		hits = append(hits, hit{path: r.URL.Path, query: r.URL.RawQuery, body: body})
		mu.Unlock()
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		modelName, action, _ := strings.Cut(name, ":")
		if action == "generateContent" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Whole reply."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8},"modelVersion":"`+modelName+`"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, fixtures.Raw(t, recordings[modelName]))
	}))
	defer upstream.Close()
	allowLocalRelayUpstream(t, upstream.URL)

	if err := model.CoreDB().Create(&model.User{Id: 998, Username: "gemini-bridge", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: billing.USD(1)}).Error; err != nil {
		t.Fatal(err)
	}
	key := &model.Token{Id: 998, UserId: 998, Key: "gemini-bridge-key", Group: model.ChannelLaneDirect, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: billing.USD(1)}
	if err := key.Insert(); err != nil {
		t.Fatal(err)
	}
	url, noBan := upstream.URL, 0
	models := []string{"gem-text", "gem-tool", "gem-think", "gem-image"}
	channel := model.Channel{Id: 998, Type: constant.ChannelTypeGemini, Key: "local-upstream", Status: common.ChannelStatusEnabled,
		Name: "gemini", Models: strings.Join(models, ","), Group: model.ChannelLaneDirect, BaseURL: &url, AutoBan: &noBan}
	if err := model.CoreDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range models {
		if err := model.CoreDB().Create(&model.Ability{ChannelId: channel.Id, Group: model.ChannelLaneDirect, Model: name, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		row := model.Model{ModelName: name, Status: 1, LifecycleStatus: model.ModelLifecycleActive, NameRule: model.NameRuleExact}
		if name == "gem-think" {
			row.ReasoningOptions = model.ModelReasoningOptions{Efforts: []string{"low", "medium", "high"}}
		}
		if err := model.CoreDB().Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		inputPrice, outputPrice := 1.0, 5.0
		if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: name, ChannelGroup: model.ChannelLaneDirect, InputPerMtok: &inputPrice, OutputPerMtok: &outputPrice, Active: true, PricingVersion: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()

	send := func(path, body string, headers map[string]string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, gateway.URL+path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer sk-"+key.Key)
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		resp, err := gateway.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: status=%d body=%s", path, body, resp.StatusCode, raw)
		}
		return string(raw)
	}
	lastHit := func() hit {
		mu.Lock()
		defer mu.Unlock()
		return hits[len(hits)-1]
	}
	chat := func(modelName, extra string, stream bool) string {
		streamText := "false"
		if stream {
			streamText = "true"
		}
		return send("/v1/chat/completions", `{"model":"`+modelName+`","stream":`+streamText+`,"messages":[{"role":"user","content":"hi"}]`+extra+`}`, nil)
	}

	t.Run("chat stream text", func(t *testing.T) {
		got := chat("gem-text", "", true)
		if !strings.Contains(got, "Hello, nice to meet you") || !strings.Contains(got, "data: [DONE]") {
			t.Fatalf("chat stream: %s", got)
		}
		upstreamHit := lastHit()
		if !strings.HasSuffix(upstreamHit.path, "/models/gem-text:streamGenerateContent") || upstreamHit.query != "alt=sse" {
			t.Fatalf("upstream URL %s?%s", upstreamHit.path, upstreamHit.query)
		}
		if _, ok := upstreamHit.body["contents"]; !ok {
			t.Fatalf("upstream body is not generateContent: %v", upstreamHit.body)
		}
		if _, ok := upstreamHit.body["safetySettings"]; !ok {
			t.Fatalf("the channel's safety settings are missing: %v", upstreamHit.body)
		}
		if _, ok := upstreamHit.body["stream"]; ok {
			t.Fatalf("generateContent takes no stream field: %v", upstreamHit.body)
		}
	})

	t.Run("chat stream tool call", func(t *testing.T) {
		got := chat("gem-tool", `,"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`, true)
		if !strings.Contains(got, `"tool_calls"`) || !strings.Contains(got, "get_weather") || !strings.Contains(got, `Paris`) {
			t.Fatalf("chat tool stream: %s", got)
		}
	})

	t.Run("chat thought signature round trip", func(t *testing.T) {
		got := chat("gem-tool", `,"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`, true)
		if strings.Count(got, `"thought_signature"`) != 1 {
			t.Fatalf("the signed call's signature must reach the client once: %s", got)
		}
		history := `{"model":"gem-tool","stream":true,"messages":[{"role":"user","content":"weather?"},` +
			`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"},"extra_content":{"google":{"thought_signature":"sig-back"}}}]},` +
			`{"role":"tool","tool_call_id":"call_1","content":"sunny"}]}`
		send("/v1/chat/completions", history, nil)
		contents, _ := lastHit().body["contents"].([]any)
		raw, _ := json.Marshal(contents)
		if !strings.Contains(string(raw), `"thoughtSignature":"sig-back"`) {
			t.Fatalf("the signature sent back must reach Gemini: %s", raw)
		}
	})

	t.Run("chat stream reasoning", func(t *testing.T) {
		got := chat("gem-think", `,"reasoning_effort":"low"`, true)
		if !strings.Contains(got, "reasoning_content") || !strings.Contains(got, "391") {
			t.Fatalf("chat reasoning stream: %s", got)
		}
		config, _ := lastHit().body["generationConfig"].(map[string]any)
		thinking, _ := config["thinkingConfig"].(map[string]any)
		if thinking["thinkingLevel"] != "low" || thinking["includeThoughts"] != true {
			t.Fatalf("thinking config: %v", config)
		}
	})

	t.Run("chat stream reasoning via extra_body", func(t *testing.T) {
		chat("gem-text", `,"extra_body":{"google":{"thinking_config":{"thinking_budget":512}}}`, true)
		config, _ := lastHit().body["generationConfig"].(map[string]any)
		thinking, _ := config["thinkingConfig"].(map[string]any)
		if thinking["thinkingBudget"] != float64(512) || thinking["includeThoughts"] != true {
			t.Fatalf("thinking config: %v", config)
		}
	})

	t.Run("chat n asks one candidate", func(t *testing.T) {
		chat("gem-text", `,"n":2`, true)
		config, _ := lastHit().body["generationConfig"].(map[string]any)
		if _, ok := config["candidateCount"]; ok {
			t.Fatalf("the reply carries one candidate, so one is asked for: %v", config)
		}
	})

	t.Run("chat stream image output", func(t *testing.T) {
		if got := chat("gem-image", "", true); !strings.Contains(got, "data:image/jpeg;base64,") {
			t.Fatalf("image stream: %s", got)
		}
	})

	t.Run("chat whole reply", func(t *testing.T) {
		got := chat("gem-text", "", false)
		var reply struct {
			Object  string `json:"object"`
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Usage struct {
				PromptTokens int `json:"prompt_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(got), &reply); err != nil || len(reply.Choices) != 1 || reply.Choices[0].Message.Content != "Whole reply." || reply.Usage.PromptTokens != 5 {
			t.Fatalf("chat whole reply: %s (%v)", got, err)
		}
		if upstreamHit := lastHit(); !strings.HasSuffix(upstreamHit.path, ":generateContent") {
			t.Fatalf("upstream path %s", upstreamHit.path)
		}
	})

	t.Run("claude client stream", func(t *testing.T) {
		got := send("/v1/messages", `{"model":"gem-text","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`,
			map[string]string{"x-api-key": "sk-" + key.Key, "anthropic-version": "2023-06-01"})
		if !strings.Contains(got, "event: message_start") || !strings.Contains(got, "Hello, nice to meet you") || !strings.Contains(got, "event: message_stop") {
			t.Fatalf("claude stream: %s", got)
		}
	})

	t.Run("responses client stream", func(t *testing.T) {
		got := send("/v1/responses", `{"model":"gem-text","stream":true,"store":false,"input":"hi"}`, nil)
		if !strings.Contains(got, "response.output_text.delta") || !strings.Contains(got, "response.completed") {
			t.Fatalf("responses stream: %s", got)
		}
	})
}
