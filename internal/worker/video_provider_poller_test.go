package worker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func TestPermanentVideoProviderPollStatuses(t *testing.T) {
	for _, statusCode := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		if !isPermanentVideoProviderPollStatus(statusCode) {
			t.Fatalf("expected status %d to fail without retry", statusCode)
		}
	}
	for _, statusCode := range []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if isPermanentVideoProviderPollStatus(statusCode) {
			t.Fatalf("expected status %d to remain retryable", statusCode)
		}
	}
}

func TestPermanentVideoArtifactDownloadStatuses(t *testing.T) {
	for _, statusCode := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusGone,
	} {
		if !isPermanentVideoArtifactDownloadStatus(statusCode) {
			t.Fatalf("expected artifact status %d to be permanent", statusCode)
		}
	}
	for _, statusCode := range []int{
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		if isPermanentVideoArtifactDownloadStatus(statusCode) {
			t.Fatalf("expected artifact status %d to remain retryable", statusCode)
		}
	}
}

func TestPermanentVideoProviderPollErrorFailsJobImmediately(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	seedWorkerMediaJobUserAndFile(t, db)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"message":"invalid provider key"}}`))
	}))
	defer provider.Close()

	channel := &model.Channel{
		Id:      402,
		Name:    "rejected video provider",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "invalid-key",
		BaseURL: &provider.URL,
		Status:  common.ChannelStatusEnabled,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("seed provider channel: %v", err)
	}
	channelID := channel.Id
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:        1,
		TokenID:       2,
		ModelName:     "sora-rejected",
		Kind:          model.MediaJobKindVideo,
		ChannelID:     &channelID,
		UpstreamJobID: "video-rejected",
		RequestID:     "req-video-rejected",
	})
	if err != nil {
		t.Fatalf("create video job: %v", err)
	}
	accepted := http.StatusAccepted
	if err := db.Create(&model.RoutingOutcome{
		RequestID: "req-video-rejected", DecisionID: 402, ChannelID: &channelID,
		FinalGroup: model.ChannelLaneDirect, StatusCode: &accepted,
		OutcomeAt: time.Now().UTC().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed accepted routing outcome: %v", err)
	}

	now := time.Now().UTC()
	if err := processUpstreamVideoJob(context.Background(), *job, now); err != nil {
		t.Fatalf("process permanent provider rejection: %v", err)
	}
	var failed model.MediaJob
	if err := db.First(&failed, job.ID).Error; err != nil {
		t.Fatalf("reload failed video job: %v", err)
	}
	if failed.Status != model.MediaJobStatusFailed ||
		failed.ErrorCode != "video_provider_poll_rejected" ||
		failed.NextPollAt != 0 {
		t.Fatalf("permanent provider rejection was rescheduled: %+v", failed)
	}
	var outcome model.RoutingOutcome
	if err := db.First(&outcome, "request_id = ?", "req-video-rejected").Error; err != nil {
		t.Fatalf("reload terminal routing outcome: %v", err)
	}
	if outcome.StatusCode == nil || *outcome.StatusCode != http.StatusBadGateway ||
		outcome.ErrorCode != "video_provider_poll_rejected" {
		t.Fatalf("provider terminal failure left accepted health evidence: %+v", outcome)
	}
}

func TestIndeterminateVideoSubmissionEventuallyTimesOut(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	seedWorkerMediaJobUserAndFile(t, db)
	snapshot, err := common.Marshal(map[string]any{
		"_provider_execution": true,
		model.MediaJobProviderSubmissionIndeterminateSnapshotKey: "accepted response body was interrupted",
	})
	if err != nil {
		t.Fatalf("marshal indeterminate submission snapshot: %v", err)
	}
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:          1,
		TokenID:         2,
		ModelName:       "sora-indeterminate",
		Kind:            model.MediaJobKindVideo,
		RequestSnapshot: model.JSONValue(snapshot),
		DeferProcessing: true,
	})
	if err != nil {
		t.Fatalf("create indeterminate video job: %v", err)
	}
	if err := db.Model(job).Update("attempts", videoProviderMaxAttempts+1).Error; err != nil {
		t.Fatalf("age indeterminate video job: %v", err)
	}
	job.Attempts = videoProviderMaxAttempts + 1
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	if err := processUpstreamVideoJob(context.Background(), *job, now); err != nil {
		t.Fatalf("timeout indeterminate submission: %v", err)
	}
	var stored model.MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("reload indeterminate video job: %v", err)
	}
	if stored.Status != model.MediaJobStatusFailed ||
		stored.ErrorCode != "video_generation_timeout" ||
		stored.CompletedAt != now.Unix() ||
		stored.NextPollAt != 0 {
		t.Fatalf("indeterminate accepted submission did not time out: %+v", stored)
	}
}

func TestAcceptedCustomVideoWaitsForPollingTemplate(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	seedWorkerMediaJobUserAndFile(t, db)
	baseURL := "https://video.example.com/generate"
	channel := &model.Channel{
		Id:      390,
		Name:    "custom video awaiting configuration",
		Type:    constant.ChannelTypeCustom,
		Key:     "provider-secret",
		BaseURL: &baseURL,
		Status:  common.ChannelStatusEnabled,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("seed custom video channel: %v", err)
	}
	channelID := channel.Id
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:          1,
		TokenID:         2,
		ModelName:       "custom-video",
		Kind:            model.MediaJobKindVideo,
		Provider:        "custom",
		ChannelID:       &channelID,
		RequestSnapshot: model.JSONValue(`{"_provider_execution":true}`),
		UpstreamJobID:   "job-42",
		DeferProcessing: true,
	})
	if err != nil {
		t.Fatalf("create accepted custom video job: %v", err)
	}
	now := time.Now().UTC()
	if err := processUpstreamVideoJob(context.Background(), *job, now); err != nil {
		t.Fatalf("missing polling template should remain recoverable: %v", err)
	}
	var stored model.MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("reload accepted custom video job: %v", err)
	}
	if stored.Status != model.MediaJobStatusRunning ||
		stored.CompletedAt != 0 ||
		stored.NextPollAt <= now.Unix() {
		t.Fatalf("accepted custom video was terminalized before configuration: %+v", stored)
	}
}

func TestMediaJobProviderKeyReusesStoredMultiKeyIndex(t *testing.T) {
	channel := &model.Channel{
		Key: "first-key\nsecond-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	job := model.MediaJob{
		RequestSnapshot: model.JSONValue(`{"channel_multi_key_index":1}`),
	}
	key, err := mediaJobProviderKey(job, channel)
	if err != nil {
		t.Fatalf("mediaJobProviderKey error: %v", err)
	}
	if key != "second-key" {
		t.Fatalf("expected stored second key, got %q", key)
	}
}

func TestMediaJobProviderKeyUsesEncryptedCredentialAfterKeyRotation(t *testing.T) {
	previousCryptoSecret := common.CryptoSecret
	previousSessionSecret := common.SessionSecret
	common.CryptoSecret = "video-provider-poller-credential-secret" // secret-scan: allow -- test fixture
	common.SessionSecret = "different-session-secret"               // secret-scan: allow -- test fixture
	defer func() {
		common.CryptoSecret = previousCryptoSecret
		common.SessionSecret = previousSessionSecret
	}()

	encrypted, err := model.EncryptMediaJobProviderCredential("selected-key")
	if err != nil {
		t.Fatalf("EncryptMediaJobProviderCredential error: %v", err)
	}
	snapshot, err := common.Marshal(map[string]any{
		model.MediaJobProviderCredentialSnapshotKey: encrypted,
	})
	if err != nil {
		t.Fatalf("marshal provider credential snapshot: %v", err)
	}
	channel := &model.Channel{
		Key: "new-first-key\nnew-second-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	key, err := mediaJobProviderKey(model.MediaJob{RequestSnapshot: model.JSONValue(snapshot)}, channel)
	if err != nil {
		t.Fatalf("mediaJobProviderKey error: %v", err)
	}
	if key != "selected-key" {
		t.Fatalf("expected immutable selected credential, got %q", key)
	}
}

func TestMediaJobProviderKeyUsesPinnedSingleKeyAfterRotation(t *testing.T) {
	previousCryptoSecret := common.CryptoSecret
	previousSessionSecret := common.SessionSecret
	common.CryptoSecret = "video-provider-single-key-secret" // secret-scan: allow -- test fixture
	common.SessionSecret = "different-session-secret"        // secret-scan: allow -- test fixture
	defer func() {
		common.CryptoSecret = previousCryptoSecret
		common.SessionSecret = previousSessionSecret
	}()

	encrypted, err := model.EncryptMediaJobProviderCredential("submission-key")
	if err != nil {
		t.Fatalf("EncryptMediaJobProviderCredential error: %v", err)
	}
	snapshot, err := common.Marshal(map[string]any{
		model.MediaJobProviderCredentialSnapshotKey: encrypted,
	})
	if err != nil {
		t.Fatalf("marshal provider credential snapshot: %v", err)
	}
	channel := &model.Channel{
		Key: "rotated-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: false,
		},
	}
	key, err := mediaJobProviderKey(model.MediaJob{RequestSnapshot: model.JSONValue(snapshot)}, channel)
	if err != nil {
		t.Fatalf("mediaJobProviderKey error: %v", err)
	}
	if key != "submission-key" {
		t.Fatalf("expected pinned single-key credential, got %q", key)
	}
}

func TestMediaJobProviderKeyRejectsMissingMultiKeyIndex(t *testing.T) {
	channel := &model.Channel{
		Key: "first-key\nsecond-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	if _, err := mediaJobProviderKey(model.MediaJob{RequestSnapshot: model.JSONValue(`{}`)}, channel); err == nil {
		t.Fatal("expected polling to fail instead of switching provider credentials")
	}
}

func TestVideoProviderHTTPClientUsesChannelProxy(t *testing.T) {
	setting := `{"proxy":"http://127.0.0.1:18080"}`
	client, err := videoProviderHTTPClient(&model.Channel{Setting: &setting}, "https://video.example.com/jobs/1", true)
	if err != nil {
		t.Fatalf("videoProviderHTTPClient error: %v", err)
	}
	if client == nil {
		t.Fatal("expected proxy-aware video provider client")
	}
}

func TestVideoProviderHTTPClientRejectsAuthenticatedCrossOriginRedirect(t *testing.T) {
	client, err := videoProviderHTTPClient(&model.Channel{}, "https://video.example.com/content/1", true)
	if err != nil {
		t.Fatalf("videoProviderHTTPClient error: %v", err)
	}
	redirect := httptest.NewRequest(http.MethodGet, "https://cdn.example.net/video.mp4", nil)
	original := httptest.NewRequest(http.MethodGet, "https://video.example.com/content/1", nil)
	if err := client.CheckRedirect(redirect, []*http.Request{original}); err == nil {
		t.Fatal("expected authenticated cross-origin redirect to be rejected")
	}
}

func TestVideoProviderHTTPClientBlocksPrivateExternalArtifactThroughProxy(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	t.Cleanup(func() {
		*fetchSetting = previousFetchSetting
	})

	proxySetting := `{"proxy":"http://127.0.0.1:18080"}`
	_, err := videoProviderHTTPClient(
		&model.Channel{Setting: &proxySetting},
		"http://127.0.0.1:8080/private-video.mp4",
		false,
	)
	if err == nil {
		t.Fatal("expected private cross-origin artifact URL to be blocked before proxying")
	}
}

func TestProviderDirectArtifactURLUsesProviderAwareCompletion(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "sora-direct-artifact")
	fakeStorage := &mediaJobFakeStorage{objects: map[string][]byte{}}
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = previousFetchSetting
	}()

	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/artifact.mp4" {
			t.Fatalf("unexpected artifact path: %s", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer provider-secret" {
			t.Fatalf("provider artifact authorization=%q", got)
		}
		writer.Header().Set("Content-Type", "video/mp4")
		_, _ = writer.Write([]byte("provider-video"))
	}))
	defer provider.Close()

	channel := &model.Channel{
		Id:      401,
		Name:    "direct artifact provider",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "provider-secret",
		BaseURL: &provider.URL,
		Status:  common.ChannelStatusEnabled,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("seed provider channel: %v", err)
	}
	channelID := channel.Id
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:          1,
		TokenID:         2,
		ModelName:       "sora-direct-artifact",
		Kind:            model.MediaJobKindVideo,
		Provider:        "openai",
		ChannelID:       &channelID,
		RequestSnapshot: model.JSONValue(`{"_provider_execution":true}`),
		ArtifactURL:     provider.URL + "/artifact.mp4",
		RequestID:       "req-direct-artifact",
	})
	if err != nil {
		t.Fatalf("create direct artifact job: %v", err)
	}

	if err := ProcessMediaJob(context.Background(), *job); err != nil {
		t.Fatalf("ProcessMediaJob error: %v", err)
	}
	var completed model.MediaJob
	if err := db.First(&completed, job.ID).Error; err != nil {
		t.Fatalf("reload completed job: %v", err)
	}
	if completed.Status != model.MediaJobStatusSucceeded || completed.ArtifactFileID == "" {
		t.Fatalf("provider artifact did not complete through stored file: %+v", completed)
	}
	if completed.ArtifactURL != "" {
		t.Fatalf("persisted provider artifact must replace expiring URL, got %q", completed.ArtifactURL)
	}
	if completed.CompletedAt <= 0 {
		t.Fatalf("completed timestamp missing: %+v", completed)
	}
}

func TestCompletedVideoArtifactDownloadFailureIsRetried(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "sora-download-retry")
	if err := db.Create(&model.SpendBudget{
		Scope:         model.SpendBudgetScopeToken,
		TokenID:       2,
		Period:        model.SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(20_000),
		Enforcement:   model.SpendBudgetEnforcementBlock,
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed retry spend budget: %v", err)
	}
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = previousFetchSetting
	}()

	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	channel := &model.Channel{
		Id:      404,
		Name:    "retry artifact provider",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "provider-secret",
		BaseURL: &provider.URL,
		Status:  common.ChannelStatusEnabled,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("seed provider channel: %v", err)
	}
	channelID := channel.Id
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:          1,
		TokenID:         2,
		ModelName:       "sora-download-retry",
		Kind:            model.MediaJobKindVideo,
		Provider:        "openai",
		ChannelID:       &channelID,
		RequestSnapshot: model.JSONValue(`{"_provider_execution":true}`),
		ArtifactURL:     provider.URL + "/artifact.mp4",
		RequestID:       "req-download-retry",
		DeferProcessing: true,
	})
	if err != nil {
		t.Fatalf("create video job: %v", err)
	}
	if err := ProcessMediaJob(context.Background(), *job); err != nil {
		t.Fatalf("process retryable download failure: %v", err)
	}
	var requeued model.MediaJob
	if err := db.First(&requeued, job.ID).Error; err != nil {
		t.Fatalf("reload video job: %v", err)
	}
	if requeued.Status != model.MediaJobStatusRunning || requeued.NextPollAt <= 0 || requeued.CompletedAt != 0 {
		t.Fatalf("transient download failure was terminalized: %+v", requeued)
	}
	var reservationCount int64
	if err := db.Model(&model.SpendBudgetReservation{}).
		Where("request_id = ?", "req-download-retry").
		Count(&reservationCount).Error; err != nil {
		t.Fatalf("count retained spend reservation: %v", err)
	}
	if reservationCount != 1 {
		t.Fatalf("retryable artifact failure released spend reservation: %d", reservationCount)
	}
}

func TestCompletedVideoArtifactRecoveryHasIndependentAttemptBudget(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "sora-late-completion")
	fakeStorage := &mediaJobFakeStorage{objects: map[string][]byte{}}
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = previousFetchSetting
	}()

	statusCalls := 0
	artifactCalls := 0
	var providerURL string
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/videos/upstream-late":
			statusCalls++
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"status":"completed","url":"` + providerURL + `/artifact.mp4"}`))
		case "/artifact.mp4":
			artifactCalls++
			if artifactCalls == 1 {
				http.Error(writer, "artifact is still propagating", http.StatusServiceUnavailable)
				return
			}
			writer.Header().Set("Content-Type", "video/mp4")
			_, _ = writer.Write([]byte("late-completed-video"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer provider.Close()
	providerURL = provider.URL
	channel := &model.Channel{
		Id:      4050,
		Name:    "late completion provider",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "provider-secret",
		BaseURL: &providerURL,
		Status:  common.ChannelStatusEnabled,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("seed provider channel: %v", err)
	}
	channelID := channel.Id
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:          1,
		TokenID:         2,
		ModelName:       "sora-late-completion",
		Kind:            model.MediaJobKindVideo,
		Provider:        "openai",
		ChannelID:       &channelID,
		RequestSnapshot: model.JSONValue(`{"_provider_execution":true}`),
		UpstreamJobID:   "upstream-late",
		RequestID:       "req-late-completion",
		DeferProcessing: true,
	})
	if err != nil {
		t.Fatalf("create late video job: %v", err)
	}
	if err := db.Model(job).Update("attempts", videoProviderMaxAttempts).Error; err != nil {
		t.Fatalf("age video job: %v", err)
	}
	job.Attempts = videoProviderMaxAttempts
	if err := processUpstreamVideoJob(context.Background(), *job, time.Now().UTC()); err != nil {
		t.Fatalf("first completed-artifact recovery: %v", err)
	}
	var retryable model.MediaJob
	if err := db.First(&retryable, job.ID).Error; err != nil {
		t.Fatalf("reload retryable completed video: %v", err)
	}
	if !mediaJobProviderArtifactRecovery(retryable) ||
		retryable.ArtifactURL != providerURL+"/artifact.mp4" ||
		retryable.Status != model.MediaJobStatusRunning {
		t.Fatalf("provider completion was not durably separated from polling: %+v", retryable)
	}
	if err := db.Model(&retryable).Update("attempts", videoProviderMaxAttempts+1).Error; err != nil {
		t.Fatalf("advance recovery attempt: %v", err)
	}
	retryable.Attempts = videoProviderMaxAttempts + 1
	if err := processUpstreamVideoJob(context.Background(), retryable, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("artifact recovery after generation attempt limit: %v", err)
	}
	var completed model.MediaJob
	if err := db.First(&completed, job.ID).Error; err != nil {
		t.Fatalf("reload completed late video: %v", err)
	}
	if completed.Status != model.MediaJobStatusSucceeded || completed.ArtifactFileID == "" {
		t.Fatalf("late completed artifact was timed out: %+v", completed)
	}
	if statusCalls != 1 || artifactCalls != 2 {
		t.Fatalf("artifact recovery unexpectedly resumed provider polling: status=%d artifact=%d", statusCalls, artifactCalls)
	}
}

