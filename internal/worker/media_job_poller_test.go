package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/internal/storage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

type mediaJobFakeStorage struct {
	objects map[string][]byte
	putErr  error
}

func (s *mediaJobFakeStorage) Put(_ context.Context, key string, r io.Reader, _ string) error {
	if s.putErr != nil {
		return s.putErr
	}
	if s.objects == nil {
		s.objects = map[string][]byte{}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.objects[key] = data
	return nil
}

func (s *mediaJobFakeStorage) PutFromFile(_ context.Context, _ string, _ string) error {
	return nil
}

func (s *mediaJobFakeStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.objects[key])), nil
}

func (s *mediaJobFakeStorage) Head(_ context.Context, key string) (storage.ObjectMeta, error) {
	data, ok := s.objects[key]
	return storage.ObjectMeta{Exists: ok, ContentLength: int64(len(data))}, nil
}

func (s *mediaJobFakeStorage) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func (s *mediaJobFakeStorage) ListByPrefix(_ context.Context, _ string) ([]storage.ObjectInfo, error) {
	return nil, nil
}

func setupMediaJobWorkerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB := model.CoreDB()
	originalLogDB := model.ConfiguredTelemetryDB()
	originalLogConsumeEnabled := common.LogConsumeEnabled
	originalRedisEnabled := common.RedisEnabled
	common.LogConsumeEnabled = true
	common.RedisEnabled = false
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Channel{},
		&model.File{},
		&model.MediaJob{},
		&model.ModelSellPrice{},
		&model.ProviderCostPrice{},
		&model.WalletLedger{},
		&model.LedgerLegacySettlementWaiver{},
		&model.Log{},
		&model.SpendBudget{},
		&model.SpendBudgetAlert{},
		&model.SpendBudgetReservation{},
		&model.SpendBudgetCharge{}, &model.ProjectSpend{},
		&model.RoutingDecision{},
		&model.RoutingOutcome{},
	); err != nil {
		t.Fatalf("failed to migrate media job tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(originalDB)
		model.SetTelemetryDB(originalLogDB)
		common.LogConsumeEnabled = originalLogConsumeEnabled
		common.RedisEnabled = originalRedisEnabled
	})
	return db
}

func seedWorkerMediaJobUserAndFile(t *testing.T, db *gorm.DB) *model.File {
	t.Helper()
	user := &model.User{
		Id:          1,
		Username:    "media-job-worker-user",
		DisplayName: "media-job-worker-user",
		Status:      common.UserStatusEnabled,
		Quota:       billing.MicroUSD(1_000_000),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	token := &model.Token{
		Id:          2,
		UserId:      user.Id,
		Key:         "mediajobworkertoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: billing.MicroUSD(1_000_000),
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	file := &model.File{
		ID:         "file-lazu-worker-artifact",
		UserID:     user.Id,
		TokenID:    &token.Id,
		Filename:   "artifact.png",
		Purpose:    "vision",
		MimeType:   "image/png",
		Bytes:      256,
		StorageKey: "test/worker-artifact.png",
		Status:     model.FileStatusProcessed,
		CreatedAt:  time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC).Unix(),
	}
	if err := db.Create(file).Error; err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	return file
}

func seedWorkerMediaJobPricing(t *testing.T, db *gorm.DB, modelName string) {
	t.Helper()
	requestUSD := 0.01
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      modelName,
		ChannelGroup:   model.ChannelLaneDirect,
		RequestUSD:     &requestUSD,
		Active:         true,
		PricingVersion: 1,
		Source:         "test",
	}).Error; err != nil {
		t.Fatalf("failed to create media job sell price: %v", err)
	}
}

func TestMediaJobArtifactMimeUsesSignedURLPathExtension(t *testing.T) {
	response := &http.Response{Header: http.Header{"Content-Type": []string{"application/octet-stream"}}}
	got := mediaJobArtifactMime(response, nil, "https://cdn.example.com/video.mp4?token=secret")
	if got != "video/mp4" {
		t.Fatalf("signed URL MIME type=%q, want video/mp4", got)
	}
}

