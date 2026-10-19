package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func TestCategorizeForMetricsExcludesGatewayConversionFailuresFromChannelHealth(t *testing.T) {
	if got := categorizeForMetrics(http.StatusOK, string(types.ErrorCodeConvertRequestFailed)); got != metrics.CategoryClientFault {
		t.Fatalf("conversion failure category = %v, want %v", got, metrics.CategoryClientFault)
	}
}

func TestBuildRoutingOutcomeCapturesStreamTTFTAndChunkCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "req-stream-observability")

	start := time.Now().Add(-2 * time.Second)
	// The stream opened with a metadata frame at 1.25s and the first token
	// arrived at 1.5s. Time-to-first-token is the latter.
	firstResponse := start.Add(1250 * time.Millisecond)
	firstOutput := start.Add(1500 * time.Millisecond)
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, start)
	common.SetContextKey(c, constant.ContextKeyIsStream, true)
	common.SetContextKey(c, constant.ContextKeyProviderAttempted, true)
	common.SetContextKey(c, constant.ContextKeyChannelName, "Final upstream")
	common.SetContextKey(c, constant.ContextKeyRequestErrorCode, string(types.ErrorCodeUpstreamNetworkError))
	common.SetContextKey(c, constant.ContextKeyRelayInfo, &relaycommon.RelayInfo{
		StartTime:         start,
		FirstResponseTime: firstResponse,
		FirstOutputTime:   firstOutput,
		IsStream:          true,
	})

	outcome := buildRoutingOutcome(c, 42)
	if outcome == nil {
		t.Fatal("expected routing outcome")
	}
	if outcome.ChannelName != "Final upstream" {
		t.Fatalf("final channel name was lost: %+v", outcome)
	}
	if outcome.TTFTMs == nil || *outcome.TTFTMs != 1500 {
		t.Fatalf("expected ttft_ms=1500 from the first output event, got %v", outcome.TTFTMs)
	}
	if outcome.ProviderAttempted == nil || !*outcome.ProviderAttempted ||
		outcome.HealthEligible == nil || !*outcome.HealthEligible ||
		outcome.HealthFailed == nil || !*outcome.HealthFailed {
		t.Fatalf("post-header stream failure verdict = %+v, want attempted eligible failure", outcome)
	}
}

func setupDistributorTestDB(t *testing.T) func() {
	t.Helper()

	previousDB := model.CoreDB()
	previousRedisEnabled := common.RedisEnabled
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false

	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()

	if err := db.AutoMigrate(
		&model.Project{}, &model.ProjectMember{}, &model.ProjectRemoval{}, &model.ProjectRemovalKey{},
		&model.User{},
		&model.Token{},
		&model.Channel{},
		&model.Ability{},
		&model.Model{},
		&model.ModelMetadata{},
		&model.Vendor{},
		&model.RoutingDecision{},
		&model.RoutingOutcome{},
		&model.EphemeralCredential{},
		&model.EphemeralCredentialAudit{},
	); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	if err := db.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}

	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		common.RedisEnabled = previousRedisEnabled
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
		model.SetCoreDB(previousDB)
		if previousDB != nil {
			model.RefreshPricing()
		}
	}
}

func TestSelectedProviderForCredentialScopePrefersChannelTag(t *testing.T) {
	tag := "openrouter"
	channel := &model.Channel{
		Type: constant.ChannelTypeOpenAI,
		Name: "fallback-provider",
		Tag:  &tag,
	}
	if got := selectedProviderForCredentialScope("openrouter/auto", channel); got != tag {
		t.Fatalf("expected channel tag provider %q, got %q", tag, got)
	}

	channel.Tag = nil
	if got := selectedProviderForCredentialScope("gpt-4o", channel); got != "OpenAI" {
		t.Fatalf("expected channel type fallback, got %q", got)
	}
}

func TestDistributePersistsPinnedChannelRoutingOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2901,
		Username:    "pinned_channel_admin",
		Password:    "password123",
		DisplayName: "pinned_channel_admin",
		Role:        common.RoleRootUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:          3901,
		UserId:      user.Id,
		Key:         strings.Repeat("p", 48),
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
		Group:       model.ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}
	channel := &model.Channel{
		Id:     4901,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "pinned-channel",
		Models: "gpt-pinned-health",
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     "gpt-pinned-health",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	if err := (&model.Model{
		ModelName:       "gpt-pinned-health",
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
	}).Insert(); err != nil {
		t.Fatalf("failed to insert model metadata: %v", err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-pinned-channel-health")
		c.Next()
	})
	engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyProviderAttempted, true)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	body, err := json.Marshal(gin.H{
		"model":    "gpt-pinned-health",
		"messages": []gin.H{{"role": "user", "content": "hello"}},
	})
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.Key+"-4901")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Lazu-Lane"); got != model.ChannelLaneDirect {
		t.Fatalf("pinned route lane header = %q, want direct", got)
	}
	var decision model.RoutingDecision
	if err := model.CoreDB().Where("request_id = ?", "req-pinned-channel-health").First(&decision).Error; err != nil {
		t.Fatalf("expected pinned routing decision: %v", err)
	}
	if decision.ChoseChannel == nil || *decision.ChoseChannel != channel.Id ||
		decision.ResolvedModel != "gpt-pinned-health" || decision.StrategyName != "token_specific_channel" {
		t.Fatalf("unexpected pinned routing decision: %+v", decision)
	}
	var outcome model.RoutingOutcome
	if err := model.CoreDB().Where("request_id = ?", "req-pinned-channel-health").First(&outcome).Error; err != nil {
		t.Fatalf("expected pinned routing outcome: %v", err)
	}
	if outcome.DecisionID != decision.ID || outcome.HealthEligible == nil || !*outcome.HealthEligible {
		t.Fatalf("unexpected pinned routing outcome: %+v", outcome)
	}
}

func TestDistribute_AllowsExactAndNormalizedTokenModelLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		tokenLimits  string
		requestModel string
	}{
		{
			name:         "allows exact model from token UI selection",
			tokenLimits:  "gpt-4o-gizmo-demo",
			requestModel: "gpt-4o-gizmo-demo",
		},
		{
			name:         "allows normalized wildcard model family",
			tokenLimits:  "gpt-4o-gizmo-*",
			requestModel: "gpt-4o-gizmo-demo",
		},
	}

	for idx, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := setupDistributorTestDB(t)
			defer cleanup()

			user := &model.User{
				Id:          2300 + idx,
				Username:    "distributor_tester",
				Password:    "password123",
				DisplayName: "distributor_tester",
				Role:        common.RoleCommonUser,
				Status:      common.UserStatusEnabled,
			}
			if err := model.CoreDB().Create(user).Error; err != nil {
				t.Fatalf("failed to insert user: %v", err)
			}

			token := &model.Token{
				Id:                 3300 + idx,
				UserId:             user.Id,
				Key:                "distributortoken" + string(rune('a'+idx)),
				Status:             common.TokenStatusEnabled,
				ExpiredTime:        -1,
				RemainQuota:        100,
				ModelLimitsEnabled: true,
				ModelLimits:        tc.tokenLimits,
			}
			if err := token.Insert(); err != nil {
				t.Fatalf("failed to insert token: %v", err)
			}

			channel := &model.Channel{
				Id:     4300 + idx,
				Type:   constant.ChannelTypeOpenAI,
				Key:    "upstream-key",
				Status: common.ChannelStatusEnabled,
				Name:   "distributor-channel",
				Models: tc.requestModel,
				Group:  "direct",
			}
			if err := model.CoreDB().Create(channel).Error; err != nil {
				t.Fatalf("failed to insert channel: %v", err)
			}

			ability := &model.Ability{
				Group:     "direct",
				Model:     tc.requestModel,
				ChannelId: channel.Id,
				Enabled:   true,
			}
			if err := model.CoreDB().Create(ability).Error; err != nil {
				t.Fatalf("failed to insert ability: %v", err)
			}
			if err := (&model.Model{
				ModelName:       tc.requestModel,
				Status:          1,
				LifecycleStatus: model.ModelLifecycleActive,
			}).Insert(); err != nil {
				t.Fatalf("failed to insert model metadata: %v", err)
			}

			model.RefreshPricing()

			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				c.Set(common.RequestIdKey, "req-distributor-"+tc.name)
				c.Next()
			})
			engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{
					"model":      c.GetString("original_model"),
					"channel_id": common.GetContextKeyInt(c, constant.ContextKeyChannelId),
				})
			})

			body, err := json.Marshal(gin.H{
				"model":    tc.requestModel,
				"messages": []gin.H{{"role": "user", "content": "hello"}},
			})
			if err != nil {
				t.Fatalf("failed to marshal request body: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
			}

			var payload map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if payload["model"] != tc.requestModel {
				t.Fatalf("expected original model %q, got %v", tc.requestModel, payload["model"])
			}
			if payload["channel_id"] == nil || payload["channel_id"] == float64(0) {
				t.Fatalf("expected selected channel id, got %v", payload["channel_id"])
			}

			var outcome model.RoutingOutcome
			if err := model.CoreDB().Where("request_id = ?", "req-distributor-"+tc.name).First(&outcome).Error; err != nil {
				t.Fatalf("expected routing outcome to be recorded: %v", err)
			}
			if outcome.DecisionID == 0 {
				t.Fatal("expected outcome to reference a routing decision")
			}
			if outcome.StatusCode == nil || *outcome.StatusCode != http.StatusOK {
				t.Fatalf("expected status 200 outcome, got %#v", outcome.StatusCode)
			}
			if outcome.ChannelID == nil || *outcome.ChannelID != channel.Id {
				t.Fatalf("expected channel id %d, got %#v", channel.Id, outcome.ChannelID)
			}
		})
	}
}

