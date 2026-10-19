package router

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/controller"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestSetRelayRouter_RegistersNativeCompatibilityRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	SetRelayRouter(engine)
	routes := engine.Routes()

	criticalRoutes := []struct {
		method string
		path   string
	}{
		{method: "POST", path: "/v1/chat/completions"},
		{method: "POST", path: "/v1/responses"},
		{method: "POST", path: "/v1/messages"},
		{method: "POST", path: "/v1/search"},
		{method: "GET", path: "/v1/realtime"},
		{method: "POST", path: "/v1/models/*path"},
		{method: "POST", path: "/v1beta/models/*path"},
	}

	for _, item := range criticalRoutes {
		if !hasRoute(routes, item.method, item.path) {
			t.Fatalf("missing relay compatibility route %s %s", item.method, item.path)
		}
	}

	// Transport guard rails: websocket is only for realtime.
	if hasRoute(routes, "POST", "/v1/realtime") {
		t.Fatalf("unexpected HTTP POST realtime route; websocket realtime must be GET /v1/realtime")
	}
	if hasRoute(routes, "GET", "/v1/chat/completions") {
		t.Fatalf("unexpected websocket GET chat route; chat/completions should remain HTTP POST + SSE")
	}
	if hasRoute(routes, "GET", "/v1/responses") {
		t.Fatalf("unexpected websocket GET responses route; responses should remain HTTP POST + SSE")
	}
}