func TestMediaJobPollerMarksArtifactJobsSucceededAndUnsupportedJobsFailed(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	file := seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "gpt-image-1")
	fakeStorage := &mediaJobFakeStorage{objects: map[string][]byte{}}
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = previousFetchSetting
	}()
	previousMaxDownloadMB := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 20
	defer func() {
		constant.MaxFileDownloadMB = previousMaxDownloadMB
	}()
	artifactServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nmedia-job-artifact"))
	}))
	defer artifactServer.Close()

	if _, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:         1,
		TokenID:        2,
		ModelName:      "gpt-image-1",
		Kind:           model.MediaJobKindImage,
		ArtifactFileID: file.ID,
	}); err != nil {
		t.Fatalf("failed to create artifact job: %v", err)
	}
	if _, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:    1,
		TokenID:   2,
		ModelName: "veo-3",
		Kind:      model.MediaJobKindVideo,
	}); err != nil {
		t.Fatalf("failed to create unsupported job: %v", err)
	}
	if _, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:      1,
		TokenID:     2,
		ModelName:   "gpt-image-1",
		Kind:        model.MediaJobKindImage,
		ArtifactURL: artifactServer.URL + "/artifact.png",
	}); err != nil {
		t.Fatalf("failed to create url artifact job: %v", err)
	}

	task := NewMediaJobPollerTask()
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("media job poller error: %v", err)
	}

	var rows []model.MediaJob
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("failed to load jobs: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(rows))
	}
	if rows[0].Status != model.MediaJobStatusSucceeded || rows[0].ArtifactFileID != file.ID {
		t.Fatalf("expected artifact job to succeed, got %+v", rows[0])
	}
	if rows[1].Status != model.MediaJobStatusFailed || rows[1].ErrorCode != "provider_not_implemented" {
		t.Fatalf("expected unsupported job to fail, got %+v", rows[1])
	}
	if rows[2].Status != model.MediaJobStatusSucceeded || rows[2].ArtifactFileID == "" {
		t.Fatalf("expected url artifact job to create a file and succeed, got %+v", rows[2])
	}
	if len(fakeStorage.objects) == 0 {
		t.Fatal("expected downloaded artifact to be stored")
	}
	var artifactFile model.File
	if err := db.Where("id = ?", rows[2].ArtifactFileID).First(&artifactFile).Error; err != nil {
		t.Fatalf("expected artifact file record: %v", err)
	}
	if artifactFile.Purpose != "vision" || artifactFile.MimeType != "image/png" {
		t.Fatalf("unexpected artifact file: %+v", artifactFile)
	}
}

func TestMediaJobPollerReleasesFileArtifactSpendBudgetReservation(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	file := seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "gpt-image-budget")
	if err := db.Create(&model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             2,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(20_000),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}).Error; err != nil {
		t.Fatalf("failed to create spend budget: %v", err)
	}
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:         1,
		TokenID:        2,
		ModelName:      "gpt-image-budget",
		Kind:           model.MediaJobKindImage,
		ArtifactFileID: file.ID,
		RequestID:      "req-file-artifact-budget",
	})
	if err != nil {
		t.Fatalf("failed to create artifact job: %v", err)
	}

	if err := ProcessMediaJob(context.Background(), *job); err != nil {
		t.Fatalf("ProcessMediaJob error: %v", err)
	}

	var stored model.MediaJob
	if err := db.First(&stored, job.ID).Error; err != nil {
		t.Fatalf("failed to reload job: %v", err)
	}
	if stored.Status != model.MediaJobStatusSucceeded {
		t.Fatalf("expected file artifact job to succeed, got %+v", stored)
	}
	var reservationCount int64
	if err := db.Model(&model.SpendBudgetReservation{}).Where("request_id = ?", "req-file-artifact-budget").Count(&reservationCount).Error; err != nil {
		t.Fatalf("failed to count reservations: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("expected file artifact reservation to be released, got %d", reservationCount)
	}
}