func TestDistributeRejectsTokenDeniedFallbackModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2401,
		Username:    "fallback_limit_tester",
		Password:    "password123",
		DisplayName: "fallback_limit_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	token := &model.Token{
		Id:                 3401,
		UserId:             user.Id,
		Key:                "fallbacklimittoken",
		Status:             common.TokenStatusEnabled,
		ExpiredTime:        -1,
		RemainQuota:        100,
		ModelLimitsEnabled: true,
		ModelLimits:        "gpt-primary-allowed",
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	for _, modelName := range []string{"gpt-primary-allowed", "gpt-fallback-denied-token"} {
		if err := (&model.Model{
			ModelName:       modelName,
			Status:          1,
			LifecycleStatus: model.ModelLifecycleActive,
		}).Insert(); err != nil {
			t.Fatalf("failed to insert model metadata %s: %v", modelName, err)
		}
	}

	channel := &model.Channel{
		Id:     4401,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "fallback-denied-channel",
		Models: "gpt-fallback-denied-token",
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     "gpt-fallback-denied-token",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to insert fallback ability: %v", err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-fallback-token-limit")
		c.Next()
	})
	engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		t.Fatal("request should have been denied before selecting the fallback model")
	})

	body, err := json.Marshal(gin.H{
		"model":    "gpt-primary-allowed",
		"models":   []string{"gpt-fallback-denied-token"},
		"messages": []gin.H{{"role": "user", "content": "hello"}},
	})
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+token.Key)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected fallback model denial status 403, got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(string(types.ErrorCodeModelNotAllowed))) {
		t.Fatalf("expected model_not_allowed error, got body=%s", w.Body.String())
	}
}

