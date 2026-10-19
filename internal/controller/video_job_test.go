package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/metrics"
	"gorm.io/gorm"
)

func seedVideoProviderRoute(t *testing.T, vendorID int, vendorName string, modelName string, channelID int) {
	t.Helper()
	vendor := &model.Vendor{Id: vendorID, PublicID: "vendor-" + strings.ToLower(vendorName), Name: vendorName}
	if err := model.CoreDB().Create(vendor).Error; err != nil {
		t.Fatalf("failed to create vendor %s: %v", vendorName, err)
	}
	if err := model.CoreDB().Create(&model.Model{
		ModelName:       modelName,
		VendorID:        vendorID,
		Status:          1,
		LifecycleStatus: model.ModelLifecycleActive,
		NameRule:        model.NameRuleExact,
		Tags:            "video generation",
	}).Error; err != nil {
		t.Fatalf("failed to create model %s: %v", modelName, err)
	}
	channel := &model.Channel{
		Id:     channelID,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   vendorName + " video",
		Models: modelName,
		Group:  model.ChannelLaneDirect,
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to create channel %s: %v", modelName, err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     modelName,
		ChannelId: channelID,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to create ability %s: %v", modelName, err)
	}
}

func TestSubmitVideoJobSelectsProviderChannelAndCreatesMediaJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, file := seedMediaJobControllerAuth(t, db)
	seedVideoProviderRoute(t, 1, "OpenAI", "sora-2", 6101)
	seedVideoProviderRoute(t, 2, "Alibaba", "wan2.2-video", 6102)
	model.RefreshPricing()
	previousMetrics := metrics.Default
	metrics.Default = metrics.NewInMemoryStore()
	defer func() {
		metrics.Default = previousMetrics
	}()

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-video-job-api")
		c.Next()
	})
	engine.POST("/v1/videos", middleware.TokenAuth(), middleware.SuppressRelayTelemetry(), middleware.Distribute(), SubmitVideoJob)
	engine.GET("/v1/videos/:id", middleware.TokenAuth(), GetVideoJob)

	submit := func(modelName string) map[string]any {
		body, err := json.Marshal(gin.H{
			"model":            modelName,
			"prompt":           "generate a launch video",
			"seconds":          6,
			"artifact_file_id": file.ID,
		})
		if err != nil {
			t.Fatalf("failed to marshal request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token.Key)
		req.Header.Set("X-Session-Id", "sess-video-api")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Fatalf("expected 202 for %s, got %d body=%s", modelName, w.Code, w.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if payload["object"] != "video" || payload["kind"] != model.MediaJobKindVideo || payload["status"] != model.MediaJobStatusPending {
			t.Fatalf("unexpected video payload: %+v", payload)
		}
		return payload
	}

	openAIJob := submit("sora-2")
	alibabaJob := submit("wan2.2-video")
	if _, exists := openAIJob["provider"]; exists {
		t.Fatalf("customer response exposed provider: %+v", openAIJob)
	}
	if _, exists := alibabaJob["provider"]; exists {
		t.Fatalf("customer response exposed provider: %+v", alibabaJob)
	}

	var rows []model.MediaJob
	if err := model.CoreDB().Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("failed to load media jobs: %v", err)
	}
	if len(rows) != 2 || rows[0].Provider != "openai" || rows[1].Provider != "alibaba" || rows[0].Kind != model.MediaJobKindVideo || rows[0].ChannelID == nil || rows[1].ChannelID == nil {
		t.Fatalf("unexpected media jobs: %+v", rows)
	}
	var outcomeCount int64
	if err := model.CoreDB().Model(&model.RoutingOutcome{}).Where("request_id = ?", "req-video-job-api").Count(&outcomeCount).Error; err != nil {
		t.Fatalf("failed to count video routing outcomes: %v", err)
	}
	if outcomeCount != 0 {
		t.Fatalf("queued video submission should not write immediate routing outcome, got %d", outcomeCount)
	}
	for _, channelID := range []int{6101, 6102} {
		snapshot := metrics.Default.Snapshot(channelID)
		if snapshot.RPMCurrent != 0 || snapshot.SamplesCount != 0 || snapshot.ErrorWindowSize != 0 || snapshot.InFlight != 0 {
			t.Fatalf("queued video submission should not update relay health metrics for channel %d: %+v", channelID, snapshot)
		}
	}
}

func TestSubmitVideoJobStartsOpenAICompatibleProviderJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/videos" {
			t.Fatalf("unexpected provider path: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer upstream-key" {
			t.Fatalf("unexpected provider authorization header")
		}
		var jobs []model.MediaJob
		if err := db.Where("request_id = ?", "req-video-provider").Find(&jobs).Error; err != nil {
			t.Fatalf("load durable job during provider request: %v", err)
		}
		if len(jobs) != 1 ||
			jobs[0].Status != model.MediaJobStatusRunning ||
			jobs[0].ChannelID == nil ||
			*jobs[0].ChannelID != 6301 {
			t.Fatalf("provider called before durable submission state: %+v", jobs)
		}
		var pendingSnapshot map[string]any
		if err := common.Unmarshal(jobs[0].RequestSnapshot, &pendingSnapshot); err != nil {
			t.Fatalf("decode pending provider snapshot: %v", err)
		}
		if pendingSnapshot["_provider_execution"] != true ||
			pendingSnapshot[model.MediaJobProviderSubmissionIndeterminateSnapshotKey] != videoSubmissionPersistencePending {
			t.Fatalf("provider call was not protected by a reconciliation marker: %+v", pendingSnapshot)
		}
		var outbound map[string]any
		if err := json.NewDecoder(request.Body).Decode(&outbound); err != nil {
			t.Fatalf("decode effective provider request: %v", err)
		}
		if outbound["seconds"] != float64(7) ||
			outbound["quality"] != "hd" ||
			outbound["seed"] != float64(42) {
			t.Fatalf("provider did not receive effective override and extension fields: %+v", outbound)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"video_upstream_123","status":"queued"}`))
	}))
	defer provider.Close()

	seedVideoProviderRoute(t, 1, "OpenAI", "sora-2", 6301)
	baseURL := provider.URL
	if err := model.CoreDB().Model(&model.Channel{}).Where("id = ?", 6301).Updates(map[string]any{
		"base_url":       baseURL,
		"param_override": `{"seconds":7,"quality":"hd"}`,
	}).Error; err != nil {
		t.Fatalf("update video provider base url: %v", err)
	}
	secondUSD := 0.01
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      "sora-2",
		ChannelGroup:   model.ChannelLaneDirect,
		SecondUSD:      &secondUSD,
		Active:         true,
		PricingVersion: 1,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("seed video sell price: %v", err)
	}
	model.RefreshPricing()
	previousMetrics := metrics.Default
	metrics.Default = metrics.NewInMemoryStore()
	defer func() {
		metrics.Default = previousMetrics
	}()

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-video-provider")
		c.Next()
	})
	engine.POST("/v1/videos", middleware.TokenAuth(), middleware.SuppressRelayTelemetry(), middleware.Distribute(), SubmitVideoJob)

	body := []byte(`{"model":"sora-2","prompt":"A calm launch film","provider":"caller-hint","seconds":4,"size":"1280x720","seed":42}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token.Key)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var job model.MediaJob
	if err := model.CoreDB().Where("request_id = ?", "req-video-provider").First(&job).Error; err != nil {
		t.Fatalf("load submitted video job: %v", err)
	}
	if job.UpstreamJobID != "video_upstream_123" ||
		job.Provider != "openai" ||
		job.ChannelID == nil ||
		*job.ChannelID != 6301 {
		t.Fatalf("unexpected submitted video job: %+v", job)
	}
	if reserved := model.MediaJobBillingReservationAmount(job); reserved != 70_000 {
		t.Fatalf("billing reservation ignored effective seven-second override: %d", reserved)
	}
	var requestSnapshot map[string]any
	if err := common.Unmarshal(job.RequestSnapshot, &requestSnapshot); err != nil {
		t.Fatalf("decode effective billing snapshot: %v", err)
	}
	if requestSnapshot["seconds"] != float64(7) ||
		requestSnapshot["quality"] != "hd" ||
		requestSnapshot["seed"] != float64(42) {
		t.Fatalf("persisted billing snapshot did not match provider request: %+v", requestSnapshot)
	}
	if _, exists := requestSnapshot[model.MediaJobProviderSubmissionIndeterminateSnapshotKey]; exists {
		t.Fatalf("completed submission retained reconciliation marker: %+v", requestSnapshot)
	}
	snapshot := metrics.Default.Snapshot(6301)
	if snapshot.RPMCurrent != 1 || snapshot.SamplesCount != 1 || snapshot.InFlight != 0 {
		t.Fatalf("provider submission telemetry missing: %+v", snapshot)
	}
	var outcome model.RoutingOutcome
	if err := model.CoreDB().Where("request_id = ?", "req-video-provider").First(&outcome).Error; err != nil {
		t.Fatalf("load provider submission routing outcome: %v", err)
	}
	if outcome.ChannelID == nil || *outcome.ChannelID != 6301 ||
		outcome.StatusCode == nil || *outcome.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected provider submission routing outcome: %+v", outcome)
	}
}

