package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupMediaJobModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB := coreDB
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&User{}, &Token{}, &File{}, &MediaJob{}); err != nil {
		t.Fatalf("failed to migrate media job tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = originalDB
	})
	return db
}

func seedMediaJobUserAndFile(t *testing.T, db *gorm.DB) *File {
	t.Helper()
	user := &User{
		Id:          1,
		Username:    "media-job-user",
		DisplayName: "media-job-user",
		Status:      common.UserStatusEnabled,
		Quota:       billing.MicroUSD(1_000_000),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	token := &Token{
		Id:          2,
		UserId:      user.Id,
		Key:         "mediajobtoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: billing.MicroUSD(1_000_000),
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	tokenID := token.Id
	file := &File{
		ID:         "file-lazu-media-job-artifact",
		UserID:     user.Id,
		TokenID:    &tokenID,
		Filename:   "artifact.png",
		Purpose:    "vision",
		MimeType:   "image/png",
		Bytes:      128,
		StorageKey: "test/artifact.png",
		Status:     FileStatusProcessed,
		CreatedAt:  time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC).Unix(),
	}
	if err := db.Create(file).Error; err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	return file
}

func TestMediaJobClaimAndCompleteWithArtifactFile(t *testing.T) {
	db := setupMediaJobModelTestDB(t)
	file := seedMediaJobUserAndFile(t, db)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	job, err := CreateMediaJob(CreateMediaJobInput{
		UserID:          1,
		TokenID:         2,
		ModelName:       "gpt-image-1",
		Kind:            MediaJobKindImage,
		RequestSnapshot: JSONValue([]byte(`{"prompt":"draw"}`)),
		ArtifactFileID:  file.ID,
		SessionID:       "sess-media",
		AgentID:         "codex",
		Now:             now,
	})
	if err != nil {
		t.Fatalf("CreateMediaJob error: %v", err)
	}
	if !strings.HasPrefix(job.PublicID, "mjob_") || job.Status != MediaJobStatusPending {
		t.Fatalf("unexpected created job: %+v", job)
	}
	claimed, err := ClaimRunnableMediaJobs(now.Add(time.Second), 10, time.Minute)
	if err != nil {
		t.Fatalf("ClaimRunnableMediaJobs error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Status != MediaJobStatusRunning || claimed[0].StartedAt == 0 {
		t.Fatalf("unexpected claimed jobs: %+v", claimed)
	}
	if err := MarkMediaJobSucceeded(claimed[0].ID, CompleteMediaJobInput{
		ArtifactFileID: file.ID,
		CostSnapshot:   JSONValue([]byte(`{"charged_microusd":0}`)),
		Now:            now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("MarkMediaJobSucceeded error: %v", err)
	}
	loaded, err := GetMediaJobByPublicIDAndUser(job.PublicID, 1)
	if err != nil {
		t.Fatalf("failed to reload job: %v", err)
	}
	if loaded.Status != MediaJobStatusSucceeded || loaded.ArtifactFileID != file.ID || loaded.CompletedAt == 0 {
		t.Fatalf("unexpected completed job: %+v", loaded)
	}
	dto := loaded.ToDTO(true)
	if dto.Artifact == nil || dto.Artifact.FileID != file.ID || len(dto.Request) == 0 {
		t.Fatalf("unexpected dto: %+v", dto)
	}
}

func TestMarkMediaJobProviderFailedReplacesAcceptedRoutingOutcome(t *testing.T) {
	db := setupMediaJobModelTestDB(t)
	if err := db.AutoMigrate(&RoutingDecision{}, &RoutingOutcome{}); err != nil {
		t.Fatalf("migrate routing evidence: %v", err)
	}
	now := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	channelID := 77
	job, err := CreateMediaJob(CreateMediaJobInput{
		UserID: 1, ModelName: "video-terminal", Kind: MediaJobKindVideo,
		ChannelID: &channelID, RequestID: "req-video-terminal", Now: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	if err := db.Create(&RoutingDecision{
		ID: 1, RequestID: "req-video-terminal", DecisionAt: now.Add(-time.Minute),
		RequestedModel: "video-terminal", ResolvedModel: "video-terminal",
		StrategyName: "test", IsPrimary: true,
	}).Error; err != nil {
		t.Fatalf("seed routing decision: %v", err)
	}
	accepted := 202
	if err := db.Create(&RoutingOutcome{
		RequestID: "req-video-terminal", DecisionID: 1, ChannelID: &channelID,
		FinalGroup: ChannelLaneDirect, StatusCode: &accepted, OutcomeAt: now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed accepted routing outcome: %v", err)
	}
	if err := MarkMediaJobProviderFailed(job.ID, "video_provider_failed", "generation failed", now); err != nil {
		t.Fatalf("mark provider failure: %v", err)
	}
	var outcome RoutingOutcome
	if err := db.First(&outcome, "request_id = ?", "req-video-terminal").Error; err != nil {
		t.Fatalf("reload routing outcome: %v", err)
	}
	if outcome.StatusCode == nil || *outcome.StatusCode != 502 ||
		outcome.ErrorCode != "video_provider_failed" || outcome.ErrorType != "upstream_error" ||
		!outcome.OutcomeAt.Equal(now) {
		t.Fatalf("terminal provider verdict not reflected in outcome: %+v", outcome)
	}
	traffic, err := QueryChannelHealthEvidence(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("query channel health after terminal failure: %v", err)
	}
	if len(traffic) != 1 || traffic[0].RequestCount != 1 || traffic[0].ErrorCount != 1 {
		t.Fatalf("terminal media failure missing from channel health: %+v", traffic)
	}
}

func TestMarkMediaJobProviderContentRejectionRemovesAcceptedHealthSuccess(t *testing.T) {
	db := setupMediaJobModelTestDB(t)
	if err := db.AutoMigrate(&RoutingOutcome{}); err != nil {
		t.Fatalf("migrate routing outcome: %v", err)
	}
	now := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	job, err := CreateMediaJob(CreateMediaJobInput{
		UserID: 1, ModelName: "video-moderated", Kind: MediaJobKindVideo,
		RequestID: "req-video-moderated", Now: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	accepted := 202
	if err := db.Create(&RoutingOutcome{
		RequestID: "req-video-moderated", DecisionID: 2,
		FinalGroup: ChannelLaneDirect, StatusCode: &accepted, OutcomeAt: now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed accepted routing outcome: %v", err)
	}
	if err := MarkMediaJobProviderFailed(job.ID, "content_filter", "prompt rejected", now); err != nil {
		t.Fatalf("mark content rejection: %v", err)
	}
	var outcome RoutingOutcome
	if err := db.First(&outcome, "request_id = ?", "req-video-moderated").Error; err != nil {
		t.Fatalf("reload routing outcome: %v", err)
	}
	if outcome.StatusCode == nil || *outcome.StatusCode != 400 || outcome.ErrorType != "invalid_request_error" {
		t.Fatalf("content rejection must leave health population: %+v", outcome)
	}
}

func TestMarkMediaJobProviderFailedUpsertsMissingRoutingOutcome(t *testing.T) {
	db := setupMediaJobModelTestDB(t)
	if err := db.AutoMigrate(&RoutingDecision{}, &RoutingOutcome{}); err != nil {
		t.Fatalf("migrate routing evidence: %v", err)
	}
	now := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	channelID := 88
	job, err := CreateMediaJob(CreateMediaJobInput{
		UserID: 1, ModelName: "video-fast-terminal", Kind: MediaJobKindVideo,
		ChannelID: &channelID, RequestID: "req-video-fast-terminal",
		RequestSnapshot: JSONValue(`{"final_group":"cheap"}`), Now: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	if err := db.Create(&RoutingDecision{
		ID: 3, RequestID: "req-video-fast-terminal", DecisionAt: now.Add(-time.Minute),
		RequestedModel: "video-fast-terminal", ResolvedModel: "video-fast-terminal",
		StrategyName: "test", IsPrimary: true,
	}).Error; err != nil {
		t.Fatalf("seed routing decision: %v", err)
	}

	if err := MarkMediaJobProviderFailed(job.ID, "provider_failed", "fast failure", now); err != nil {
		t.Fatalf("mark provider failure: %v", err)
	}
	var outcome RoutingOutcome
	if err := db.First(&outcome, "request_id = ?", "req-video-fast-terminal").Error; err != nil {
		t.Fatalf("load upserted outcome: %v", err)
	}
	if outcome.StatusCode == nil || *outcome.StatusCode != http.StatusBadGateway ||
		outcome.DecisionID != 3 || outcome.ChannelID == nil || *outcome.ChannelID != channelID ||
		outcome.FinalGroup != ChannelLaneAgent {
		t.Fatalf("upserted terminal outcome = %+v", outcome)
	}
}

func TestMediaJobDTOHidesInternalRequestSnapshotFields(t *testing.T) {
	job := MediaJob{
		RequestSnapshot: JSONValue([]byte(`{
			"prompt":"public prompt",
			"channel_multi_key_index":2,
			"_provider_credential":"encrypted-secret",
			"_billing_reservation_id":"reservation-secret",
			"_billing_reserved_quota_microusd":42,
			"_provider_execution":true
		}`)),
	}
	out := job.ToDTO(true)
	var request map[string]any
	if err := json.Unmarshal(out.Request, &request); err != nil {
		t.Fatalf("decode public request: %v", err)
	}
	if request["prompt"] != "public prompt" {
		t.Fatalf("public request field missing: %+v", request)
	}
	for _, key := range []string{
		"channel_multi_key_index",
		"_provider_credential",
		"_billing_reservation_id",
		"_billing_reserved_quota_microusd",
		"_provider_execution",
	} {
		if _, exists := request[key]; exists {
			t.Fatalf("internal request field %q leaked: %+v", key, request)
		}
	}
}

func TestFailedMediaJobDTOHidesArtifactURL(t *testing.T) {
	job := MediaJob{
		PublicID:    "mjob_failed_video",
		Status:      MediaJobStatusFailed,
		ArtifactURL: "https://provider.example.com/signed-video.mp4",
		ErrorCode:   "artifact_too_large",
	}
	out := job.ToDTO(false)
	if out.Artifact != nil {
		t.Fatalf("failed media job leaked provider artifact: %+v", out.Artifact)
	}
	if out.Error == nil || out.Error.Code != "artifact_too_large" {
		t.Fatalf("failed media job error missing from dto: %+v", out.Error)
	}
}

func TestMediaJobClaimRecoversStaleRunningJob(t *testing.T) {
	db := setupMediaJobModelTestDB(t)
	seedMediaJobUserAndFile(t, db)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	running := &MediaJob{
		PublicID:        "mjob_stale",
		UserID:          1,
		TokenID:         intPtr(2),
		ModelName:       "veo-3",
		Kind:            MediaJobKindVideo,
		Status:          MediaJobStatusRunning,
		RequestSnapshot: JSONValue([]byte(`{"prompt":"video"}`)),
		LockedAt:        now.Add(-10 * time.Minute).Unix(),
		StartedAt:       now.Add(-10 * time.Minute).Unix(),
		NextPollAt:      now.Add(-10 * time.Minute).Unix(),
	}
	if err := db.Create(running).Error; err != nil {
		t.Fatalf("failed to create stale running job: %v", err)
	}
	claimed, err := ClaimRunnableMediaJobs(now, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimRunnableMediaJobs error: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("expected stale running job to be claimed, got %+v", claimed)
	}
	if claimed[0].PublicID != "mjob_stale" || claimed[0].Attempts != 1 || claimed[0].LockedAt != now.Unix() {
		t.Fatalf("unexpected claimed stale job: %+v", claimed[0])
	}
}

func TestRescheduleMediaJobReleasesLeaseAndPreservesRunningState(t *testing.T) {
	db := setupMediaJobModelTestDB(t)
	seedMediaJobUserAndFile(t, db)
	now := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	job, err := CreateMediaJob(CreateMediaJobInput{
		UserID:    1,
		ModelName: "veo-3-fast",
		Kind:      MediaJobKindVideo,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("CreateMediaJob error: %v", err)
	}
	claimed, err := ClaimRunnableMediaJobs(now.Add(time.Second), 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimRunnableMediaJobs error=%v jobs=%+v", err, claimed)
	}
	nextPoll := now.Add(15 * time.Second)
	if err := RescheduleMediaJob(job.ID, nextPoll); err != nil {
		t.Fatalf("RescheduleMediaJob error: %v", err)
	}
	var stored MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("reload media job: %v", err)
	}
	if stored.Status != MediaJobStatusRunning || stored.LockedAt != 0 || stored.NextPollAt != nextPoll.Unix() {
		t.Fatalf("unexpected rescheduled job: %+v", stored)
	}
}

func intPtr(value int) *int {
	return &value
}

func TestMediaJobCustomerCostProjection(t *testing.T) {
	job := MediaJob{ProjectID: 7, Provider: "private-provider", CostSnapshot: JSONValue(`{"pricing_version":3,"customer_charge_microusd":123,"line_items":[{"dimension":"second","amount_microusd":123}],"provider_cost_microusd":20,"provider_line_items":[{"secret":"cost"}],"gross_margin_usd":1,"usage_fact":{"channel_id":77,"provider_family":"private"},"new_internal_field":"secret"}`)}
	for _, detail := range []bool{false, true} {
		out := job.ToDTO(detail)
		text := string(out.CostSnapshot)
		for _, secret := range []string{"provider", "gross_margin", "usage_fact", "channel", "new_internal_field"} {
			if strings.Contains(text, secret) {
				t.Fatalf("leaked %s: %s", secret, text)
			}
		}
		if out.Provider != "" || !strings.Contains(text, "customer_charge_microusd") || !strings.Contains(text, "line_items") {
			t.Fatalf("bad customer projection: %+v", out)
		}
	}
	if !strings.Contains(string(job.CostSnapshot), "provider_cost") {
		t.Fatal("stored cost changed")
	}
	job.CostSnapshot = JSONValue(`not-json`)
	if len(job.ToDTO(false).CostSnapshot) != 0 {
		t.Fatal("malformed snapshot leaked")
	}
}

// setupMediaJobVerdictTestDB adds what a terminal verdict writes to: the
// routing outcome and the route breaker.
func setupMediaJobVerdictTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupMediaJobModelTestDB(t)
	if err := db.AutoMigrate(&RoutingDecision{}, &RoutingOutcome{}, &RouteHealthState{}); err != nil {
		t.Fatalf("migrate verdict tables: %v", err)
	}
	resetRouteHealthForTest := func() {
		routeHealthCache.Lock()
		routeHealthCache.routes = map[string]routeHealthEntry{}
		routeHealthCache.Unlock()
		routeHealthTableCache.Lock()
		routeHealthTableCache.checked = false
		routeHealthTableCache.db = nil
		routeHealthTableCache.Unlock()
	}
	resetRouteHealthForTest()
	t.Cleanup(resetRouteHealthForTest)
	return db
}

func seedAcceptedVideoJob(t *testing.T, db *gorm.DB, requestID string, channelID int, createdAt time.Time) MediaJob {
	t.Helper()
	job, err := CreateMediaJob(CreateMediaJobInput{
		UserID: 1, ModelName: "seedance-test", Kind: MediaJobKindVideo,
		ChannelID: &channelID, RequestID: requestID,
		RequestSnapshot: JSONValue(`{"final_group":"direct"}`), Now: createdAt,
	})
	if err != nil {
		t.Fatalf("create media job: %v", err)
	}
	if err := db.Model(&MediaJob{}).Where("id = ?", job.ID).Update("created_at", createdAt).Error; err != nil {
		t.Fatalf("pin job creation time: %v", err)
	}
	job.CreatedAt = createdAt
	accepted := http.StatusAccepted
	submitMs := 900
	if err := db.Create(&RoutingOutcome{
		RequestID: requestID, DecisionID: int64(channelID), ChannelID: &channelID,
		FinalGroup: ChannelLaneDirect, StatusCode: &accepted, OutcomeAt: createdAt,
		TotalDurationMs: &submitMs,
	}).Error; err != nil {
		t.Fatalf("seed accepted routing outcome: %v", err)
	}
	return *job
}

func routeFailuresForTest(t *testing.T, db *gorm.DB, channelID int) int {
	t.Helper()
	var row RouteHealthState
	err := db.Where("model_name = ? AND lane = ? AND channel_id = ?", "seedance-test", ChannelLaneDirect, channelID).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return 0
	}
	if err != nil {
		t.Fatalf("load route health: %v", err)
	}
	return row.ConsecutiveFailures
}

// A delivered video is the only evidence that the route can make one. It
// must replace the submission's 202 with the full wait and end the failure
// streak; before, nothing a successful job did could ever reset the breaker.
func TestRecordMediaJobProviderSuccessRewritesOutcomeAndResetsBreaker(t *testing.T) {
	db := setupMediaJobVerdictTestDB(t)
	created := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	channelID := 41
	for i := 0; i < 2; i++ {
		if err := RecordRouteHealthEvidence("seedance-test", ChannelLaneDirect, channelID, false, "upstream_error", created.Add(-time.Hour+time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("seed breaker failure: %v", err)
		}
	}
	job := seedAcceptedVideoJob(t, db, "req-video-delivered", channelID, created)

	delivered := created.Add(3 * time.Minute)
	if err := RecordMediaJobProviderSuccess(job, delivered); err != nil {
		t.Fatalf("record delivered outcome: %v", err)
	}
	var outcome RoutingOutcome
	if err := db.First(&outcome, "request_id = ?", "req-video-delivered").Error; err != nil {
		t.Fatalf("reload outcome: %v", err)
	}
	if outcome.StatusCode == nil || *outcome.StatusCode != http.StatusOK ||
		outcome.TotalDurationMs == nil || *outcome.TotalDurationMs != int((3*time.Minute).Milliseconds()) ||
		outcome.HealthFailed == nil || *outcome.HealthFailed || !outcome.OutcomeAt.Equal(delivered) {
		t.Fatalf("delivered outcome = %+v", outcome)
	}
	if got := routeFailuresForTest(t, db, channelID); got != 0 {
		t.Fatalf("breaker failures after delivery = %d, want 0", got)
	}
}

// Ark files a moderated prompt or clip as a failed task. That is the caller's
// content, not the route: it must leave the health population and the
// breaker alone. An upstream failure must count in both.
func TestMarkMediaJobProviderFailedFeedsBreakerByFailureClass(t *testing.T) {
	db := setupMediaJobVerdictTestDB(t)
	created := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	channelID := 42

	moderated := seedAcceptedVideoJob(t, db, "req-video-sensitive", channelID, created)
	if err := MarkMediaJobProviderFailed(moderated.ID, "OutputVideoSensitiveContentDetected", "flagged", created.Add(time.Minute)); err != nil {
		t.Fatalf("mark moderated failure: %v", err)
	}
	var outcome RoutingOutcome
	if err := db.First(&outcome, "request_id = ?", "req-video-sensitive").Error; err != nil {
		t.Fatalf("reload moderated outcome: %v", err)
	}
	if outcome.StatusCode == nil || *outcome.StatusCode != http.StatusBadRequest ||
		outcome.HealthEligible == nil || *outcome.HealthEligible {
		t.Fatalf("moderated outcome must leave the health population: %+v", outcome)
	}
	if got := routeFailuresForTest(t, db, channelID); got != 0 {
		t.Fatalf("moderation counted against the route: failures = %d", got)
	}

	broken := seedAcceptedVideoJob(t, db, "req-video-broken", channelID, created.Add(time.Minute))
	if err := MarkMediaJobProviderFailed(broken.ID, "InternalServiceError", "boom", created.Add(2*time.Minute)); err != nil {
		t.Fatalf("mark upstream failure: %v", err)
	}
	if got := routeFailuresForTest(t, db, channelID); got != 1 {
		t.Fatalf("upstream failure not counted: failures = %d, want 1", got)
	}

	limited := seedAcceptedVideoJob(t, db, "req-video-quota", channelID, created.Add(2*time.Minute))
	if err := MarkMediaJobProviderFailed(limited.ID, "QuotaExceeded", "quota", created.Add(3*time.Minute)); err != nil {
		t.Fatalf("mark quota failure: %v", err)
	}
	var quota RoutingOutcome
	if err := db.First(&quota, "request_id = ?", "req-video-quota").Error; err != nil {
		t.Fatalf("reload quota outcome: %v", err)
	}
	if quota.StatusCode == nil || *quota.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("quota outcome = %+v", quota)
	}
	if got := routeFailuresForTest(t, db, channelID); got != 2 {
		t.Fatalf("quota failure not counted: failures = %d, want 2", got)
	}
}