func TestSetRelayRouter_EphemeralCredentialCanReadVideoJobOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2501,
		Username:    "video_ephemeral_user",
		Password:    "password123",
		DisplayName: "video_ephemeral_user",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       billing.USD(1),
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:          3501,
		UserId:      user.Id,
		Key:         "videoephemeralparent",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: billing.USD(1),
		Group:       model.ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}
	issued, err := model.IssueEphemeralCredential(model.IssueEphemeralCredentialInput{
		UserID:     user.Id,
		TokenID:    token.Id,
		SessionID:  "sess-video-status",
		Models:     []string{"sora-2"},
		Providers:  []string{"openai"},
		TTLSeconds: 300,
	})
	if err != nil {
		t.Fatalf("failed to issue ephemeral credential: %v", err)
	}
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:    user.Id,
		TokenID:   token.Id,
		ModelName: "sora-2",
		Kind:      model.MediaJobKindVideo,
		Provider:  "openai",
		SessionID: "sess-video-status",
		RequestID: "req-video-status",
	})
	if err != nil {
		t.Fatalf("failed to create media job: %v", err)
	}
	otherJob, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:    user.Id,
		TokenID:   token.Id,
		ModelName: "other-video-model",
		Kind:      model.MediaJobKindVideo,
		Provider:  "openai",
		SessionID: "sess-other-video",
		RequestID: "req-other-video-status",
	})
	if err != nil {
		t.Fatalf("failed to create other media job: %v", err)
	}

	engine := gin.New()
	SetRelayRouter(engine)

	videoReq := httptest.NewRequest(http.MethodGet, "/v1/videos/"+job.PublicID, nil)
	videoReq.Header.Set("Authorization", "Bearer "+issued.Credential)
	videoReq.Header.Set("X-Session-Id", "sess-video-status")
	videoResp := httptest.NewRecorder()
	engine.ServeHTTP(videoResp, videoReq)
	if videoResp.Code != http.StatusOK {
		t.Fatalf("expected ephemeral video status read to succeed, got %d body=%s", videoResp.Code, videoResp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/videos", nil)
	listReq.Header.Set("Authorization", "Bearer "+issued.Credential)
	listResp := httptest.NewRecorder()
	engine.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected ephemeral video list read to succeed, got %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listPayload dto.MediaJobListResponse
	if err := json.Unmarshal(listResp.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("failed to decode video list: %v body=%s", err, listResp.Body.String())
	}
	if len(listPayload.Data) != 1 || listPayload.Data[0].ID != job.PublicID {
		t.Fatalf("expected list to include only scoped video job %s, got %+v", job.PublicID, listPayload.Data)
	}

	otherReq := httptest.NewRequest(http.MethodGet, "/v1/videos/"+otherJob.PublicID, nil)
	otherReq.Header.Set("Authorization", "Bearer "+issued.Credential)
	otherResp := httptest.NewRecorder()
	engine.ServeHTTP(otherResp, otherReq)
	if otherResp.Code != http.StatusForbidden {
		t.Fatalf("expected out-of-scope video job to reject ephemeral credential, got %d body=%s", otherResp.Code, otherResp.Body.String())
	}

	mediaReq := httptest.NewRequest(http.MethodGet, "/v1/media/jobs/"+job.PublicID, nil)
	mediaReq.Header.Set("Authorization", "Bearer "+issued.Credential)
	mediaReq.Header.Set("X-Session-Id", "sess-video-status")
	mediaResp := httptest.NewRecorder()
	engine.ServeHTTP(mediaResp, mediaReq)
	if mediaResp.Code != http.StatusForbidden {
		t.Fatalf("expected generic media jobs endpoint to reject ephemeral credential, got %d body=%s", mediaResp.Code, mediaResp.Body.String())
	}
}

func TestSetRelayRouter_SearchUsesConfiguredBackendAndTokenPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	oldLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	defer func() {
		common.LogConsumeEnabled = oldLogConsumeEnabled
	}()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if payload["api_key"] != "tavily-test-key" {
			t.Fatalf("expected api_key to be forwarded, got %#v", payload["api_key"])
		}
		if got := int(payload["max_results"].(float64)); got != 5 {
			t.Fatalf("expected max_results capped to backend limit 5, got %d", got)
		}
		domains, ok := payload["include_domains"].([]interface{})
		if !ok || len(domains) != 1 || domains[0] != "example.com" {
			t.Fatalf("expected token domain allowlist to become include_domains, got %#v", payload["include_domains"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"answer":"Example answer",
			"results":[
				{"title":"Allowed","url":"https://example.com/a","content":"allowed snippet","score":0.9},
				{"title":"Blocked","url":"https://other.test/b","content":"blocked snippet","score":0.8}
			]
		}`))
	}))
	defer upstream.Close()
	allowLocalRelayUpstream(t, upstream.URL)

	user := &model.User{
		Id:          2301,
		Username:    "search_user",
		Password:    "password123",
		DisplayName: "search_user",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       billing.USD(1),
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:                    3301,
		UserId:                user.Id,
		Key:                   "searchroutetoken",
		Status:                common.TokenStatusEnabled,
		ExpiredTime:           -1,
		RemainQuota:           billing.USD(1),
		SearchLimitsEnabled:   true,
		SearchProviders:       model.StringSlice{"tavily"},
		SearchDomainAllowlist: "example.com",
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	priority := int64(10)
	baseURL := upstream.URL
	channel := &model.Channel{
		Id:       4301,
		Type:     constant.ChannelTypeTavily,
		Key:      "tavily-test-key",
		Status:   common.ChannelStatusEnabled,
		Name:     "tavily-search-test",
		BaseURL:  &baseURL,
		Priority: &priority,
		Group:    model.ChannelLaneDirect,
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{SearchBackend: &dto.SearchBackendConfig{
		Provider:     "tavily",
		Capabilities: []string{dto.SearchCapabilityWebSearch, dto.SearchCapabilityAnswer},
		MaxResults:   5,
		TimeoutMs:    5000,
	}})
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}

	engine := gin.New()
	SetRelayRouter(engine)
	body := []byte(`{"query":"example","backend":"auto","max_results":10,"include_answer":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-searchroutetoken")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}
	var payload dto.SearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if payload.ProviderTrace.Backend != "tavily" {
		t.Fatalf("expected tavily backend, got %+v", payload.ProviderTrace)
	}
	if payload.Answer == nil || *payload.Answer != "Example answer" {
		t.Fatalf("expected answer from provider, got %#v", payload.Answer)
	}
	if len(payload.Results) != 1 || payload.Results[0].URL != "https://example.com/a" {
		t.Fatalf("expected domain-filtered result, got %+v", payload.Results)
	}
	if payload.Usage.WebSearchRequests != 1 {
		t.Fatalf("expected one web search request, got %+v", payload.Usage)
	}
}

func TestSetRelayRouter_SearchIgnoresDomainAllowlistWhenLimitsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	oldLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	defer func() {
		common.LogConsumeEnabled = oldLogConsumeEnabled
	}()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		if _, ok := payload["include_domains"]; ok {
			t.Fatalf("did not expect include_domains when search limits are disabled, got %#v", payload["include_domains"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results":[
				{"title":"Allowed","url":"https://example.com/a","content":"allowed snippet"},
				{"title":"Other","url":"https://other.test/b","content":"other snippet"}
			]
		}`))
	}))
	defer upstream.Close()
	allowLocalRelayUpstream(t, upstream.URL)

	user := &model.User{
		Id:          2302,
		Username:    "search_unrestricted_user",
		Password:    "password123",
		DisplayName: "search_unrestricted_user",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       billing.USD(1),
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:                    3302,
		UserId:                user.Id,
		Key:                   "searchunrestrictedtoken",
		Status:                common.TokenStatusEnabled,
		ExpiredTime:           -1,
		RemainQuota:           billing.USD(1),
		SearchLimitsEnabled:   false,
		SearchDomainAllowlist: "example.com",
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	priority := int64(10)
	baseURL := upstream.URL
	channel := &model.Channel{
		Id:       4302,
		Type:     constant.ChannelTypeTavily,
		Key:      "tavily-test-key",
		Status:   common.ChannelStatusEnabled,
		Name:     "tavily-unrestricted-test",
		BaseURL:  &baseURL,
		Priority: &priority,
		Group:    model.ChannelLaneDirect,
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{SearchBackend: &dto.SearchBackendConfig{
		Provider:     "tavily",
		Capabilities: []string{dto.SearchCapabilityWebSearch},
		MaxResults:   5,
		TimeoutMs:    5000,
	}})
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}

	engine := gin.New()
	SetRelayRouter(engine)
	body := []byte(`{"query":"example","backend":"auto"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-searchunrestrictedtoken")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}
	var payload dto.SearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if len(payload.Results) != 2 {
		t.Fatalf("expected unrestricted results, got %+v", payload.Results)
	}
}

func TestSetRelayRouter_ListModelsAcceptsNormalizedTokenModelLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2201,
		Username:    "relay_catalog_tester",
		Password:    "password123",
		DisplayName: "relay_catalog_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	token := &model.Token{
		Id:                 3201,
		UserId:             user.Id,
		Key:                "relaycatalogtoken",
		Status:             common.TokenStatusEnabled,
		ExpiredTime:        -1,
		RemainQuota:        100,
		ModelLimitsEnabled: true,
		ModelLimits:        "gpt-4o-gizmo-*",
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	channel := &model.Channel{
		Id:     4201,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "relay-channel",
		Models: "gpt-4o-gizmo-demo",
		Group:  "relay-group",
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}

	ability := &model.Ability{
		Group:     "relay-group",
		Model:     "gpt-4o-gizmo-demo",
		ChannelId: channel.Id,
		Enabled:   true,
	}
	if err := model.CoreDB().Create(ability).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	if err := (&model.Model{
		ModelName:       "gpt-4o-gizmo-demo",
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
	}).Insert(); err != nil {
		t.Fatalf("failed to insert model: %v", err)
	}

	model.RefreshPricing()

	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-relaycatalogtoken")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "gpt-4o-gizmo-demo" {
		t.Fatalf("expected relay model list to contain normalized match, got %+v", payload.Data)
	}
}

func TestSetRelayRouter_ListModelsPublicRichResponseWithoutAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	meta := &model.Model{
		Id:        5301,
		ModelName: "public-rich-model",
		Status:    1,
		Capabilities: model.ModelCapabilities{
			SupportsFunctionCalling: true,
			SupportsVision:          true,
		},
	}
	if err := meta.Insert(); err != nil {
		t.Fatalf("failed to insert model: %v", err)
	}

	channel := &model.Channel{
		Id:     4301,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "public-rich-channel",
		Models: "public-rich-model",
		Group:  "public-group",
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     "public-group",
		Model:     "public-rich-model",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	model.RefreshPricing()

	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID           string `json:"id"`
			Object       string `json:"object"`
			Capabilities struct {
				SupportsFunctionCalling bool `json:"supports_function_calling"`
				SupportsVision          bool `json:"supports_vision"`
			} `json:"capabilities"`
			Accessible *bool `json:"accessible,omitempty"`
			YourPrice  any   `json:"your_price,omitempty"`
		} `json:"data"`
		YourPricingContext any `json:"your_pricing_context,omitempty"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Object != "list" || len(payload.Data) != 1 {
		t.Fatalf("expected one list item, got %+v", payload)
	}
	item := payload.Data[0]
	if item.ID != "public-rich-model" || item.Object != "model" {
		t.Fatalf("unexpected model item: %+v", item)
	}
	if !item.Capabilities.SupportsFunctionCalling || !item.Capabilities.SupportsVision {
		t.Fatalf("expected structured capabilities in response, got %+v", item.Capabilities)
	}
	if item.Accessible != nil || item.YourPrice != nil || payload.YourPricingContext != nil {
		t.Fatalf("public response leaked personalized pricing fields: %+v", payload)
	}
}

func TestSetRelayRouter_ListModelsIncludesOptionalHealth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	meta := &model.Model{
		Id:        5303,
		ModelName: "health-model",
		Status:    1,
	}
	if err := meta.Insert(); err != nil {
		t.Fatalf("failed to insert model: %v", err)
	}

	channel := &model.Channel{
		Id:           4303,
		Type:         constant.ChannelTypeOpenAI,
		Key:          "upstream-key",
		Status:       common.ChannelStatusEnabled,
		Name:         "health-channel",
		Models:       "health-model",
		Group:        "health-group",
		ResponseTime: 123,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     "health-group",
		Model:     "health-model",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	model.RefreshPricing()

	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/v1/models?include=health", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Data []struct {
			ID     string `json:"id"`
			Health *struct {
				Status       string   `json:"status"`
				LatencyP50Ms *int     `json:"latency_p50_ms"`
				LatencyP95Ms *int     `json:"latency_p95_ms"`
				Availability *float64 `json:"availability"`
			} `json:"health"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "health-model" {
		t.Fatalf("expected health-model, got %+v", payload.Data)
	}
	health := payload.Data[0].Health
	if health == nil {
		t.Fatalf("expected optional health payload")
	}
	if health.Status != "unknown" {
		t.Fatalf("expected unsampled health to remain unknown, got %+v", health)
	}
	if health.LatencyP50Ms != nil || health.LatencyP95Ms != nil {
		t.Fatalf("expected no synthetic latency for an unsampled route, got %+v", health)
	}
	if health.Availability != nil {
		t.Fatalf("expected no availability without evidence, got %+v", health)
	}
}

func TestSetRelayRouter_ListModelsHealthDeduplicatesChannelGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	if err := (&model.Model{
		Id:        5305,
		ModelName: "dedupe-health-model",
		Status:    1,
	}).Insert(); err != nil {
		t.Fatalf("failed to insert model: %v", err)
	}

	activeChannel := &model.Channel{
		Id:           4305,
		Type:         constant.ChannelTypeOpenAI,
		Key:          "upstream-key",
		Status:       common.ChannelStatusEnabled,
		Name:         "dedupe-health-active-channel",
		Models:       "dedupe-health-model",
		Group:        "health-group-a,health-group-b",
		ResponseTime: 100,
	}
	disabledChannel := &model.Channel{
		Id:           4306,
		Type:         constant.ChannelTypeOpenAI,
		Key:          "upstream-key",
		Status:       common.ChannelStatusManuallyDisabled,
		Name:         "dedupe-health-disabled-channel",
		Models:       "dedupe-health-model",
		Group:        "health-group-a",
		ResponseTime: 900,
	}
	if err := model.CoreDB().Create(activeChannel).Error; err != nil {
		t.Fatalf("failed to insert active channel: %v", err)
	}
	if err := model.CoreDB().Create(disabledChannel).Error; err != nil {
		t.Fatalf("failed to insert disabled channel: %v", err)
	}
	for _, ability := range []model.Ability{
		{Group: "health-group-a", Model: "dedupe-health-model", ChannelId: activeChannel.Id, Enabled: true},
		{Group: "health-group-b", Model: "dedupe-health-model", ChannelId: activeChannel.Id, Enabled: true},
		{Group: "health-group-a", Model: "dedupe-health-model", ChannelId: disabledChannel.Id, Enabled: true},
	} {
		if err := model.CoreDB().Create(&ability).Error; err != nil {
			t.Fatalf("failed to insert ability: %v", err)
		}
	}
	model.RefreshPricing()

	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/v1/models?include=health", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Data []struct {
			ID     string `json:"id"`
			Health *struct {
				Status       string   `json:"status"`
				LatencyP50Ms *int     `json:"latency_p50_ms"`
				Availability *float64 `json:"availability"`
			} `json:"health"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "dedupe-health-model" {
		t.Fatalf("expected dedupe-health-model, got %+v", payload.Data)
	}
	health := payload.Data[0].Health
	if health == nil {
		t.Fatalf("expected optional health payload")
	}
	if health.Status != "unknown" {
		t.Fatalf("expected unsampled health to remain unknown, got %+v", health)
	}
	if health.Availability != nil {
		t.Fatalf("expected no availability without lane evidence, got %+v", health)
	}
	if health.LatencyP50Ms != nil {
		t.Fatalf("expected no response-time fallback without observations, got %+v", health)
	}
}

func TestSetRouter_ModelsJSONServesPublicCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	if err := (&model.Model{ModelName: "static-model", Status: 1}).Insert(); err != nil {
		t.Fatalf("failed to insert model: %v", err)
	}
	channel := &model.Channel{
		Id:     4304,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "static-channel",
		Models: "static-model",
		Group:  "static-group",
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     "static-group",
		Model:     "static-model",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	model.RefreshPricing()
	controller.InvalidateModelDiscoveryCache()

	engine := gin.New()
	SetRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/models.json", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}
	// The payload carries lane health now, so it cannot be cached for an hour:
	// a CDN would keep advertising an outage long after recovery.
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=60, s-maxage=60" {
		t.Fatalf("unexpected cache header %q", got)
	}
	if got := w.Header().Get("X-Lazu-Models-Source"); got != "/v1/models" {
		t.Fatalf("unexpected source header %q", got)
	}

	var payload struct {
		Data []struct {
			ID         string `json:"id"`
			Accessible *bool  `json:"accessible"`
			YourPrice  any    `json:"your_price"`
		} `json:"data"`
		YourPricingContext any `json:"your_pricing_context"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload.Data) != 1 || payload.Data[0].ID != "static-model" {
		t.Fatalf("expected static-model in public catalog, got %+v", payload.Data)
	}
	if payload.Data[0].Accessible != nil || payload.Data[0].YourPrice != nil || payload.YourPricingContext != nil {
		t.Fatalf("static catalog leaked personalized fields: %+v", payload)
	}
}