func TestCompletedVideoArtifactStorageFailureIsRetried(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "sora-storage-retry")
	fakeStorage := &mediaJobFakeStorage{
		objects: map[string][]byte{},
		putErr:  errors.New("temporary storage outage"),
	}
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = previousFetchSetting
	}()

	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "video/mp4")
		_, _ = writer.Write([]byte("provider-video"))
	}))
	defer provider.Close()
	channel := &model.Channel{
		Id:      405,
		Name:    "retry storage provider",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "provider-secret",
		BaseURL: &provider.URL,
		Status:  common.ChannelStatusEnabled,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("seed provider channel: %v", err)
	}
	channelID := channel.Id
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:          1,
		TokenID:         2,
		ModelName:       "sora-storage-retry",
		Kind:            model.MediaJobKindVideo,
		Provider:        "openai",
		ChannelID:       &channelID,
		RequestSnapshot: model.JSONValue(`{"_provider_execution":true}`),
		ArtifactURL:     provider.URL + "/artifact.mp4",
		RequestID:       "req-storage-retry",
		DeferProcessing: true,
	})
	if err != nil {
		t.Fatalf("create video job: %v", err)
	}
	if err := ProcessMediaJob(context.Background(), *job); err != nil {
		t.Fatalf("process retryable storage failure: %v", err)
	}
	var requeued model.MediaJob
	if err := db.First(&requeued, job.ID).Error; err != nil {
		t.Fatalf("reload video job: %v", err)
	}
	if requeued.Status != model.MediaJobStatusRunning || requeued.NextPollAt <= 0 || requeued.CompletedAt != 0 {
		t.Fatalf("transient storage failure was terminalized: %+v", requeued)
	}
}

