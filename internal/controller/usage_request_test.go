package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

func TestBuildUsageRequestUsageCombinesRawAndAnalyticalCacheFields(t *testing.T) {
	log := &model.Log{
		RequestID:        "req-cache-detail",
		PromptTokens:     100,
		CompletionTokens: 30,
	}
	other := map[string]interface{}{
		"cache_tokens":          10,
		"cache_write_tokens":    20,
		"cache_write_5m_tokens": 20,
		"cache_miss_tokens":     70,
	}

	usage := buildUsageRequestUsage(log, other, nil)
	if usage.TotalTokens != 130 {
		t.Fatalf("expected total tokens=130, got %d", usage.TotalTokens)
	}
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionInput, 70, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheRead, 10, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheWrite5m, 20, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheMiss, 70, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionOutput, 30, usagepkg.UnitToken)
}

func TestBuildUsageRequestUsageKeepsBillingFallbackBucketsOutOfUsageDimensions(t *testing.T) {
	log := &model.Log{
		RequestID:        "req-cache-write-unknown",
		PromptTokens:     100,
		CompletionTokens: 30,
	}
	other := map[string]interface{}{
		"cache_write_tokens": 20,
	}

	usage := buildUsageRequestUsage(log, other, nil)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheWrite, 20, usagepkg.UnitToken)
	assertNoUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheWrite5m)
	assertNoUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheWrite1h)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionInput, 80, usagepkg.UnitToken)
}

func TestBuildUsageRequestUsagePreservesClaudeSeparatedInputTokens(t *testing.T) {
	log := &model.Log{
		RequestID:        "req-claude-cache",
		ModelName:        "claude-sonnet-4-20250514",
		PromptTokens:     21,
		CompletionTokens: 8,
	}
	other := map[string]interface{}{
		"claude":                true,
		"cache_tokens":          300,
		"cache_creation_tokens": 500,
		"cache_write_tokens":    500,
	}

	usage := buildUsageRequestUsage(log, other, nil)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionInput, 21, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheRead, 300, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheWrite, 500, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionOutput, 8, usagepkg.UnitToken)
}

func TestBuildUsageRequestUsageFallsBackToRoutingOutcome(t *testing.T) {
	log := &model.Log{
		RequestID:        "req-cache-fallback",
		PromptTokens:     100,
		CompletionTokens: 40,
	}
	cacheRead := 25
	cacheWrite := 15
	audioOutput := 5
	outcome := &model.RoutingOutcome{
		CachedInputTokens: &cacheRead,
		CacheWriteTokens:  &cacheWrite,
		AudioOutputTokens: &audioOutput,
	}

	usage := buildUsageRequestUsage(log, nil, outcome)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionInput, 60, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheRead, 25, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheWrite, 15, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionOutput, 35, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionAudioOutput, 5, usagepkg.UnitToken)
}

func TestBuildUsageRequestUsageReadsLegacyImageOutputAsImageInput(t *testing.T) {
	log := &model.Log{
		RequestID:        "req-image-legacy",
		PromptTokens:     100,
		CompletionTokens: 20,
	}
	other := map[string]interface{}{
		"image_output": 30,
	}

	usage := buildUsageRequestUsage(log, other, nil)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionImageInput, 30, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionInput, 70, usagepkg.UnitToken)
}

func TestUsageProviderRawFieldsOmitsAdminInfo(t *testing.T) {
	raw := usageProviderRawFields(map[string]interface{}{
		"cache_tokens":       10,
		"cache_write_tokens": 20,
		"admin_info":         map[string]interface{}{"use_channel": []string{"secret"}},
	})

	if raw["cache_tokens"] != 10 || raw["cache_write_tokens"] != 20 {
		t.Fatalf("expected cache raw fields, got %#v", raw)
	}
	if _, ok := raw["admin_info"]; ok {
		t.Fatalf("expected admin_info to be omitted, got %#v", raw)
	}
}