func TestDistributeAuthorizesEphemeralCredentialScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	vendor := &model.Vendor{Id: 1, PublicID: "vendor-openai", Name: "OpenAI"}
	if err := model.CoreDB().Create(vendor).Error; err != nil {
		t.Fatalf("failed to insert vendor: %v", err)
	}
	for _, modelName := range []string{"gpt-ephemeral", "gpt-denied"} {
		if err := model.CoreDB().Create(&model.Model{
			ModelName:       modelName,
			VendorID:        vendor.Id,
			Status:          1,
			LifecycleStatus: model.ModelLifecycleActive,
			NameRule:        model.NameRuleExact,
		}).Error; err != nil {
			t.Fatalf("failed to insert model %s: %v", modelName, err)
		}
	}
	user := &model.User{
		Id:          2701,
		Username:    "ephemeral_distributor_tester",
		Password:    "password123",
		DisplayName: "ephemeral_distributor_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:          3701,
		UserId:      user.Id,
		Key:         "ephemeraldistributortoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
		Group:       model.ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}
	channel := &model.Channel{
		Id:     4701,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "ephemeral-channel",
		Models: "gpt-ephemeral,gpt-denied",
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	for _, modelName := range []string{"gpt-ephemeral", "gpt-denied"} {
		if err := model.CoreDB().Create(&model.Ability{
			Group:     model.ChannelLaneDirect,
			Model:     modelName,
			ChannelId: channel.Id,
			Enabled:   true,
		}).Error; err != nil {
			t.Fatalf("failed to insert ability %s: %v", modelName, err)
		}
	}
	model.RefreshPricing()

	issued, err := model.IssueEphemeralCredential(model.IssueEphemeralCredentialInput{
		UserID:     user.Id,
		TokenID:    token.Id,
		SessionID:  "sess-ephemeral",
		AgentID:    "codex",
		Models:     []string{"gpt-ephemeral"},
		Providers:  []string{"openai"},
		TTLSeconds: 300,
	})
	if err != nil {
		t.Fatalf("failed to issue credential: %v", err)
	}

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-ephemeral")
		c.Next()
	})
	engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"channel_id": common.GetContextKeyInt(c, constant.ContextKeyChannelId),
			"token_id":   c.GetInt("token_id"),
		})
	})

	request := func(modelName string) *httptest.ResponseRecorder {
		body, err := json.Marshal(gin.H{
			"model":    modelName,
			"messages": []gin.H{{"role": "user", "content": "hello"}},
		})
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+issued.Credential)
		req.Header.Set("X-Session-Id", "sess-ephemeral")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	okResponse := request("gpt-ephemeral")
	if okResponse.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", okResponse.Code, okResponse.Body.String())
	}
	deniedResponse := request("gpt-denied")
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}
	var useAudit, deniedAudit model.EphemeralCredentialAudit
	if err := model.CoreDB().Where("event_type = ?", model.EphemeralCredentialAuditUse).First(&useAudit).Error; err != nil {
		t.Fatalf("expected credential use audit: %v", err)
	}
	if err := model.CoreDB().Where("event_type = ?", model.EphemeralCredentialAuditDenied).First(&deniedAudit).Error; err != nil {
		t.Fatalf("expected credential denied audit: %v", err)
	}
	if useAudit.SessionID != "sess-ephemeral" || useAudit.ModelName != "gpt-ephemeral" || useAudit.Provider != "openai" {
		t.Fatalf("unexpected use audit: %+v", useAudit)
	}
	if deniedAudit.ModelName != "gpt-denied" || deniedAudit.RequestID != "req-ephemeral" {
		t.Fatalf("unexpected denied audit: %+v", deniedAudit)
	}
}

func TestDistributeAuthorizesEphemeralCredentialAfterFallbackRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	vendor := &model.Vendor{Id: 2, PublicID: "vendor-openai-fallback", Name: "OpenAI"}
	if err := model.CoreDB().Create(vendor).Error; err != nil {
		t.Fatalf("failed to insert vendor: %v", err)
	}
	if err := model.CoreDB().Create(&model.Model{
		ModelName:       "gpt-fallback-denied",
		VendorID:        vendor.Id,
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
		NameRule:        model.NameRuleExact,
	}).Error; err != nil {
		t.Fatalf("failed to insert fallback model: %v", err)
	}
	user := &model.User{
		Id:          2702,
		Username:    "ephemeral_fallback_tester",
		Password:    "password123",
		DisplayName: "ephemeral_fallback_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:          3702,
		UserId:      user.Id,
		Key:         "ephemeralfallbacktoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
		Group:       model.ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}
	channel := &model.Channel{
		Id:     4702,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "ephemeral-fallback-channel",
		Models: "gpt-fallback-denied",
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     "gpt-fallback-denied",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to insert fallback ability: %v", err)
	}
	model.RefreshPricing()

	issued, err := model.IssueEphemeralCredential(model.IssueEphemeralCredentialInput{
		UserID:     user.Id,
		TokenID:    token.Id,
		SessionID:  "sess-ephemeral-fallback",
		AgentID:    "codex",
		Models:     []string{"gpt-primary-allowed"},
		Providers:  []string{"openai"},
		TTLSeconds: 300,
	})
	if err != nil {
		t.Fatalf("failed to issue credential: %v", err)
	}

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-ephemeral-fallback")
		c.Next()
	})
	engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		t.Fatal("request should have been denied before relay handler")
	})

	body, err := json.Marshal(gin.H{
		"model":    "gpt-primary-allowed",
		"models":   []string{"gpt-fallback-denied"},
		"messages": []gin.H{{"role": "user", "content": "hello"}},
	})
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issued.Credential)
	req.Header.Set("X-Session-Id", "sess-ephemeral-fallback")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 after fallback selection, got %d body=%s", w.Code, w.Body.String())
	}
	var deniedAudit model.EphemeralCredentialAudit
	if err := model.CoreDB().Where("event_type = ?", model.EphemeralCredentialAuditDenied).First(&deniedAudit).Error; err != nil {
		t.Fatalf("expected credential denied audit: %v", err)
	}
	if deniedAudit.ModelName != "gpt-fallback-denied" || deniedAudit.Provider != "openai" {
		t.Fatalf("expected final fallback model/provider in denied audit, got %+v", deniedAudit)
	}
}