func TestCompletedVideoBillingSettlementFailureIsRetried(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "sora-billing-retry")
	if err := db.Create(&model.SpendBudget{
		Scope:         model.SpendBudgetScopeToken,
		TokenID:       2,
		Period:        model.SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(20_000),
		Enforcement:   model.SpendBudgetEnforcementBlock,
		Enabled:       true,
	}).Error; err != nil {
		t.Fatalf("seed billing retry spend budget: %v", err)
	}
	fakeStorage := &mediaJobFakeStorage{objects: map[string][]byte{}}
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = previousFetchSetting
	}()

	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "video/mp4")
		_, _ = writer.Write([]byte("provider-video"))
	}))
	defer provider.Close()
	channel := &model.Channel{
		Id:      406,
		Name:    "billing retry provider",
		Type:    constant.ChannelTypeOpenAI,
		Key:     "provider-secret",
		BaseURL: &provider.URL,
		Status:  common.ChannelStatusEnabled,
	}
	if err := db.Create(channel).Error; err != nil {
		t.Fatalf("seed provider channel: %v", err)
	}
	channelID := channel.Id
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:          1,
		TokenID:         2,
		ModelName:       "sora-billing-retry",
		Kind:            model.MediaJobKindVideo,
		Provider:        "openai",
		ChannelID:       &channelID,
		RequestSnapshot: model.JSONValue(`{"_provider_execution":true}`),
		ArtifactURL:     provider.URL + "/artifact.mp4",
		RequestID:       "req-billing-retry",
		DeferProcessing: true,
	})
	if err != nil {
		t.Fatalf("create video job: %v", err)
	}

	const callbackName = "test:fail_video_billing_ledger_once"
	failedOnce := false
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		ledger, ok := tx.Statement.Dest.(*model.WalletLedger)
		if ok && ledger.RefType == model.MediaJobRefType && !failedOnce {
			failedOnce = true
			_ = tx.AddError(errors.New("temporary billing database outage"))
		}
	}); err != nil {
		t.Fatalf("register billing failure callback: %v", err)
	}
	if err := ProcessMediaJob(context.Background(), *job); err != nil {
		t.Fatalf("process retryable billing failure: %v", err)
	}
	if err := db.Callback().Create().Remove(callbackName); err != nil {
		t.Fatalf("remove billing failure callback: %v", err)
	}

	var requeued model.MediaJob
	if err := db.First(&requeued, job.ID).Error; err != nil {
		t.Fatalf("reload requeued video job: %v", err)
	}
	if requeued.Status != model.MediaJobStatusRunning || requeued.NextPollAt <= 0 || requeued.CompletedAt != 0 {
		t.Fatalf("transient billing failure was terminalized: %+v", requeued)
	}
	var reservationCount int64
	if err := db.Model(&model.SpendBudgetReservation{}).
		Where("request_id = ?", job.RequestID).
		Count(&reservationCount).Error; err != nil {
		t.Fatalf("count retained spend reservation: %v", err)
	}
	if reservationCount != 1 {
		t.Fatalf("retryable billing failure released spend reservation: %d", reservationCount)
	}

	if err := ProcessMediaJob(context.Background(), requeued); err != nil {
		t.Fatalf("retry completed video billing: %v", err)
	}
	var completed model.MediaJob
	if err := db.First(&completed, job.ID).Error; err != nil {
		t.Fatalf("reload completed video job: %v", err)
	}
	if completed.Status != model.MediaJobStatusSucceeded || completed.ArtifactFileID == "" {
		t.Fatalf("billing retry did not complete video job: %+v", completed)
	}
	if err := db.Model(&model.SpendBudgetReservation{}).
		Where("request_id = ?", job.RequestID).
		Count(&reservationCount).Error; err != nil {
		t.Fatalf("count released spend reservation: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("successful retry retained spend reservation: %d", reservationCount)
	}
}

