package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"gorm.io/gorm"
)

type MediaJobPollerTask struct{}

func NewMediaJobPollerTask() *MediaJobPollerTask {
	return &MediaJobPollerTask{}
}

func (t *MediaJobPollerTask) Name() string {
	return "media_job_poller"
}

func (t *MediaJobPollerTask) Interval() time.Duration {
	return time.Duration(common.GetEnvOrDefault("MEDIA_JOB_WORKER_INTERVAL_SECONDS", 30)) * time.Second
}

func (t *MediaJobPollerTask) Run(ctx context.Context) error {
	if !common.GetEnvOrDefaultBool("MEDIA_JOB_WORKER_ENABLED", true) {
		return nil
	}
	limit := common.GetEnvOrDefault("MEDIA_JOB_WORKER_BATCH_SIZE", 20)
	lease := time.Duration(common.GetEnvOrDefault("MEDIA_JOB_CLAIM_LEASE_SECONDS", 300)) * time.Second
	jobs, err := model.ClaimRunnableMediaJobs(time.Now().UTC(), limit, lease)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := ProcessMediaJob(ctx, job); err != nil {
			common.SysLog(fmt.Sprintf("media job %s processing error: %v", job.PublicID, err))
		}
	}
	return nil
}

func ProcessMediaJob(ctx context.Context, job model.MediaJob) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	now := time.Now().UTC()
	if job.ArtifactFileID != "" {
		retryArtifact := model.MediaJobRetriesArtifact(job)
		file, err := model.GetFileByIDAndOwner(job.ArtifactFileID, job.UserID, job.ResourcePrincipal())
		if err != nil {
			code := "artifact_file_not_found"
			message := "artifact file not found"
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				code = "artifact_file_lookup_failed"
				message = err.Error()
			}
			return failMediaJob(job, code, message, now)
		}
		costSnapshot := job.CostSnapshot
		if !retryArtifact {
			billingResult, err := model.SettleMediaJobBilling(job, now)
			if err != nil {
				return failMediaJobBilling(job, err, now)
			}
			costSnapshot = billingResult.CostSnapshot
			defer model.ReleaseMediaJobSpendBudgetReservation(job)
		}
		if err := model.MarkMediaJobSucceeded(job.ID, model.CompleteMediaJobInput{
			ArtifactFileID: file.ID,
			CostSnapshot:   costSnapshot,
			Now:            now,
		}); err != nil {
			if errors.Is(err, model.ErrMediaJobAlreadyCompleted) {
				return nil
			}
			return err
		}
		return nil
	}
	if job.Kind == model.MediaJobKindVideo &&
		(job.UpstreamJobID != "" || mediaJobProviderExecution(job)) {
		return processUpstreamVideoJob(ctx, job, now)
	}
	if job.ArtifactURL != "" {
		return completeMediaJobFromURL(ctx, job, now)
	}
	return failMediaJob(job, "provider_not_implemented", "media provider execution is not implemented yet", now)
}

func mediaJobProviderExecution(job model.MediaJob) bool {
	var snapshot map[string]any
	if common.Unmarshal(job.RequestSnapshot, &snapshot) != nil {
		return false
	}
	enabled, _ := snapshot["_provider_execution"].(bool)
	return enabled
}

func completeMediaJobFromURL(ctx context.Context, job model.MediaJob, now time.Time) error {
	retryArtifact := model.MediaJobRetriesArtifact(job)
	if retryArtifact && job.Attempts > videoProviderMaxAttempts {
		return failMediaJob(job, "artifact_retry_exhausted", "image artifact retrieval timed out", now)
	}
	if !retryArtifact && model.MediaJobBillingReservationAmount(job) <= 0 {
		if err := model.CheckMediaJobBillingBeforeArtifactFetch(job, now); err != nil {
			return failMediaJobBilling(job, err, now)
		}
	}
	if !retryArtifact {
		defer model.ReleaseMediaJobSpendBudgetReservation(job)
	}

	resp, err := service.DoDownloadRequest(job.ArtifactURL, "media_job_artifact")
	if err != nil {
		if retryArtifact {
			return rescheduleMediaArtifact(job, now, "download", err)
		}
		return failMediaJob(job, "artifact_download_failed", err.Error(), now)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err := fmt.Errorf("artifact download returned status %d", resp.StatusCode)
		if retryArtifact && retryableMediaArtifactStatus(resp.StatusCode) {
			return rescheduleMediaArtifact(job, now, "download", err)
		}
		return failMediaJob(job, "artifact_download_failed", err.Error(), now)
	}
	maxSize := constant.MaxFileDownloadMB
	if maxSize <= 0 {
		maxSize = 20
	}
	maxBytes := int64(maxSize * 1024 * 1024)
	reader := io.LimitReader(resp.Body, maxBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		if retryArtifact {
			return rescheduleMediaArtifact(job, now, "download", err)
		}
		return failMediaJob(job, "artifact_download_failed", err.Error(), now)
	}
	if int64(len(data)) > maxBytes {
		return failMediaJob(job, "artifact_too_large", fmt.Sprintf("artifact exceeds maximum allowed size: %dMB", maxSize), now)
	}
	file, err := service.StoreMediaJobArtifact(ctx, job, data, mediaJobArtifactMime(resp, data, job.ArtifactURL), job.ArtifactURL, now)
	if err != nil {
		if retryArtifact {
			return rescheduleMediaArtifact(job, now, "storage", err)
		}
		return failMediaJob(job, "artifact_storage_failed", err.Error(), now)
	}
	costSnapshot := job.CostSnapshot
	if !retryArtifact {
		billingResult, err := model.SettleMediaJobBilling(job, now)
		if err != nil {
			cleanupMediaJobArtifact(ctx, file)
			return failMediaJobBilling(job, err, now)
		}
		costSnapshot = billingResult.CostSnapshot
	}
	if err := model.MarkMediaJobSucceeded(job.ID, model.CompleteMediaJobInput{
		ArtifactFileID: file.ID,
		ArtifactURL:    job.ArtifactURL,
		CostSnapshot:   costSnapshot,
		Now:            now,
	}); err != nil {
		cleanupMediaJobArtifact(ctx, file)
		if errors.Is(err, model.ErrMediaJobAlreadyCompleted) {
			return nil
		}
		return err
	}
	return nil
}

func retryableMediaArtifactStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooEarly ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}

func rescheduleMediaArtifact(job model.MediaJob, now time.Time, stage string, cause error) error {
	if err := model.RescheduleMediaJob(job.ID, now.Add(videoProviderPollInterval)); err != nil {
		return err
	}
	common.SysLog(fmt.Sprintf("media job %s artifact %s retry: %v", job.PublicID, stage, cause))
	return nil
}

func failMediaJobBilling(job model.MediaJob, err error, now time.Time) error {
	code := "media_billing_failed"
	if errors.Is(err, model.ErrSpendBudgetExceeded) {
		code = "spend_budget_exceeded"
	} else if errors.Is(err, model.ErrMediaJobPriceNotConfigured) {
		code = "media_price_not_configured"
	} else if errors.Is(err, model.ErrMediaJobDurationRequired) {
		code = "media_duration_required"
	}
	return failMediaJob(job, code, err.Error(), now)
}

func failMediaJob(job model.MediaJob, code string, message string, now time.Time) error {
	// Keep the job retryable until its billing reservation has actually been
	// returned. Refunds are idempotent, so a later attempt can safely repeat
	// this step if marking the terminal state fails.
	if err := model.RefundMediaJobBillingReservation(job, now); err != nil &&
		!errors.Is(err, model.ErrMediaJobBillingReservationUnverified) {
		return err
	}
	if err := model.MarkMediaJobFailed(job.ID, code, message, now); err != nil {
		return err
	}
	model.RecordMediaJobFailureLog(job.ID, now)
	return nil
}

func failMediaProviderJob(job model.MediaJob, code string, message string, now time.Time) error {
	// Keep the billing transition identical to other terminal failures, but
	// also replace the submission-time 202 with the provider's final verdict so
	// lane health observes the asynchronous result.
	if err := model.RefundMediaJobBillingReservation(job, now); err != nil &&
		!errors.Is(err, model.ErrMediaJobBillingReservationUnverified) {
		return err
	}
	if err := model.MarkMediaJobProviderFailed(job.ID, code, message, now); err != nil {
		return err
	}
	model.RecordMediaJobFailureLog(job.ID, now)
	return nil
}

func cleanupMediaJobArtifact(ctx context.Context, file *model.File) {
	if err := service.DeleteMediaJobArtifact(ctx, file); err != nil {
		common.SysLog(fmt.Sprintf("failed to clean up media job artifact %s after billing failure: %v", file.ID, err))
	}
}

func mediaJobArtifactMime(resp *http.Response, data []byte, rawURL string) string {
	contentType := ""
	if resp != nil {
		contentType = strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	}
	if contentType == "" || contentType == "application/octet-stream" {
		if len(data) > 0 {
			contentType = http.DetectContentType(data)
		}
	}
	if contentType != "" && contentType != "application/octet-stream" &&
		strings.Contains(contentType, "/") {
		return contentType
	}
	filename := rawURL
	if parsed, err := url.Parse(rawURL); err == nil {
		filename = parsed.Path
	}
	if ext := path.Ext(filename); ext != "" {
		if guessed := mime.TypeByExtension(ext); guessed != "" {
			return strings.TrimSpace(strings.Split(guessed, ";")[0])
		}
	}
	return "application/octet-stream"
}