func TestDistributeDoesNotRecordDecisionBeforeDeniedEphemeralCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	vendor := &model.Vendor{Id: 4, PublicID: "vendor-openai-shadow", Name: "OpenAI"}
	if err := model.CoreDB().Create(vendor).Error; err != nil {
		t.Fatalf("failed to insert vendor: %v", err)
	}
	if err := model.CoreDB().Create(&model.Model{
		ModelName:       "gpt-shadow-denied",
		VendorID:        vendor.Id,
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
		NameRule:        model.NameRuleExact,
	}).Error; err != nil {
		t.Fatalf("failed to insert model: %v", err)
	}
	user := &model.User{
		Id:          2704,
		Username:    "ephemeral_shadow_tester",
		Password:    "password123",
		DisplayName: "ephemeral_shadow_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:          3704,
		UserId:      user.Id,
		Key:         "ephemeralshadowtoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
		Group:       model.ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}
	channel := &model.Channel{
		Id:     4704,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "ephemeral-shadow-channel",
		Models: "gpt-shadow-denied",
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     "gpt-shadow-denied",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	model.RefreshPricing()

	issued, err := model.IssueEphemeralCredential(model.IssueEphemeralCredentialInput{
		UserID:     user.Id,
		TokenID:    token.Id,
		SessionID:  "sess-ephemeral-shadow",
		AgentID:    "codex",
		Models:     []string{"gpt-shadow-allowed"},
		Providers:  []string{"openai"},
		TTLSeconds: 300,
	})
	if err != nil {
		t.Fatalf("failed to issue credential: %v", err)
	}

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-ephemeral-shadow-denied")
		c.Next()
	})
	engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		t.Fatal("request should have been denied before relay handler")
	})

	body, err := json.Marshal(gin.H{
		"model":    "gpt-shadow-denied",
		"messages": []gin.H{{"role": "user", "content": "hello"}},
	})
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issued.Credential)
	req.Header.Set("X-Session-Id", "sess-ephemeral-shadow")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 for denied credential, got %d body=%s", w.Code, w.Body.String())
	}
	var decisions int64
	if err := model.CoreDB().Model(&model.RoutingDecision{}).Where("request_id = ?", "req-ephemeral-shadow-denied").Count(&decisions).Error; err != nil {
		t.Fatalf("failed to count routing decisions: %v", err)
	}
	if decisions != 0 {
		t.Fatalf("expected credential denial before decision logging, got %d decisions", decisions)
	}
}

func TestDistributeValidatesEphemeralProviderAgainstSelectedChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	vendor := &model.Vendor{Id: 3, PublicID: "vendor-openai-provider-scope", Name: "OpenAI"}
	if err := model.CoreDB().Create(vendor).Error; err != nil {
		t.Fatalf("failed to insert vendor: %v", err)
	}
	if err := model.CoreDB().Create(&model.Model{
		ModelName:       "gpt-provider-scope",
		VendorID:        vendor.Id,
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
		NameRule:        model.NameRuleExact,
	}).Error; err != nil {
		t.Fatalf("failed to insert model: %v", err)
	}
	user := &model.User{
		Id:          2703,
		Username:    "ephemeral_provider_tester",
		Password:    "password123",
		DisplayName: "ephemeral_provider_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:          3703,
		UserId:      user.Id,
		Key:         "ephemeralprovidertoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
		Group:       model.ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}
	channel := &model.Channel{
		Id:     4703,
		Type:   constant.ChannelTypeAnthropic,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "anthropic-provider-scope-channel",
		Models: "gpt-provider-scope",
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     "gpt-provider-scope",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	model.RefreshPricing()

	issued, err := model.IssueEphemeralCredential(model.IssueEphemeralCredentialInput{
		UserID:     user.Id,
		TokenID:    token.Id,
		SessionID:  "sess-ephemeral-provider",
		AgentID:    "codex",
		Models:     []string{"gpt-provider-scope"},
		Providers:  []string{"openai"},
		TTLSeconds: 300,
	})
	if err != nil {
		t.Fatalf("failed to issue credential: %v", err)
	}

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-ephemeral-provider")
		c.Next()
	})
	engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		t.Fatal("request should have been denied before relay handler")
	})

	body, err := json.Marshal(gin.H{
		"model":    "gpt-provider-scope",
		"messages": []gin.H{{"role": "user", "content": "hello"}},
	})
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issued.Credential)
	req.Header.Set("X-Session-Id", "sess-ephemeral-provider")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 for provider mismatch, got %d body=%s", w.Code, w.Body.String())
	}
	var deniedAudit model.EphemeralCredentialAudit
	if err := model.CoreDB().Where("event_type = ?", model.EphemeralCredentialAuditDenied).First(&deniedAudit).Error; err != nil {
		t.Fatalf("expected credential denied audit: %v", err)
	}
	if deniedAudit.ModelName != "gpt-provider-scope" || deniedAudit.Provider != "anthropic" {
		t.Fatalf("expected selected channel provider in denied audit, got %+v", deniedAudit)
	}
}