func TestBuildUsageRequestUsageAddsToolCallDimensionsFromRawFields(t *testing.T) {
	log := &model.Log{RequestID: "req-tools", PromptTokens: 10, CompletionTokens: 5}
	other := map[string]interface{}{
		"web_search_call_count":  2,
		"file_search_call_count": 1,
		"image_generation_call":  true,
	}

	usage := buildUsageRequestUsage(log, other, nil)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionWebSearch, 2, usagepkg.UnitRequest)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionFileSearch, 1, usagepkg.UnitRequest)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionImageGeneration, 1, usagepkg.UnitRequest)
}

func TestBuildUsageRequestUsagePrefersUsageFact(t *testing.T) {
	fact := usagepkg.Fact{
		Version:          usagepkg.FactVersion,
		PromptTokens:     100,
		CompletionTokens: 30,
		TotalTokens:      130,
		Dimensions: []usagepkg.DimensionUsage{
			{Name: usagepkg.DimensionInput, Quantity: 60, Unit: usagepkg.UnitToken},
			{Name: usagepkg.DimensionCacheRead, Quantity: 40, Unit: usagepkg.UnitToken},
			{Name: usagepkg.DimensionCacheMiss, Quantity: 60, Unit: usagepkg.UnitToken},
		},
	}
	log := &model.Log{
		RequestID:        "req-canonical-fact",
		PromptTokens:     999,
		CompletionTokens: 999,
		UsageFact:        fact.JSONString(),
	}
	other := map[string]interface{}{
		"cache_tokens":       1,
		"cache_miss_tokens":  1,
		"cache_write_tokens": 1,
	}

	usage := buildUsageRequestUsage(log, other, nil)
	if usage.TotalTokens != 130 {
		t.Fatalf("expected usage fact total tokens=130, got %d", usage.TotalTokens)
	}
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionInput, 60, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheRead, 40, usagepkg.UnitToken)
	assertUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheMiss, 60, usagepkg.UnitToken)
	assertNoUsageDimension(t, usage.Dimensions, usagepkg.DimensionCacheWrite)
}

func TestBuildUsageRequestProviderUsagePrefersUsageFact(t *testing.T) {
	fact := usagepkg.Fact{
		Version:        usagepkg.FactVersion,
		ProviderFamily: string(usagepkg.ProviderFamilyDeepSeek),
		PromptTokens:   100,
		TotalTokens:    100,
		ProviderRawUsage: map[string]any{
			"prompt_cache_hit_tokens":  40,
			"prompt_cache_miss_tokens": 60,
		},
	}
	log := &model.Log{
		ModelName: "custom-name-without-provider",
		UsageFact: fact.JSONString(),
	}

	providerUsage := buildUsageRequestProviderUsage(log, map[string]interface{}{
		"cache_tokens": 1,
	})

	if providerUsage.Family != string(usagepkg.ProviderFamilyDeepSeek) {
		t.Fatalf("expected provider family deepseek, got %q", providerUsage.Family)
	}
	if intFromUsageMap(providerUsage.RawFields, "prompt_cache_hit_tokens") != 40 {
		t.Fatalf("expected raw usage from fact, got %#v", providerUsage.RawFields)
	}
	if _, ok := providerUsage.RawFields["cache_tokens"]; ok {
		t.Fatalf("expected fact raw usage to win over legacy fields, got %#v", providerUsage.RawFields)
	}
}