func TestSubmitVideoJobPreservesIndeterminateAcceptedSubmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "256")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"accepted_but_truncated`))
	}))
	defer provider.Close()

	seedVideoProviderRoute(t, 31, "OpenAI", "sora-indeterminate", 6310)
	if err := db.Model(&model.Channel{}).Where("id = ?", 6310).Update("base_url", provider.URL).Error; err != nil {
		t.Fatalf("update video provider base url: %v", err)
	}
	secondUSD := 0.01
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      "sora-indeterminate",
		ChannelGroup:   model.ChannelLaneDirect,
		SecondUSD:      &secondUSD,
		Active:         true,
		PricingVersion: 1,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("seed video sell price: %v", err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-video-indeterminate")
		c.Next()
	})
	engine.POST("/v1/videos", middleware.TokenAuth(), middleware.SuppressRelayTelemetry(), middleware.Distribute(), SubmitVideoJob)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewReader([]byte(`{"model":"sora-indeterminate","prompt":"preserve accepted work","seconds":4}`)),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token.Key)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("indeterminate accepted submission should remain a 202 job, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if providerCalls != 1 {
		t.Fatalf("accepted provider response must not retry another submission, calls=%d", providerCalls)
	}

	var job model.MediaJob
	if err := db.Where("request_id = ?", "req-video-indeterminate").First(&job).Error; err != nil {
		t.Fatalf("load indeterminate video job: %v", err)
	}
	if job.Status != model.MediaJobStatusPending ||
		job.UpstreamJobID != "" ||
		job.ErrorCode != videoSubmissionIndeterminateErrorCode ||
		job.ChannelID == nil ||
		*job.ChannelID != 6310 {
		t.Fatalf("accepted response interruption was terminalized or lost: %+v", job)
	}
	if reserved := model.MediaJobBillingReservationAmount(job); reserved != 40_000 {
		t.Fatalf("accepted response interruption refunded its billing reservation: %d", reserved)
	}
	var snapshot map[string]any
	if err := common.Unmarshal(job.RequestSnapshot, &snapshot); err != nil {
		t.Fatalf("decode indeterminate request snapshot: %v", err)
	}
	if snapshot["_provider_execution"] != true ||
		strings.TrimSpace(fmt.Sprint(snapshot[model.MediaJobProviderSubmissionIndeterminateSnapshotKey])) == "" {
		t.Fatalf("indeterminate submission state was not persisted: %+v", snapshot)
	}
	encrypted, ok := snapshot[model.MediaJobProviderCredentialSnapshotKey].(string)
	if !ok || !model.IsEncryptedChannelKey(encrypted) {
		t.Fatalf("single-key provider credential was not pinned: %+v", snapshot)
	}
}

