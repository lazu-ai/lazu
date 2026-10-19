package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupMediaJobControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalSchedule := scheduleProjectNotificationWake
	scheduleProjectNotificationWake = func(int64) {}
	t.Cleanup(func() { scheduleProjectNotificationWake = originalSchedule })
	originalDB := model.CoreDB()
	originalTelemetryDB := model.ConfiguredTelemetryDB()
	originalRedisEnabled := common.RedisEnabled
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalCryptoSecret := common.CryptoSecret
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.CryptoSecret = "media-job-controller-test-secret" // secret-scan: allow -- test fixture
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectMember{}, &model.ProjectRemoval{}, &model.ProjectRemovalKey{}, &model.ProjectNotification{}, &model.ProjectBudgetHistory{},
		&model.User{},
		&model.Token{},
		&model.File{},
		&model.MediaJob{},
		&model.Channel{},
		&model.Ability{},
		&model.Model{},
		&model.ModelMetadata{},
		&model.Vendor{},
		&model.RoutingDecision{},
		&model.RoutingOutcome{},
		&model.RoutingAttemptOutcome{},
		&model.ModelSellPrice{},
		&model.ProviderCostPrice{},
		&model.WalletLedger{},
		&model.SettlementFailure{},
		&model.LedgerLegacySettlementWaiver{},
		&model.SettlementOutbox{},
		&model.SpendBudget{},
		&model.SpendBudgetReservation{},
		&model.SpendBudgetCharge{}, &model.ProjectSpend{},
	); err != nil {
		t.Fatalf("failed to migrate media job tables: %v", err)
	}
	if err := db.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(originalDB)
		model.SetTelemetryDB(originalTelemetryDB)
		common.RedisEnabled = originalRedisEnabled
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.CryptoSecret = originalCryptoSecret
		if originalDB != nil {
			model.RefreshPricing()
		}
	})
	return db
}

func seedMediaJobControllerAuth(t *testing.T, db *gorm.DB) (*model.Token, *model.File) {
	t.Helper()
	user := &model.User{
		Id:          1,
		Username:    "media-job-controller-user",
		DisplayName: "media-job-controller-user",
		Status:      common.UserStatusEnabled,
		Quota:       billing.MicroUSD(1_000_000),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	token := &model.Token{
		Id:          2,
		UserId:      user.Id,
		Key:         "mediajobcontrollertoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: billing.MicroUSD(1_000_000),
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	file := &model.File{
		ID:         "file-lazu-controller-artifact",
		UserID:     user.Id,
		TokenID:    &token.Id,
		Filename:   "artifact.png",
		Purpose:    "vision",
		MimeType:   "image/png",
		Bytes:      512,
		StorageKey: "test/controller-artifact.png",
		Status:     model.FileStatusProcessed,
		CreatedAt:  time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC).Unix(),
	}
	if err := db.Create(file).Error; err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	return token, file
}

func TestMediaJobSnapshotWithContextStoresSelectedLane(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("final_group", model.ChannelLaneAgent)
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 3)

	snapshot := mediaJobSnapshotWithContext(model.JSONValue([]byte(`{"prompt":"draw"}`)), c)
	var fields map[string]any
	if err := common.Unmarshal(snapshot, &fields); err != nil {
		t.Fatalf("failed to unmarshal snapshot: %v", err)
	}
	if fields["final_group"] != model.ChannelLaneAgent || fields["channel_group"] != model.ChannelLaneAgent {
		t.Fatalf("expected selected lane in snapshot, got %+v", fields)
	}
	if fields["channel_multi_key_index"] != float64(3) {
		t.Fatalf("expected selected channel key index in snapshot, got %+v", fields)
	}
}

func TestMediaJobAPISubmitListAndGet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, file := seedMediaJobControllerAuth(t, db)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-media-job-api")
		c.Next()
	})
	group := engine.Group("/v1/media/jobs")
	group.Use(middleware.TokenAuth())
	group.POST("", CreateMediaJob)
	group.GET("", ListMediaJobs)
	group.GET("/:id", GetMediaJob)

	body, err := json.Marshal(gin.H{
		"kind":             "image",
		"model":            "gpt-image-1",
		"artifact_file_id": file.ID,
		"request": gin.H{
			"prompt": "draw",
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/media/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.Key)
	req.Header.Set("X-Session-Id", "sess-media-api")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to decode created job: %v", err)
	}
	id, _ := created["id"].(string)
	if !strings.HasPrefix(id, "mjob_") || created["status"] != model.MediaJobStatusPending {
		t.Fatalf("unexpected created payload: %+v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/media/jobs?status=pending", nil)
	listReq.Header.Set("Authorization", "Bearer "+token.Key)
	listW := httptest.NewRecorder()
	engine.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected list 200, got %d body=%s", listW.Code, listW.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v1/media/jobs/"+id, nil)
	getReq.Header.Set("Authorization", "Bearer "+token.Key)
	getW := httptest.NewRecorder()
	engine.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected get 200, got %d body=%s", getW.Code, getW.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(getW.Body.Bytes(), &detail); err != nil {
		t.Fatalf("failed to decode detail: %v", err)
	}
	if detail["request"] == nil || detail["session_id"] != "sess-media-api" {
		t.Fatalf("unexpected detail payload: %+v", detail)
	}
}