func TestGetUsageRequestDetailMissingRequestIDUsesHTTPError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "request_id", Value: " "}}

	GetUsageRequestDetail(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Error struct {
			Type  string `json:"type"`
			Code  string `json:"code"`
			Param string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Code != "missing_required_parameter" || payload.Error.Param != "request_id" {
		t.Fatalf("unexpected error payload: %+v", payload.Error)
	}
}

func assertUsageDimension(t *testing.T, dimensions []dto.UsageDimension, name string, quantity int64, unit string) {
	t.Helper()
	for _, dimension := range dimensions {
		if dimension.Name == name {
			if dimension.Quantity != quantity || dimension.Unit != unit {
				t.Fatalf("expected dimension %s=%d %s, got %d %s", name, quantity, unit, dimension.Quantity, dimension.Unit)
			}
			return
		}
	}
	t.Fatalf("expected dimension %s in %#v", name, dimensions)
}

func assertNoUsageDimension(t *testing.T, dimensions []dto.UsageDimension, name string) {
	t.Helper()
	for _, dimension := range dimensions {
		if dimension.Name == name {
			t.Fatalf("expected dimension %s to be absent, got %#v", name, dimensions)
		}
	}
}

func TestProjectRequestReceiptHidesInternalRouting(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}, &model.Project{}); err != nil {
		t.Fatal(err)
	}
	old := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(old) })
	if err := db.Create(&model.Project{ID: 9, PublicID: "prj_receipt", OwnerUserID: 4, Name: "Receipt"}).Error; err != nil {
		t.Fatal(err)
	}
	row := model.Log{ProjectID: 9, UserId: 5, TokenId: 7, Type: model.LogTypeConsume, RequestID: "receipt-safe", ModelName: "public-model", PromptTokens: 12, AmountMicrousd: 100, UsageFact: `{"provider_family":"secret-provider","provider_raw_usage":{"secret":"supplier-data"}}`}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	channel, status, duration := 77, 200, 150
	outcome := model.RoutingOutcome{RequestID: row.RequestID, ChannelID: &channel, StatusCode: &status, TotalDurationMs: &duration}
	if err := db.Create(&outcome).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 5)
		c.Set("token_id", 7)
		common.SetContextKey(c, constant.ContextKeyProjectID, 9)
		common.SetContextKey(c, constant.ContextKeyPayerUserID, 4)
	})
	router.GET("/receipt/:request_id", GetUsageRequestDetail)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/receipt/receipt-safe", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, secret := range []string{"secret-", "provider_usage", "channel_id", "decision_id", "strategy", "fallback", "upstream_request_id"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("receipt leaked %s: %s", secret, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), `"amount_microusd":100`) || !strings.Contains(w.Body.String(), `"total_duration_ms":150`) {
		t.Fatalf("customer receipt lost billing or timing: %s", w.Body.String())
	}
}

func TestProjectRequestReceiptPollingDoesNotRecordModelFailure(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.Create(&model.Project{ID: 9, PublicID: "prj_polling", OwnerUserID: 4, Name: "Polling"}).Error; err != nil {
		t.Fatal(err)
	}
	foreign := model.Log{ProjectID: 9, UserId: 5, TokenId: 8, Type: model.LogTypeConsume, RequestID: "foreign-receipt", ModelName: "public-model", AmountMicrousd: 100}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 5)
		c.Set("token_id", 7)
		c.Set("username", "receipt-polling-test")
		c.Set(common.RequestIdKey, "receipt-read-id")
		common.SetContextKey(c, constant.ContextKeyProjectID, 9)
		common.SetContextKey(c, constant.ContextKeyPayerUserID, 4)
	})
	router.GET("/receipt/:request_id", GetUsageRequestDetail)
	for _, requestID := range []string{"pending-receipt", "foreign-receipt"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/receipt/"+requestID, nil))
		var payload struct {
			Error struct {
				Code  string `json:"code"`
				Param string `json:"param"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusNotFound || payload.Error.Code != "request_not_found" || payload.Error.Param != "request_id" || w.Header().Get("X-Request-Id") != "receipt-read-id" {
			t.Fatalf("unexpected receipt miss response: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	row := model.Log{ProjectID: 9, UserId: 5, TokenId: 7, Type: model.LogTypeConsume, RequestID: "pending-receipt", ModelName: "public-model", AmountMicrousd: 36}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/receipt/pending-receipt", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"amount_microusd":36`) {
		t.Fatalf("persisted receipt unavailable: status=%d body=%s", w.Code, w.Body.String())
	}
	var logs int64
	if err := db.Model(&model.Log{}).Count(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if logs != 2 {
		t.Fatalf("receipt reads added usage rows: got %d, want two original consume rows", logs)
	}
	for _, table := range []any{&model.WalletLedger{}, &model.SpendBudgetReservation{}, &model.SpendBudgetCharge{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("receipt reads changed financial table %T: %d rows", table, count)
		}
	}
}