func TestAcceptedVideoSubmissionPersistenceRetriesTransientFailures(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:          token.UserId,
		TokenID:         token.Id,
		ModelName:       "sora-retry-persist",
		Kind:            model.MediaJobKindVideo,
		RequestSnapshot: model.JSONValue(`{"_provider_execution":false}`),
		DeferProcessing: true,
	})
	if err != nil {
		t.Fatalf("create durable video job: %v", err)
	}

	attempts := 0
	const callbackName = "test:transient_video_submission_persistence"
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil &&
			tx.Statement.Schema.Name == "MediaJob" &&
			attempts < 2 {
			attempts++
			_ = tx.AddError(errors.New("transient database error"))
		}
	}); err != nil {
		t.Fatalf("register transient update callback: %v", err)
	}
	defer db.Callback().Update().Remove(callbackName)

	updated, err := updateMediaJobSubmissionWithRetry(job.ID, token.UserId, model.UpdateMediaJobSubmissionInput{
		Provider:      "openai",
		UpstreamJobID: "video_retry_accepted",
		Ready:         true,
		Now:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("accepted submission should survive transient persistence failures: %v", err)
	}
	if attempts != 2 || updated.UpstreamJobID != "video_retry_accepted" {
		t.Fatalf("unexpected accepted submission retry result: attempts=%d job=%+v", attempts, updated)
	}
}