func TestVideoDriverForPrefersPinnedFamily(t *testing.T) {
	baseURL := "https://gateway.example/ark/api/v3"
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "k", BaseURL: &baseURL}
	// The customer-facing name says nothing about Seedance; the submission
	// went through the Ark family after model mapping and pinned it.
	job := model.MediaJob{ModelName: "video-pro", RequestSnapshot: model.JSONValue(`{"_video_family":"ark"}`)}
	driver, _, err := videoDriverFor(job, channel)
	if err != nil || driver.Family() != "ark" {
		t.Fatalf("pinned family: %v %v", driver, err)
	}
	unpinned := model.MediaJob{ModelName: "video-pro", RequestSnapshot: model.JSONValue(`{}`)}
	driver, _, err = videoDriverFor(unpinned, channel)
	if err != nil || driver.Family() != "gateway" {
		t.Fatalf("unpinned falls back to the name rule: %v %v", driver, err)
	}
}

func TestVideoDriverForKeepsLegacyFamilyForUnpinnedSeedanceJobs(t *testing.T) {
	baseURL := "https://gateway.example/v1"
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "k", BaseURL: &baseURL}
	// Submitted before families were pinned: it went out OpenAI-shaped, so
	// the poll must not switch to the Ark path because of the model name.
	job := model.MediaJob{ModelName: "doubao-seedance-1-5-pro-251215", RequestSnapshot: model.JSONValue(`{}`)}
	driver, _, err := videoDriverFor(job, channel)
	if err != nil || driver.Family() != "gateway" {
		t.Fatalf("legacy job family: %v %v", driver, err)
	}
}