func TestSetRouter_APIPreflightUsesConfiguredCORSOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CORS_ALLOW_ORIGINS", "https://lazu.ai")

	engine := gin.New()
	SetRouter(engine)

	req := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	req.Header.Set("Origin", "https://lazu.ai")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "Cache-Control, Content-Type")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://lazu.ai" {
		t.Fatalf("unexpected allow-origin header %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("unexpected allow-credentials header %q", got)
	}
	allowedHeaders := w.Header().Get("Access-Control-Allow-Headers")
	if allowedHeaders == "*" {
		t.Fatal("credentialed CORS responses must not use a wildcard allow-headers value")
	}
	for _, header := range []string{"Cache-Control", "Content-Type"} {
		if !strings.Contains(strings.ToLower(allowedHeaders), strings.ToLower(header)) {
			t.Fatalf("expected allow-headers to contain %q, got %q", header, allowedHeaders)
		}
	}
}

func TestSetRouter_MetricsExposesPrometheusText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withMetricsConfig(t, true, "metrics-secret")
	engine := gin.New()
	SetRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("X-Metrics-Token", "metrics-secret")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", got)
	}
	body := w.Body.String()
	for _, want := range []string{
		"# HELP lazu_info",
		"lazu_system_cpu_usage_percent",
		"lazu_hot_path_metrics_enabled",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}