func TestAcceptedVideoSubmissionKeepsReconciliationMarkerWhenFinalPersistenceFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"video_persist_lost","status":"queued"}`))
	}))
	defer provider.Close()

	seedVideoProviderRoute(t, 41, "OpenAI", "sora-persist-fallback", 6320)
	if err := db.Model(&model.Channel{}).Where("id = ?", 6320).Update("base_url", provider.URL).Error; err != nil {
		t.Fatalf("update provider base url: %v", err)
	}
	requestUSD := 0.01
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      "sora-persist-fallback",
		ChannelGroup:   model.ChannelLaneDirect,
		RequestUSD:     &requestUSD,
		Active:         true,
		PricingVersion: 1,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("seed video sell price: %v", err)
	}
	model.RefreshPricing()

	const callbackName = "test:fail_final_video_submission_persistence"
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if ok && updates["upstream_job_id"] == "video_persist_lost" {
			_ = tx.AddError(errors.New("forced final persistence failure"))
		}
	}); err != nil {
		t.Fatalf("register final persistence failure: %v", err)
	}
	defer db.Callback().Update().Remove(callbackName)

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-video-persist-fallback")
		c.Next()
	})
	engine.POST("/v1/videos", middleware.TokenAuth(), middleware.SuppressRelayTelemetry(), middleware.Distribute(), SubmitVideoJob)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewReader([]byte(`{"model":"sora-persist-fallback","prompt":"preserve accepted work","seconds":4}`)),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token.Key)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected persistence error, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	var job model.MediaJob
	if err := db.Where("request_id = ?", "req-video-persist-fallback").First(&job).Error; err != nil {
		t.Fatalf("load recoverable video job: %v", err)
	}
	if job.Status != model.MediaJobStatusRunning ||
		job.UpstreamJobID != "" ||
		job.ChannelID == nil ||
		*job.ChannelID != 6320 {
		t.Fatalf("accepted provider job lost recoverable state: %+v", job)
	}
	var snapshot map[string]any
	if err := common.Unmarshal(job.RequestSnapshot, &snapshot); err != nil {
		t.Fatalf("decode recoverable request snapshot: %v", err)
	}
	if snapshot["_provider_execution"] != true ||
		snapshot[model.MediaJobProviderSubmissionIndeterminateSnapshotKey] != videoSubmissionPersistencePending {
		t.Fatalf("accepted provider job lost reconciliation marker: %+v", snapshot)
	}
}

func TestValidateEphemeralVideoJobAccessUsesStoredJobSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/mjob_test", nil)
	context.Request.Header.Set("X-Session-Id", "session-allowed")
	credential := &model.EphemeralCredential{
		Scope:     model.EphemeralCredentialScopeRelay,
		SessionID: "session-allowed",
		Models:    model.StringSlice{"sora-2"},
		Providers: model.StringSlice{"openai"},
	}
	job := &model.MediaJob{
		ModelName: "sora-2",
		Provider:  "openai",
		SessionID: "session-other",
	}
	if err := validateEphemeralVideoJobAccess(context, credential, job); err == nil {
		t.Fatal("request header must not replace the stored media job session")
	}
	job.SessionID = "session-allowed"
	if err := validateEphemeralVideoJobAccess(context, credential, job); err != nil {
		t.Fatalf("matching stored media job session should pass: %v", err)
	}
}

func TestMediaJobSnapshotMarksProviderExecution(t *testing.T) {
	snapshot := mediaJobSnapshotWithProviderExecution(model.JSONValue(`{"final_group":"cheap"}`), true)
	var fields map[string]any
	if err := common.Unmarshal(snapshot, &fields); err != nil {
		t.Fatalf("decode provider execution snapshot: %v", err)
	}
	if fields["_provider_execution"] != true || fields["final_group"] != "cheap" {
		t.Fatalf("provider execution snapshot lost routing context: %+v", fields)
	}
	sanitized := mediaJobSnapshotWithProviderExecution(model.JSONValue(`{"_provider_execution":true}`), false)
	fields = map[string]any{}
	if err := common.Unmarshal(sanitized, &fields); err != nil {
		t.Fatalf("decode sanitized snapshot: %v", err)
	}
	if _, exists := fields["_provider_execution"]; exists {
		t.Fatalf("artifact-only clients must not opt into provider completion: %+v", fields)
	}
}

func TestMediaJobSnapshotStoresEncryptedProviderCredential(t *testing.T) {
	previousCryptoSecret := common.CryptoSecret
	previousSessionSecret := common.SessionSecret
	common.CryptoSecret = "video-job-credential-secret" // secret-scan: allow -- test fixture
	common.SessionSecret = "different-session-secret"   // secret-scan: allow -- test fixture
	defer func() {
		common.CryptoSecret = previousCryptoSecret
		common.SessionSecret = previousSessionSecret
	}()

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(context, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(context, constant.ContextKeyChannelKey, "selected-video-key")
	snapshot, err := mediaJobSnapshotWithProviderCredential(
		model.JSONValue(`{"channel_multi_key_index":1,"prompt":"test"}`),
		context,
	)
	if err != nil {
		t.Fatalf("mediaJobSnapshotWithProviderCredential error: %v", err)
	}
	if strings.Contains(string(snapshot), "selected-video-key") {
		t.Fatalf("provider credential must not be stored in plaintext: %s", snapshot)
	}
	var fields map[string]any
	if err := common.Unmarshal(snapshot, &fields); err != nil {
		t.Fatalf("decode provider credential snapshot: %v", err)
	}
	if _, exists := fields["channel_multi_key_index"]; exists {
		t.Fatalf("mutable key index must not be stored for new video jobs: %+v", fields)
	}
	encrypted, ok := fields[model.MediaJobProviderCredentialSnapshotKey].(string)
	if !ok || !model.IsEncryptedChannelKey(encrypted) {
		t.Fatalf("expected encrypted provider credential envelope: %+v", fields)
	}
	decrypted, err := model.DecryptMediaJobProviderCredential(encrypted)
	if err != nil || decrypted != "selected-video-key" {
		t.Fatalf("decrypt stored provider credential: value=%q err=%v", decrypted, err)
	}
}

func TestMediaJobSnapshotStoresSingleKeyProviderCredential(t *testing.T) {
	previousCryptoSecret := common.CryptoSecret
	previousSessionSecret := common.SessionSecret
	common.CryptoSecret = "video-job-single-credential-secret" // secret-scan: allow -- test fixture
	common.SessionSecret = "different-session-secret"          // secret-scan: allow -- test fixture
	defer func() {
		common.CryptoSecret = previousCryptoSecret
		common.SessionSecret = previousSessionSecret
	}()

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(context, constant.ContextKeyChannelIsMultiKey, false)
	common.SetContextKey(context, constant.ContextKeyChannelKey, "single-video-key")
	snapshot, err := mediaJobSnapshotWithProviderCredential(
		model.JSONValue(`{"prompt":"test"}`),
		context,
	)
	if err != nil {
		t.Fatalf("mediaJobSnapshotWithProviderCredential error: %v", err)
	}
	if strings.Contains(string(snapshot), "single-video-key") {
		t.Fatalf("single provider credential must not be stored in plaintext: %s", snapshot)
	}
	var fields map[string]any
	if err := common.Unmarshal(snapshot, &fields); err != nil {
		t.Fatalf("decode provider credential snapshot: %v", err)
	}
	encrypted, ok := fields[model.MediaJobProviderCredentialSnapshotKey].(string)
	if !ok || !model.IsEncryptedChannelKey(encrypted) {
		t.Fatalf("expected encrypted single-key credential envelope: %+v", fields)
	}
	decrypted, err := model.DecryptMediaJobProviderCredential(encrypted)
	if err != nil || decrypted != "single-video-key" {
		t.Fatalf("decrypt stored single-key credential: value=%q err=%v", decrypted, err)
	}
}

func TestSubmitVideoJobChecksQuotaBeforeProviderCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	providerCalls := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		providerCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"should_not_be_created"}`))
	}))
	defer provider.Close()

	seedVideoProviderRoute(t, 1, "OpenAI", "sora-quota-check", 6302)
	if err := db.Model(&model.Channel{}).Where("id = ?", 6302).Update("base_url", provider.URL).Error; err != nil {
		t.Fatalf("update video provider base url: %v", err)
	}
	secondUSD := 0.01
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      "sora-quota-check",
		ChannelGroup:   model.ChannelLaneDirect,
		SecondUSD:      &secondUSD,
		Active:         true,
		PricingVersion: 1,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("seed video sell price: %v", err)
	}
	if err := db.Model(&model.User{}).Where("id = ?", token.UserId).Update("quota", 1).Error; err != nil {
		t.Fatalf("lower user quota: %v", err)
	}
	if err := db.Model(&model.Token{}).Where("id = ?", token.Id).Update("remain_quota", 1).Error; err != nil {
		t.Fatalf("lower token quota: %v", err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-video-quota-check")
		c.Next()
	})
	engine.POST("/v1/videos", middleware.TokenAuth(), middleware.SuppressRelayTelemetry(), middleware.Distribute(), SubmitVideoJob)

	body := []byte(`{"model":"sora-quota-check","prompt":"A paid video","seconds":4}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token.Key)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if providerCalls != 0 {
		t.Fatalf("provider should not be called when quota is insufficient, got %d calls", providerCalls)
	}
}

func TestSubmitVideoJobRetriesAnotherEligibleChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	token, _ := seedMediaJobControllerAuth(t, db)
	firstCalls := 0
	firstProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		firstCalls++
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer firstProvider.Close()
	secondCalls := 0
	secondProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		secondCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"video_retry_success"}`))
	}))
	defer secondProvider.Close()

	seedVideoProviderRoute(t, 1, "OpenAI", "sora-retry", 6401)
	priorityHigh := int64(10)
	autoBanOff := 0
	if err := db.Model(&model.Channel{}).Where("id = ?", 6401).Updates(map[string]any{
		"base_url":       firstProvider.URL,
		"priority":       priorityHigh,
		"auto_ban":       autoBanOff,
		"param_override": `{"seconds":5}`,
	}).Error; err != nil {
		t.Fatalf("configure first retry channel: %v", err)
	}
	if err := db.Model(&model.Ability{}).Where("channel_id = ?", 6401).Update("priority", priorityHigh).Error; err != nil {
		t.Fatalf("configure first retry ability: %v", err)
	}
	priorityLow := int64(0)
	secondParamOverride := `{"seconds":8}`
	secondChannel := &model.Channel{
		Id:            6402,
		Type:          constant.ChannelTypeOpenAI,
		Key:           "second-upstream-key",
		Status:        common.ChannelStatusEnabled,
		Name:          "OpenAI retry fallback",
		Models:        "sora-retry",
		Group:         model.ChannelLaneDirect,
		BaseURL:       &secondProvider.URL,
		Priority:      &priorityLow,
		AutoBan:       &autoBanOff,
		ParamOverride: &secondParamOverride,
	}
	if err := db.Create(secondChannel).Error; err != nil {
		t.Fatalf("create second retry channel: %v", err)
	}
	if err := db.Create(&model.Ability{
		Group:     model.ChannelLaneDirect,
		Model:     "sora-retry",
		ChannelId: 6402,
		Enabled:   true,
		Priority:  &priorityLow,
	}).Error; err != nil {
		t.Fatalf("create second retry ability: %v", err)
	}
	secondUSD := 0.01
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      "sora-retry",
		ChannelGroup:   model.ChannelLaneDirect,
		SecondUSD:      &secondUSD,
		Active:         true,
		PricingVersion: 1,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("seed retry video sell price: %v", err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set(common.RequestIdKey, "req-video-retry")
		c.Next()
	})
	engine.POST("/v1/videos", middleware.TokenAuth(), middleware.SuppressRelayTelemetry(), middleware.Distribute(), SubmitVideoJob)

	body := []byte(`{"model":"sora-retry","prompt":"A resilient video","seconds":4}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token.Key)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected retry to succeed with 202, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("expected one call per channel, got first=%d second=%d", firstCalls, secondCalls)
	}
	var job model.MediaJob
	if err := db.Where("request_id = ?", "req-video-retry").First(&job).Error; err != nil {
		t.Fatalf("load retried media job: %v", err)
	}
	if job.UpstreamJobID != "video_retry_success" || job.ChannelID == nil || *job.ChannelID != 6402 {
		t.Fatalf("retry did not persist winning channel: %+v", job)
	}
	if reserved := model.MediaJobBillingReservationAmount(job); reserved != 80_000 {
		t.Fatalf("retry kept the failed channel's billing parameters: %d", reserved)
	}
	var attempt model.RoutingAttemptOutcome
	if err := db.Where("request_id = ?", "req-video-retry").First(&attempt).Error; err != nil {
		t.Fatalf("load video routing attempt: %v", err)
	}
	if attempt.ChannelID != 6401 || attempt.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("failed primary video attempt was not preserved: %+v", attempt)
	}
}

func TestVideoChannelTestReturnsUnsupportedWithoutSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupMediaJobControllerTestDB(t)
	_, _ = seedMediaJobControllerAuth(t, db)
	channel := &model.Channel{
		Id:     6201,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "invalid-video-key",
		Status: common.ChannelStatusEnabled,
		Name:   "video test channel",
		Models: "sora-2",
		Group:  model.ChannelLaneDirect,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("failed to create channel: %v", err)
	}

	result := testChannel(channel, "sora-2", string(constant.EndpointTypeVideoGeneration))
	if result.localErr == nil {
		t.Fatal("expected video channel test to return an unsupported local error")
	}
	if !strings.Contains(result.localErr.Error(), "unsupported") {
		t.Fatalf("expected unsupported error, got %v", result.localErr)
	}
	if result.lazuErr != nil {
		t.Fatalf("expected unsupported video test not to be treated as an upstream channel error, got %v", result.lazuErr)
	}
}