func TestCreateMediaJobRejectsProviderExecutionWithoutArtifact(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-media-job-provider-unavailable")
		c.Next()
	})
	group := engine.Group("/v1/media/jobs")
	group.Use(middleware.TokenAuth())
	group.POST("", CreateMediaJob)

	body, err := json.Marshal(gin.H{
		"kind":  "image",
		"model": "gpt-image-1",
		"request": gin.H{
			"prompt": "draw",
		},
	})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/media/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.Key)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected provider execution to be rejected with 501, got %d body=%s", w.Code, w.Body.String())
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["error"] == nil {
		t.Fatalf("expected OpenAI-style error payload, got %+v", payload)
	}
	var count int64
	if err := db.Model(&model.MediaJob{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count media jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no media job to be queued, got %d", count)
	}
}

func TestCreateMediaJobRejectsDeniedTokenModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	if err := db.Model(token).Updates(map[string]any{
		"model_limits_enabled": true,
		"model_limits":         "allowed-model",
	}).Error; err != nil {
		t.Fatalf("failed to update token model limits: %v", err)
	}
	engine := gin.New()
	group := engine.Group("/v1/media/jobs")
	group.Use(middleware.TokenAuth())
	group.POST("", CreateMediaJob)

	body, err := json.Marshal(gin.H{
		"kind":         "image",
		"model":        "blocked-model",
		"artifact_url": "https://example.com/artifact.png",
	})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/media/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.Key)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected denied model to return 403, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), string(types.ErrorCodeModelNotAllowed)) {
		t.Fatalf("expected model_not_allowed error, got %s", w.Body.String())
	}
	var count int64
	if err := db.Model(&model.MediaJob{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count media jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected denied request to queue no media job, got %d", count)
	}
}

func TestCreateMediaJobRejectsDeniedTokenVendor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	vendor := model.Vendor{Id: 901, Name: "Blocked Vendor", PublicID: "blocked-vendor"}
	if err := db.Create(&vendor).Error; err != nil {
		t.Fatalf("failed to create vendor: %v", err)
	}
	if err := db.Create(&model.Model{
		ModelName:       "blocked-vendor-model",
		VendorID:        vendor.Id,
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
		NameRule:        model.NameRuleExact,
	}).Error; err != nil {
		t.Fatalf("failed to create model metadata: %v", err)
	}
	channel := model.Channel{
		Id:     9010,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "blocked-vendor-upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "blocked-vendor-channel",
		Models: "blocked-vendor-model",
		Group:  model.ChannelLaneDirect,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to create channel: %v", err)
	}
	if err := db.Create(&model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     "blocked-vendor-model",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to create ability: %v", err)
	}
	model.RefreshPricing()
	token.VendorLimitsEnabled = true
	token.VendorLimits = model.IntSlice{vendor.Id}
	if err := db.Save(token).Error; err != nil {
		t.Fatalf("failed to update token vendor limits: %v", err)
	}
	engine := gin.New()
	group := engine.Group("/v1/media/jobs")
	group.Use(middleware.TokenAuth())
	group.POST("", CreateMediaJob)

	body, err := json.Marshal(gin.H{
		"kind":         "image",
		"model":        "blocked-vendor-model",
		"artifact_url": "https://example.com/artifact.png",
	})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/media/jobs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.Key)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected denied vendor to return 403, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), string(types.ErrorCodeTokenVendorDenied)) {
		t.Fatalf("expected token_vendor_denied error, got %s", w.Body.String())
	}
	var count int64
	if err := db.Model(&model.MediaJob{}).Count(&count).Error; err != nil {
		t.Fatalf("failed to count media jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected denied request to queue no media job, got %d", count)
	}
}