func TestSetRouter_MetricsDisabledByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withMetricsConfig(t, false, "")
	engine := gin.New()
	SetRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected disabled metrics to return 404, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestSetRouter_MetricsEnabledWithoutTokenStaysHidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withMetricsConfig(t, true, "")
	engine := gin.New()
	SetRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected enabled metrics without token to return 404, got %d, body=%s", w.Code, w.Body.String())
	}
}

func TestSetRouter_MetricsRequiresConfiguredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withMetricsConfig(t, true, "metrics-secret")
	engine := gin.New()
	SetRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing metrics token to return 401, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer metrics-secret")
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected bearer token to allow metrics scrape, got %d, body=%s", w.Code, w.Body.String())
	}
}

func withMetricsConfig(t *testing.T, enabled bool, token string) {
	t.Helper()
	oldEnabled := common.MetricsEnabled
	oldToken := common.MetricsToken
	common.MetricsEnabled = enabled
	common.MetricsToken = token
	t.Cleanup(func() {
		common.MetricsEnabled = oldEnabled
		common.MetricsToken = oldToken
	})
}

func allowLocalRelayUpstream(t *testing.T, rawURL string) {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	_, port, err := net.SplitHostPort(parsedURL.Host)
	if err != nil {
		t.Fatalf("parse upstream host: %v", err)
	}
	fetchSetting := system_setting.GetFetchSetting()
	oldAllowPrivateIP := fetchSetting.AllowPrivateIp
	oldAllowedPorts := append([]string(nil), fetchSetting.AllowedPorts...)
	fetchSetting.AllowPrivateIp = true
	fetchSetting.AllowedPorts = append(oldAllowedPorts, port)
	t.Cleanup(func() {
		fetchSetting.AllowPrivateIp = oldAllowPrivateIP
		fetchSetting.AllowedPorts = oldAllowedPorts
	})
}