func TestFailMediaJobKeepsJobRetryableUntilReservationRefunds(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	_ = seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "gpt-image-refund-retry")
	now := time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC)
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:    1,
		TokenID:   2,
		ModelName: "gpt-image-refund-retry",
		Kind:      model.MediaJobKindImage,
		RequestID: "req-refund-retry",
	})
	if err != nil {
		t.Fatalf("failed to create media job: %v", err)
	}
	if err := db.Model(job).Update("status", model.MediaJobStatusRunning).Error; err != nil {
		t.Fatalf("mark media job running: %v", err)
	}
	job.Status = model.MediaJobStatusRunning
	if _, err := model.ReserveMediaJobBillingBeforeSubmit(job, now); err != nil {
		t.Fatalf("failed to reserve media job billing: %v", err)
	}

	refundErr := errors.New("simulated refund ledger failure")
	callbackName := "test:fail_media_job_refund"
	if err := db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		ledger, ok := tx.Statement.Dest.(*model.WalletLedger)
		if ok && ledger.RefType == "media_job_reservation_refund" {
			_ = tx.AddError(refundErr)
		}
	}); err != nil {
		t.Fatalf("register refund callback: %v", err)
	}
	if err := failMediaJob(*job, "provider_failed", "provider failed", now.Add(time.Minute)); !errors.Is(err, refundErr) {
		t.Fatalf("expected refund failure, got %v", err)
	}
	var retryable model.MediaJob
	if err := db.First(&retryable, job.ID).Error; err != nil {
		t.Fatalf("reload retryable job: %v", err)
	}
	if retryable.Status != model.MediaJobStatusRunning {
		t.Fatalf("job became terminal before refund succeeded: %+v", retryable)
	}

	if err := db.Callback().Create().Remove(callbackName); err != nil {
		t.Fatalf("remove refund callback: %v", err)
	}
	if err := failMediaJob(*job, "provider_failed", "provider failed", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("retry failMediaJob: %v", err)
	}
	var failed model.MediaJob
	if err := db.First(&failed, job.ID).Error; err != nil {
		t.Fatalf("reload failed job: %v", err)
	}
	if failed.Status != model.MediaJobStatusFailed {
		t.Fatalf("expected terminal failure after refund, got %+v", failed)
	}
	var user model.User
	if err := db.Select("quota").First(&user, 1).Error; err != nil {
		t.Fatalf("reload refunded user: %v", err)
	}
	if user.Quota != billing.MicroUSD(1_000_000) {
		t.Fatalf("reserved quota was not refunded: %d", user.Quota)
	}
}

func TestMediaJobPollerCleansURLArtifactWhenBillingFails(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	_ = seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "gpt-image-blocked")
	if err := db.Create(&model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             2,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}).Error; err != nil {
		t.Fatalf("failed to create spend budget: %v", err)
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
	downloadAttempts := 0
	artifactServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloadAttempts++
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nmedia-job-artifact"))
	}))
	defer artifactServer.Close()

	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:      1,
		TokenID:     2,
		ModelName:   "gpt-image-blocked",
		Kind:        model.MediaJobKindImage,
		ArtifactURL: artifactServer.URL + "/artifact.png",
	})
	if err != nil {
		t.Fatalf("failed to create url artifact job: %v", err)
	}
	var beforeFileCount int64
	if err := db.Model(&model.File{}).Count(&beforeFileCount).Error; err != nil {
		t.Fatalf("failed to count files before run: %v", err)
	}

	task := NewMediaJobPollerTask()
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("media job poller error: %v", err)
	}

	var storedJob model.MediaJob
	if err := db.First(&storedJob, job.ID).Error; err != nil {
		t.Fatalf("failed to reload media job: %v", err)
	}
	if storedJob.Status != model.MediaJobStatusFailed || storedJob.ErrorCode != "spend_budget_exceeded" {
		t.Fatalf("expected spend budget failure, got %+v", storedJob)
	}
	if downloadAttempts != 0 {
		t.Fatalf("expected spend budget failure before artifact download, got %d downloads", downloadAttempts)
	}
	var afterFileCount int64
	if err := db.Model(&model.File{}).Count(&afterFileCount).Error; err != nil {
		t.Fatalf("failed to count files after run: %v", err)
	}
	if afterFileCount != beforeFileCount {
		t.Fatalf("expected downloaded artifact file to be deleted, before=%d after=%d", beforeFileCount, afterFileCount)
	}
	if len(fakeStorage.objects) != 0 {
		t.Fatalf("expected stored artifact object to be deleted, got %d objects", len(fakeStorage.objects))
	}
	var user model.User
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if user.StorageBytesUsed != 0 {
		t.Fatalf("expected storage bytes to be refunded, got %d", user.StorageBytesUsed)
	}
}