func TestDistribute_PreservesBodyWhenModelComesFromGeminiPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:          2401,
		Username:    "gemini_distributor_tester",
		Password:    "password123",
		DisplayName: "gemini_distributor_tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	token := &model.Token{
		Id:          3401,
		UserId:      user.Id,
		Key:         "geminidistributortoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	channel := &model.Channel{
		Id:     4401,
		Type:   constant.ChannelTypeGemini,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "gemini-distributor-channel",
		Models: "gemini-2.5-flash",
		Group:  "direct",
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}

	ability := &model.Ability{
		Group:     "direct",
		Model:     "gemini-2.5-flash",
		ChannelId: channel.Id,
		Enabled:   true,
	}
	if err := model.CoreDB().Create(ability).Error; err != nil {
		t.Fatalf("failed to insert ability: %v", err)
	}
	if err := (&model.Model{
		ModelName:       "gemini-2.5-flash",
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
	}).Insert(); err != nil {
		t.Fatalf("failed to insert model metadata: %v", err)
	}

	model.RefreshPricing()

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-gemini-distributor")
		c.Next()
	})
	engine.POST("/v1beta/models/:modelAndAction", TokenAuth(), Distribute(), func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("failed to read downstream body: %v", err)
		}
		c.JSON(http.StatusOK, gin.H{
			"model":      c.GetString("original_model"),
			"channel_id": common.GetContextKeyInt(c, constant.ContextKeyChannelId),
			"body":       string(body),
		})
	})

	body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+token.Key)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["model"] != "gemini-2.5-flash" {
		t.Fatalf("expected extracted model, got %v", payload["model"])
	}
	if payload["body"] != body {
		t.Fatalf("downstream body was not preserved: %v", payload["body"])
	}
}

func TestDistributeCheapLaneNeverFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name              string
		priced, available bool
	}{
		{name: "missing_price"},
		{name: "no_cheap_channel", priced: true},
		{name: "cheap_available", priced: true, available: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			cleanup := setupDistributorTestDB(t)
			defer cleanup()

			vendor := &model.Vendor{Id: 1, PublicID: "vendor-openai", Name: "OpenAI"}
			if err := model.CoreDB().Create(vendor).Error; err != nil {
				t.Fatalf("failed to insert vendor: %v", err)
			}
			modelMeta := &model.Model{
				ModelName: "gpt-cheap-fallback",
				VendorID:  vendor.Id,
				Status:    1,
				NameRule:  model.NameRuleExact,
			}
			if err := model.CoreDB().Create(modelMeta).Error; err != nil {
				t.Fatalf("failed to insert model meta: %v", err)
			}
			user := &model.User{
				Id:          2501,
				Username:    "cheap_lane_tester",
				Password:    "password123",
				DisplayName: "cheap_lane_tester",
				Role:        common.RoleCommonUser,
				Status:      common.UserStatusEnabled,
			}
			if err := model.CoreDB().Create(user).Error; err != nil {
				t.Fatalf("failed to insert user: %v", err)
			}

			token := &model.Token{
				Id:           3501,
				UserId:       user.Id,
				Key:          "cheaplanetoken",
				Status:       common.TokenStatusEnabled,
				ExpiredTime:  -1,
				RemainQuota:  100,
				VendorGroups: model.TokenVendorGroupMap{"vendor-openai": model.ChannelLaneAgent},
				Group:        model.ChannelLaneDirect,
			}
			if err := token.Insert(); err != nil {
				t.Fatalf("failed to insert token: %v", err)
			}

			channel := &model.Channel{
				Id:     4501,
				Type:   constant.ChannelTypeOpenAI,
				Key:    "upstream-key",
				Status: common.ChannelStatusEnabled,
				Name:   "direct-only-channel",
				Models: "gpt-cheap-fallback",
				Group:  model.ChannelLaneDirect,
			}
			if err := model.CoreDB().Create(channel).Error; err != nil {
				t.Fatalf("failed to insert channel: %v", err)
			}
			if err := model.CoreDB().Create(&model.Ability{
				Group:     model.ChannelLaneDirect,
				Model:     "gpt-cheap-fallback",
				ChannelId: channel.Id,
				Enabled:   true,
			}).Error; err != nil {
				t.Fatalf("failed to insert ability: %v", err)
			}

			if err := model.CoreDB().AutoMigrate(&model.ModelSellPrice{}); err != nil {
				t.Fatal(err)
			}
			if tc.priced {
				price := 1.0
				if err := model.CoreDB().Create(&model.ModelSellPrice{ModelName: modelMeta.ModelName, ChannelGroup: model.ChannelLaneAgent, InputPerMtok: &price, OutputPerMtok: &price, Active: true, PricingVersion: 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.available {
				cheap := *channel
				cheap.Id = 4502
				cheap.PublicID = ""
				cheap.Group = model.ChannelLaneAgent
				if err := model.CoreDB().Create(&cheap).Error; err != nil {
					t.Fatal(err)
				}
				if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneAgent, Model: modelMeta.ModelName, ChannelId: cheap.Id, Enabled: true}).Error; err != nil {
					t.Fatal(err)
				}
			}
			model.RefreshPricing()

			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				c.Set(common.RequestIdKey, "req-agent-lane-fallback")
				c.Next()
				if got := common.GetContextKeyString(c, constant.ContextKeyOriginalModel); got != "gpt-cheap-fallback" {
					t.Errorf("early rejection lost model: %q", got)
				}
			})
			engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{
					"channel_id": common.GetContextKeyInt(c, constant.ContextKeyChannelId),
					"group":      common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
				})
			})

			body, err := json.Marshal(gin.H{
				"model":    "gpt-cheap-fallback",
				"messages": []gin.H{{"role": "user", "content": "hello"}},
			})
			if err != nil {
				t.Fatalf("failed to marshal request body: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if tc.available {
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"group":"cheap"`) {
					t.Fatalf("expected agent-lane route, got %d %s", w.Code, w.Body.String())
				}
				if got := w.Header().Get("X-Lazu-Lane"); got != model.ChannelLaneAgent {
					t.Fatalf("agent-lane route lane header = %q, want cheap", got)
				}
				return
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("expected status 404, got %d, body=%s", w.Code, w.Body.String())
			}
			if got := w.Header().Get("X-Lazu-Lane-Fallback"); got != "" {
				t.Fatalf("cheap must not fall back, got %q", got)
			}
			if !strings.Contains(w.Body.String(), "preferred_lane_unavailable") {
				t.Fatalf("expected explicit agent lane error, got %s", w.Body.String())
			}
		})
	}
}

func TestOutcomeDoesNotInferInterruptionFromLateContextCancellation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, cancel := context.WithCancel(context.Background())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		c.Set(common.RequestIdKey, "req-completed")
		common.SetContextKey(c, constant.ContextKeyIsStream, stream)
		common.SetContextKey(c, constant.ContextKeyProviderAttempted, true)
		if stream {
			c.Header("Content-Type", "text/event-stream")
		}
		_, _ = c.Writer.Write([]byte(strings.Repeat("x", 11059)))
		cancel()
		got := buildRoutingOutcome(c, 42)
		if *got.StreamAborted || *got.StatusCode != 200 || !*got.HealthEligible || *got.HealthFailed {
			t.Fatalf("completed response=%+v", got)
		}
		common.SetContextKey(c, constant.ContextKeyRequestErrorCode, string(types.ErrorCodeDownstreamWriteFailed))
		got = buildRoutingOutcome(c, 42)
		if *got.HealthEligible || *got.HealthFailed || *got.StreamAborted != stream {
			t.Fatalf("downstream failure=%+v", got)
		}
		if categorizeForMetrics(200, got.ErrorCode) != metrics.CategoryClientFault {
			t.Fatal("downstream error penalized upstream")
		}
	}
}

func TestRejectedStreamRequestIsNotAnAbortedStream(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusServiceUnavailable} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Set(common.RequestIdKey, "req-rejected-before-stream")
		common.SetContextKey(c, constant.ContextKeyIsStream, true)
		common.SetContextKey(c, constant.ContextKeyProviderAttempted, status == 503)
		common.SetContextKey(c, constant.ContextKeyRequestErrorCode, "upstream_error")
		c.JSON(status, gin.H{"error": "request rejected"})
		if outcome := buildRoutingOutcome(c, 42); *outcome.StreamAborted {
			t.Fatalf("HTTP %d before SSE was incorrectly counted as an interrupted stream", status)
		}
	}
}

func TestOutcomeSeparatesAnyAttemptFromFinalAttemptAndPreservesZeroReasoning(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set(common.RequestIdKey, "req-retry-selection-failure")
	common.SetContextKey(c, constant.ContextKeyProviderAttempted, false)
	common.SetContextKey(c, constant.ContextKeyAnyProviderAttempted, true)
	common.SetContextKey(c, constant.ContextKeyRequestErrorCode, "read_response_body_failed")
	c.Set("reported_reasoning_tokens", 0)
	c.Writer.WriteHeader(502)
	o := buildRoutingOutcome(c, 42)
	if o.ProviderAttempted == nil || *o.ProviderAttempted || (o.HealthEligible != nil && *o.HealthEligible) {
		t.Fatalf("attempt scopes mixed: %+v", o)
	}
	if o.ReasoningTokens == nil || *o.ReasoningTokens != 0 {
		t.Fatal("reported zero reasoning must not become null")
	}
}

func TestCheapUnavailableClassification(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()
	for _, tc := range []struct {
		name                              string
		configured, enabled, missingPrice bool
		status                            int
	}{
		{"unoffered", false, false, false, 404},
		{"temporarily_disabled", true, false, false, 503},
		{"missing_operator_price", true, true, true, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.configured {
				if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneAgent, Model: tc.name, ChannelId: 42, Enabled: tc.enabled}).Error; err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			abortUnavailableCheapModel(c, tc.name, tc.missingPrice)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if tc.missingPrice && w.Header().Get("x-should-retry") != "false" {
				t.Fatal("configuration failure must discourage retry")
			}
		})
	}
}

func TestSetLaneFallbackHeaderRecordsRequestedLaneForTheLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	setLaneFallbackHeader(c, model.ChannelLaneAgent, model.ChannelLaneDirect)
	if got := common.GetContextKeyString(c, constant.ContextKeyLaneRequested); got != model.ChannelLaneAgent {
		t.Fatalf("lane_requested = %q, want %q", got, model.ChannelLaneAgent)
	}

	same, _ := gin.CreateTestContext(httptest.NewRecorder())
	setLaneFallbackHeader(same, model.ChannelLaneAgent, model.ChannelLaneAgent)
	if got := common.GetContextKeyString(same, constant.ContextKeyLaneRequested); got != "" {
		t.Fatalf("a request served on its own lane must not be marked, got %q", got)
	}
}

func TestCheapUnavailableCachedClassificationDoesNotQueryCore(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()
	common.MemoryCacheEnabled = true
	for _, ability := range []model.Ability{{Group: model.ChannelLaneAgent, Model: "cached-paused", ChannelId: 42}, {Group: model.ChannelLaneAgent, Model: "cached-unpriced", ChannelId: 42, Enabled: true}} {
		if err := model.CoreDB().Create(&ability).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.InitChannelCache()
	queries := 0
	model.CoreDB().Callback().Query().Before("gorm:query").Register("test:cheap_no_core", func(tx *gorm.DB) { queries++; tx.AddError(fmt.Errorf("unexpected warm cheap rejection SQL")) })
	for _, tc := range []struct {
		name         string
		missingPrice bool
		status       int
	}{{"cached-unoffered", false, 404}, {"cached-paused", false, 503}, {"cached-unpriced", true, 503}} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		abortUnavailableCheapModel(c, tc.name, tc.missingPrice)
		if recorder.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.name, recorder.Code, recorder.Body.String())
		}
		if tc.missingPrice && recorder.Header().Get("x-should-retry") != "false" {
			t.Fatal("missing-price retry policy lost")
		}
	}
	if queries != 0 {
		t.Fatalf("cached rejection issued %d Core SQL queries", queries)
	}
}