func TestSetRelayRouter_ListModelsWithKeyAddsPersonalizedView(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2301,
		Username:    "relay_personalized_tester",
		Password:    "password123",
		DisplayName: "relay_personalized_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:                 3301,
		UserId:             user.Id,
		Key:                "relaypersonalizedtoken",
		Status:             common.TokenStatusEnabled,
		ExpiredTime:        -1,
		RemainQuota:        100,
		ModelLimitsEnabled: true,
		ModelLimits:        "accessible-model",
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	for _, name := range []string{"accessible-model", "inaccessible-model"} {
		if err := (&model.Model{ModelName: name, Status: 1}).Insert(); err != nil {
			t.Fatalf("failed to insert model %s: %v", name, err)
		}
	}
	channel := &model.Channel{
		Id:     4302,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "personalized-channel",
		Models: "accessible-model,inaccessible-model",
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	for _, name := range []string{"accessible-model", "inaccessible-model"} {
		if err := model.CoreDB().Create(&model.Ability{
			Group:     model.ChannelLaneDirect,
			Model:     name,
			ChannelId: channel.Id,
			Enabled:   true,
		}).Error; err != nil {
			t.Fatalf("failed to insert ability: %v", err)
		}
	}
	model.RefreshPricing()

	engine := gin.New()
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-relaypersonalizedtoken")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Data []struct {
			ID         string `json:"id"`
			Accessible *bool  `json:"accessible"`
			YourPrice  any    `json:"your_price"`
		} `json:"data"`
		YourPricingContext *struct {
			Group          string  `json:"group"`
			Tier           string  `json:"tier"`
			DiscountFactor float64 `json:"discount_factor"`
		} `json:"your_pricing_context"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.YourPricingContext == nil {
		t.Fatalf("expected personalized pricing context")
	}
	seen := map[string]bool{}
	for _, item := range payload.Data {
		if item.Accessible == nil || item.YourPrice == nil {
			t.Fatalf("expected personalized fields for %s: %+v", item.ID, item)
		}
		seen[item.ID] = *item.Accessible
	}
	if seen["accessible-model"] != true {
		t.Fatalf("expected accessible-model to be accessible, got %+v", seen)
	}
	if seen["inaccessible-model"] != false {
		t.Fatalf("expected inaccessible-model to be inaccessible, got %+v", seen)
	}
}