func TestCompleteMediaJobFromURLCleansDuplicateArtifactWhenAlreadyCompleted(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	_ = seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "gpt-image-duplicate")
	fakeStorage := &mediaJobFakeStorage{objects: map[string][]byte{}}
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = previousFetchSetting
	}()
	artifactServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nduplicate-artifact"))
	}))
	defer artifactServer.Close()

	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		UserID:      1,
		TokenID:     2,
		ModelName:   "gpt-image-duplicate",
		Kind:        model.MediaJobKindImage,
		ArtifactURL: artifactServer.URL + "/artifact.png",
	})
	if err != nil {
		t.Fatalf("failed to create url artifact job: %v", err)
	}
	task := NewMediaJobPollerTask()
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("media job poller error: %v", err)
	}

	var completed model.MediaJob
	if err := db.First(&completed, job.ID).Error; err != nil {
		t.Fatalf("failed to reload completed job: %v", err)
	}
	if completed.Status != model.MediaJobStatusSucceeded || completed.ArtifactFileID == "" {
		t.Fatalf("expected first completion to succeed, got %+v", completed)
	}
	var beforeFileCount int64
	if err := db.Model(&model.File{}).Count(&beforeFileCount).Error; err != nil {
		t.Fatalf("failed to count files before duplicate completion: %v", err)
	}
	beforeStorageObjects := len(fakeStorage.objects)
	var beforeUser model.User
	if err := db.First(&beforeUser, 1).Error; err != nil {
		t.Fatalf("failed to reload user before duplicate completion: %v", err)
	}

	staleJob := *job
	staleJob.Status = model.MediaJobStatusRunning
	if err := completeMediaJobFromURL(context.Background(), staleJob, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatalf("expected duplicate completion to be cleaned up, got %v", err)
	}

	var after model.MediaJob
	if err := db.First(&after, job.ID).Error; err != nil {
		t.Fatalf("failed to reload job after duplicate completion: %v", err)
	}
	if after.ArtifactFileID != completed.ArtifactFileID {
		t.Fatalf("expected duplicate completion not to overwrite artifact, before=%s after=%s", completed.ArtifactFileID, after.ArtifactFileID)
	}
	var afterFileCount int64
	if err := db.Model(&model.File{}).Count(&afterFileCount).Error; err != nil {
		t.Fatalf("failed to count files after duplicate completion: %v", err)
	}
	if afterFileCount != beforeFileCount {
		t.Fatalf("expected duplicate artifact file to be deleted, before=%d after=%d", beforeFileCount, afterFileCount)
	}
	if len(fakeStorage.objects) != beforeStorageObjects {
		t.Fatalf("expected duplicate storage object to be deleted, before=%d after=%d", beforeStorageObjects, len(fakeStorage.objects))
	}
	var afterUser model.User
	if err := db.First(&afterUser, 1).Error; err != nil {
		t.Fatalf("failed to reload user after duplicate completion: %v", err)
	}
	if afterUser.StorageBytesUsed != beforeUser.StorageBytesUsed {
		t.Fatalf("expected duplicate cleanup to refund storage bytes, before=%d after=%d", beforeUser.StorageBytesUsed, afterUser.StorageBytesUsed)
	}
}

// The worker settles an in-flight job from the principal the job was accepted
// with, not from the request that is no longer there. That principal has to
// reach the job's own artifact file inside its project: the project, the actor
// and the token id all have to line up, or the job fails as
// artifact_file_not_found long after the customer was charged for it.
func TestMediaJobPollerSettlesInFlightProjectJobFromItsStoredPrincipal(t *testing.T) {
	db := setupMediaJobWorkerTestDB(t)
	file := seedWorkerMediaJobUserAndFile(t, db)
	seedWorkerMediaJobPricing(t, db, "gpt-image-1")
	fakeStorage := &mediaJobFakeStorage{objects: map[string][]byte{}}
	restoreStorage := service.SetUserFileStorageClientForTest(fakeStorage)
	defer restoreStorage()
	if err := db.AutoMigrate(&model.Project{}, &model.ProjectMember{}); err != nil {
		t.Fatalf("failed to migrate project tables: %v", err)
	}
	if err := db.Create(&model.Project{ID: 5, OwnerUserID: 1, Name: "Worker project"}).Error; err != nil {
		t.Fatalf("failed to create project: %v", err)
	}
	// The shape the relay writes: the project, its payer, the member who made
	// the request and the Key that carried it.
	if err := db.Model(&model.File{}).Where("id = ?", file.ID).
		Updates(map[string]any{"project_id": 5, "payer_user_id": 1, "actor_user_id": 1, "user_id": 1}).Error; err != nil {
		t.Fatalf("failed to attribute artifact file: %v", err)
	}
	job, err := model.CreateMediaJob(model.CreateMediaJobInput{
		ProjectID:      5,
		PayerUserID:    1,
		ActorUserID:    1,
		UserID:         1,
		TokenID:        2,
		ModelName:      "gpt-image-1",
		Kind:           model.MediaJobKindImage,
		ArtifactFileID: file.ID,
	})
	if err != nil {
		t.Fatalf("failed to create project job: %v", err)
	}
	if err := ProcessMediaJob(context.Background(), *job); err != nil {
		t.Fatalf("process project job: %v", err)
	}
	var settled model.MediaJob
	if err := db.First(&settled, job.ID).Error; err != nil {
		t.Fatalf("failed to reload job: %v", err)
	}
	if settled.Status != model.MediaJobStatusSucceeded {
		t.Fatalf("in-flight project job did not settle: status=%s code=%s message=%s", settled.Status, settled.ErrorCode, settled.ErrorMessage)
	}
}
