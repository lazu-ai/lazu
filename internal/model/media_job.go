package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel/video"
	"github.com/lazu-ai/lazu/internal/types"

	"gorm.io/gorm"
)

const (
	MediaJobObjectType = "media_job"
	MediaJobRefType    = "media_job_generation"

	MediaJobKindImage = "image"
	MediaJobKindVideo = "video"

	MediaJobSourceAPI = "media_job"

	MediaJobStatusPending   = "pending"
	MediaJobStatusRunning   = "running"
	MediaJobStatusSucceeded = "succeeded"
	MediaJobStatusFailed    = "failed"

	MediaJobProviderSubmissionIndeterminateSnapshotKey = "_provider_submission_indeterminate"
	MediaJobProviderArtifactRecoverySnapshotKey        = "_provider_artifact_recovery"
	// MediaJobProviderFactsSnapshotKey holds what the provider reported at
	// the terminal state; the estimate key holds what the submission priced.
	MediaJobProviderFactsSnapshotKey         = "_provider_facts"
	MediaJobProviderFactsEstimateSnapshotKey = "_provider_facts_estimate"
	// MediaJobVideoFamilySnapshotKey pins the upstream family the submission
	// used, so the worker polls the same way even when the model was mapped.
	MediaJobVideoFamilySnapshotKey = "_video_family"
)

var ErrMediaJobAlreadyCompleted = errors.New("media job already completed")

// GetUserMediaJobCounts returns the all-time media jobs a single account has
// submitted, from any source, and how many of them ended in the failed state.
func GetUserMediaJobCounts(userID int) (total int64, failed int64, err error) {
	if err = CoreDB().Model(&MediaJob{}).
		Where("user_id = ?", userID).
		Count(&total).Error; err != nil {
		return 0, 0, err
	}
	if err = CoreDB().Model(&MediaJob{}).
		Where("user_id = ? AND status = ?", userID, MediaJobStatusFailed).
		Count(&failed).Error; err != nil {
		return 0, 0, err
	}
	return total, failed, nil
}

type MediaJob struct {
	admission       *mediaJobAdmission
	LedgerInputJSON string `json:"-" gorm:"type:text;not null;default:''"`
	ProjectID       int    `json:"-" gorm:"index;not null;default:0"`
	PayerUserID     int    `json:"-" gorm:"not null;default:0"`
	ActorUserID     int    `json:"-" gorm:"not null;default:0"`

	ID               uint      `json:"-" gorm:"primaryKey"`
	PublicID         string    `json:"id" gorm:"type:varchar(64);uniqueIndex;not null"`
	UserID           int       `json:"user_id" gorm:"index;not null"`
	TokenID          *int      `json:"token_id" gorm:"index"`
	Source           string    `json:"source" gorm:"type:varchar(64);index;not null;default:'media_job'"`
	ModelName        string    `json:"model" gorm:"type:varchar(191);index;not null"`
	Kind             string    `json:"kind" gorm:"type:varchar(32);index;not null"`
	Provider         string    `json:"provider" gorm:"type:varchar(128);index"`
	ChannelID        *int      `json:"channel_id" gorm:"index"`
	Status           string    `json:"status" gorm:"type:varchar(32);index;not null"`
	RequestSnapshot  JSONValue `json:"request" gorm:"type:json"`
	CostSnapshot     JSONValue `json:"cost_snapshot" gorm:"type:json"`
	ArtifactFileID   string    `json:"artifact_file_id" gorm:"type:varchar(64);index"`
	ArtifactURL      string    `json:"artifact_url" gorm:"type:text"`
	UpstreamJobID    string    `json:"upstream_job_id" gorm:"type:varchar(191);index"`
	ErrorCode        string    `json:"error_code" gorm:"type:varchar(64);index"`
	ErrorMessage     string    `json:"error_message" gorm:"type:text"`
	SessionID        string    `json:"session_id" gorm:"type:varchar(64);index"`
	AgentID          string    `json:"agent_id" gorm:"type:varchar(64);index"`
	RequestID        string    `json:"request_id" gorm:"type:varchar(64);index"`
	Attempts         int       `json:"attempts" gorm:"default:0"`
	BillingSettledAt int64     `json:"billing_settled_at" gorm:"bigint;index"`
	NextPollAt       int64     `json:"next_poll_at" gorm:"bigint;index"`
	LockedAt         int64     `json:"locked_at" gorm:"bigint;index"`
	StartedAt        int64     `json:"started_at" gorm:"bigint"`
	CompletedAt      int64     `json:"completed_at" gorm:"bigint;index"`
	CreatedAt        time.Time `json:"created_at" gorm:"index"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CreateMediaJobInput struct {
	admission       *mediaJobAdmission
	ProjectID       int
	PayerUserID     int
	ActorUserID     int
	UserID          int
	TokenID         int
	Source          string
	ModelName       string
	Kind            string
	Provider        string
	ChannelID       *int
	RequestSnapshot JSONValue
	CostSnapshot    JSONValue
	ArtifactFileID  string
	ArtifactURL     string
	UpstreamJobID   string
	SessionID       string
	AgentID         string
	RequestID       string
	Now             time.Time
	DeferProcessing bool
}

type UpdateMediaJobSubmissionInput struct {
	Provider        string
	ChannelID       *int
	RequestSnapshot JSONValue
	CostSnapshot    JSONValue
	ArtifactURL     string
	UpstreamJobID   string
	ErrorCode       string
	ErrorMessage    string
	Now             time.Time
	Ready           bool
}

type CompleteMediaJobInput struct {
	ArtifactFileID string
	ArtifactURL    string
	CostSnapshot   JSONValue
	Now            time.Time
}

func (job *MediaJob) BeforeCreate(tx *gorm.DB) error {
	if job.ProjectID == 0 {
		principal, err := captureLegacyResourceIdentity(tx, job.UserID, job.TokenID)
		if err != nil {
			return err
		}
		job.ProjectID, job.PayerUserID, job.ActorUserID = principal.ProjectID, principal.PayerUserID, principal.ActorUserID
	}
	if job.PublicID == "" {
		job.PublicID = common.GeneratePublicID("mjob")
	}
	if job.Status == "" {
		job.Status = MediaJobStatusPending
	}
	if job.Source == "" {
		job.Source = MediaJobSourceAPI
	}
	return nil
}

func CreateMediaJob(input CreateMediaJobInput) (*MediaJob, error) {
	if CoreDB() == nil {
		return nil, errors.New("database is not initialized")
	}
	input = normalizeCreateMediaJobInput(input)
	if input.UserID <= 0 {
		return nil, errors.New("user_id is required")
	}
	if input.ModelName == "" {
		return nil, errors.New("model is required")
	}
	if !IsValidMediaJobKind(input.Kind) {
		return nil, fmt.Errorf("unsupported media job kind %q", input.Kind)
	}
	tokenID := (*int)(nil)
	if input.TokenID > 0 {
		tokenID = &input.TokenID
	}
	status := MediaJobStatusPending
	lockedAt := int64(0)
	nextPollAt := input.Now.Unix()
	if input.DeferProcessing {
		status = MediaJobStatusRunning
		lockedAt = input.Now.Add(15 * time.Minute).Unix()
		nextPollAt = lockedAt
	}
	row := &MediaJob{
		admission: input.admission,
		ProjectID: input.ProjectID, PayerUserID: input.PayerUserID, ActorUserID: input.ActorUserID,
		UserID:          input.UserID,
		TokenID:         tokenID,
		Source:          input.Source,
		ModelName:       input.ModelName,
		Kind:            input.Kind,
		Provider:        input.Provider,
		ChannelID:       input.ChannelID,
		Status:          status,
		RequestSnapshot: jsonValueOrDefault(input.RequestSnapshot, "{}"),
		CostSnapshot:    jsonValueOrDefault(input.CostSnapshot, "{}"),
		ArtifactFileID:  truncateMediaJobString(input.ArtifactFileID, 64),
		ArtifactURL:     strings.TrimSpace(input.ArtifactURL),
		UpstreamJobID:   truncateMediaJobString(input.UpstreamJobID, 191),
		SessionID:       truncateMediaJobString(input.SessionID, 64),
		AgentID:         truncateMediaJobString(input.AgentID, 64),
		RequestID:       truncateMediaJobString(input.RequestID, 64),
		NextPollAt:      nextPollAt,
		LockedAt:        lockedAt,
	}
	if modernMediaBilling() {
		if row.admission == nil {
			return nil, ErrMediaJobAdmissionMissing
		}
		if !input.DeferProcessing {
			row.PublicID = common.GeneratePublicID("mjob")
			// Artifact imports are financial jobs too. Reserve their zero or
			// paid quote before making the job runnable by the worker.
			if _, err := ReserveMediaJobBillingBeforeSubmit(row, input.Now); err != nil {
				return nil, errors.Join(ErrMediaJobBillingAdmission, err)
			}
		}
	}
	if err := CoreDB().Create(row).Error; err != nil {
		if row.LedgerInputJSON != "" {
			if releaseErr := RefundMediaJobBillingReservationDurably(*row, input.Now); releaseErr != nil {
				return nil, errors.Join(err, releaseErr)
			}
		}
		return nil, err
	}
	return row, nil
}

func UpdateMediaJobSubmission(jobID uint, userID int, input UpdateMediaJobSubmissionInput) (*MediaJob, error) {
	if CoreDB() == nil {
		return nil, errors.New("database is not initialized")
	}
	if jobID == 0 || userID < 0 {
		return nil, errors.New("media job and user are required")
	}
	input.Now = normalizeMediaJobTime(input.Now)
	updates := map[string]any{
		"provider":         truncateMediaJobString(strings.ToLower(strings.TrimSpace(input.Provider)), 128),
		"channel_id":       input.ChannelID,
		"request_snapshot": jsonValueOrDefault(input.RequestSnapshot, "{}"),
		"cost_snapshot":    jsonValueOrDefault(input.CostSnapshot, "{}"),
		"artifact_url":     strings.TrimSpace(input.ArtifactURL),
		"upstream_job_id":  truncateMediaJobString(input.UpstreamJobID, 191),
		"error_code":       truncateMediaJobString(input.ErrorCode, 64),
		"error_message":    strings.TrimSpace(input.ErrorMessage),
	}
	if input.Ready {
		updates["status"] = MediaJobStatusPending
		updates["locked_at"] = int64(0)
		updates["next_poll_at"] = input.Now.Unix()
	}
	result := CoreDB().Model(&MediaJob{}).
		Where("id = ? AND user_id = ? AND status IN ?", jobID, userID, []string{MediaJobStatusPending, MediaJobStatusRunning}).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrMediaJobAlreadyCompleted
	}
	var row MediaJob
	if err := CoreDB().First(&row, jobID).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func GetMediaJobByPublicIDAndUser(publicID string, userID int, principals ...ProjectPrincipal) (*MediaJob, error) {
	var row MediaJob
	err := requestResourceScope(CoreDB(), userID, principals).Where("public_id = ?", strings.TrimSpace(publicID)).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func ListMediaJobsByUser(userID int, kind string, status string, limit int, principals ...ProjectPrincipal) ([]MediaJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	kind = normalizeMediaJobKind(kind)
	status = normalizeMediaJobStatus(status)
	query := requestResourceScope(CoreDB(), userID, principals)
	if kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var rows []MediaJob
	err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func ClaimRunnableMediaJobs(now time.Time, limit int, lease time.Duration) ([]MediaJob, error) {
	if CoreDB() == nil {
		return nil, errors.New("database is not initialized")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if lease <= 0 {
		lease = 5 * time.Minute
	}
	now = normalizeMediaJobTime(now)
	nowUnix := now.Unix()
	lockBefore := now.Add(-lease).Unix()
	var candidates []MediaJob
	err := CoreDB().Where(
		"(status = ? OR (status = ? AND locked_at <= ?)) AND (next_poll_at = 0 OR next_poll_at <= ?)",
		MediaJobStatusPending,
		MediaJobStatusRunning,
		lockBefore,
		nowUnix,
	).Order("created_at ASC, id ASC").Limit(limit).Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	claimed := make([]MediaJob, 0, len(candidates))
	for _, candidate := range candidates {
		result := CoreDB().Model(&MediaJob{}).
			Where("id = ? AND (status = ? OR (status = ? AND locked_at <= ?))", candidate.ID, MediaJobStatusPending, MediaJobStatusRunning, lockBefore).
			Where("next_poll_at = 0 OR next_poll_at <= ?", nowUnix).
			Updates(map[string]any{
				"status":       MediaJobStatusRunning,
				"locked_at":    nowUnix,
				"started_at":   gorm.Expr("CASE WHEN started_at = 0 THEN ? ELSE started_at END", nowUnix),
				"attempts":     gorm.Expr("attempts + 1"),
				"next_poll_at": now.Add(lease).Unix(),
			})
		if result.Error != nil {
			return claimed, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		var row MediaJob
		if err := CoreDB().First(&row, candidate.ID).Error; err != nil {
			return claimed, err
		}
		claimed = append(claimed, row)
	}
	return claimed, nil
}

func MarkMediaJobSucceeded(jobID uint, input CompleteMediaJobInput) error {
	if CoreDB() == nil {
		return errors.New("database is not initialized")
	}
	input.Now = normalizeMediaJobTime(input.Now)
	artifactFileID := truncateMediaJobString(input.ArtifactFileID, 64)
	artifactURL := strings.TrimSpace(input.ArtifactURL)
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&MediaJob{}).
			Where("id = ? AND status IN ?", jobID, []string{MediaJobStatusPending, MediaJobStatusRunning}).
			Updates(map[string]any{
				"status":           MediaJobStatusSucceeded,
				"artifact_file_id": artifactFileID,
				"artifact_url":     artifactURL,
				"cost_snapshot":    jsonValueOrDefault(input.CostSnapshot, "{}"),
				"completed_at":     input.Now.Unix(),
				"locked_at":        0,
				"next_poll_at":     0,
				"error_code":       "",
				"error_message":    "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return mediaJobCompletionConflictError(tx, jobID)
		}
		return runMediaJobSucceededHooks(tx, jobID, input, artifactFileID, artifactURL)
	})
}

func MarkMediaJobFailed(jobID uint, code string, message string, now time.Time) error {
	return markMediaJobFailed(jobID, code, message, now, false)
}

// MarkMediaJobProviderFailed records a terminal asynchronous provider verdict.
// Video submission initially returns 202, so without this final write channel
// health would permanently count an accepted job as successful even when the
// provider later reports that generation failed.
func MarkMediaJobProviderFailed(jobID uint, code string, message string, now time.Time) error {
	return markMediaJobFailed(jobID, code, message, now, true)
}

func markMediaJobFailed(jobID uint, code string, message string, now time.Time, providerVerdict bool) error {
	if CoreDB() == nil {
		return errors.New("database is not initialized")
	}
	now = normalizeMediaJobTime(now)
	code = truncateMediaJobString(code, 64)
	message = strings.TrimSpace(message)
	var job MediaJob
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		if providerVerdict {
			if err := tx.Select("id", "kind", "model_name", "request_id", "channel_id", "request_snapshot", "created_at").First(&job, jobID).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&MediaJob{}).
			Where("id = ? AND status IN ?", jobID, []string{MediaJobStatusPending, MediaJobStatusRunning}).
			Updates(map[string]any{
				"status":        MediaJobStatusFailed,
				"error_code":    code,
				"error_message": message,
				"completed_at":  now.Unix(),
				"locked_at":     0,
				"next_poll_at":  0,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return mediaJobCompletionConflictError(tx, jobID)
		}
		return runMediaJobFailedHooks(tx, jobID, code, message)
	})
	if err != nil || !providerVerdict {
		return err
	}
	// Routing facts live in the Telemetry store, which is a different
	// database from the job's in production, so the verdict follows the
	// job's commit rather than joining its transaction.
	return recordMediaJobProviderVerdict(job, mediaJobProviderFailureVerdict(code), now)
}

// RecordMediaJobProviderSuccess replaces a delivered job's accepted (202)
// outcome with a 200 whose duration runs from submission to the artifact, so
// a video lane's latency is the wait a caller actually has, and lets the
// success end any failure streak the route's breaker is counting.
//
// The job is already succeeded when this runs; a failure here loses a health
// sample, never the job, so callers log it rather than retry the job.
func RecordMediaJobProviderSuccess(job MediaJob, now time.Time) error {
	if !MediaJobProviderExecuted(job) {
		// A customer can import a finished video with its own task id and
		// artifact URL. Nothing was asked of the route, so nothing it did
		// can speak for it.
		return nil
	}
	now = normalizeMediaJobTime(now)
	verdict := mediaJobProviderVerdict{statusCode: http.StatusOK}
	if !job.CreatedAt.IsZero() && now.After(job.CreatedAt) {
		duration := int(now.Sub(job.CreatedAt).Milliseconds())
		verdict.durationMs = &duration
	}
	return recordMediaJobProviderVerdict(job, verdict, now)
}

// mediaJobProviderVerdict is the terminal outcome of an asynchronous job, in
// the routing outcome's own terms.
type mediaJobProviderVerdict struct {
	statusCode int
	code       string
	errorType  string
	// durationMs is set only by a delivered job: a failure's elapsed time is
	// how long the provider took to give up, not a latency sample.
	durationMs *int
}

// mediaJobProviderFailureVerdict files a terminal failure by whose fault it
// is. The provider codes themselves are classified where the video families
// are known (video.ClassifyFailure); a refusal leaves the health population
// as a 4xx, so a moderated prompt never counts against the route.
func mediaJobProviderFailureVerdict(code string) mediaJobProviderVerdict {
	switch video.ClassifyFailure(code) {
	case video.FailureCaller:
		return mediaJobProviderVerdict{statusCode: http.StatusBadRequest, code: code, errorType: string(types.ErrorTypeInvalidRequest)}
	case video.FailureRateLimited:
		return mediaJobProviderVerdict{statusCode: http.StatusTooManyRequests, code: code, errorType: string(types.ErrorTypeRateLimit)}
	default:
		return mediaJobProviderVerdict{statusCode: http.StatusBadGateway, code: code, errorType: string(types.ErrorTypeUpstreamError)}
	}
}

// recordMediaJobProviderVerdict replaces an asynchronous job's accepted (202)
// outcome with the provider's final verdict, so channel health counts the
// generation rather than the submission, and folds that verdict into the
// route's breaker. The submission itself never reaches the breaker: an
// accepted task says the API answered, not that the route can make a video.
func recordMediaJobProviderVerdict(job MediaJob, verdict mediaJobProviderVerdict, now time.Time) error {
	providerAttempted := true
	healthEligible, healthFailed := types.ChannelHealthVerdict(
		verdict.statusCode, types.ErrorCode(verdict.code), providerAttempted,
	)
	recordMediaJobBreakerEvidence(job, healthEligible, healthFailed, verdict.code, now)

	requestID := strings.TrimSpace(job.RequestID)
	store := TelemetryDB()
	if requestID == "" || store == nil {
		return nil
	}
	statusCode := verdict.statusCode
	if !routingFactsAreViews(store) {
		if !store.Migrator().HasTable(&RoutingOutcome{}) {
			return nil
		}
		updates := map[string]any{
			"status_code":        statusCode,
			"error_code":         verdict.code,
			"error_type":         verdict.errorType,
			"provider_attempted": providerAttempted,
			"health_eligible":    healthEligible,
			"health_failed":      healthFailed,
			"outcome_at":         now,
		}
		if verdict.durationMs != nil {
			updates["total_duration_ms"] = *verdict.durationMs
		}
		result := store.Model(&RoutingOutcome{}).
			Where("request_id = ?", requestID).
			Updates(updates)
		if result.Error != nil || result.RowsAffected > 0 {
			return result.Error
		}
	}
	outcome, err := GetOutcomeByRequestID(requestID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		decision, err := GetDecisionByRequestID(requestID)
		if err != nil {
			return err
		}
		outcome = &RoutingOutcome{
			RequestID: requestID, DecisionID: decision.ID, ChannelID: job.ChannelID,
			FinalGroup: mediaJobRoutingFinalGroup(job.RequestSnapshot),
		}
	} else if err != nil {
		return err
	}
	outcome.OutcomeAt = now
	outcome.StatusCode, outcome.ErrorCode, outcome.ErrorType = &statusCode, verdict.code, verdict.errorType
	outcome.ProviderAttempted = &providerAttempted
	outcome.HealthEligible, outcome.HealthFailed = &healthEligible, &healthFailed
	if verdict.durationMs != nil {
		outcome.TotalDurationMs = verdict.durationMs
	}
	if !routingFactsAreViews(store) {
		return store.Clauses(routingOutcomeConflictClause()).Create(outcome).Error
	}
	return RecordRoutingOutcomeSync(outcome)
}

// recordMediaJobBreakerEvidence is the asynchronous half of the relay's
// recordRouteBreakerEvidence: the same eligibility rule, the same writer.
// Without it a video route could only ever accumulate submission failures,
// and no success could end the streak.
func recordMediaJobBreakerEvidence(job MediaJob, eligible bool, failed bool, reason string, now time.Time) {
	if !eligible || job.ChannelID == nil || *job.ChannelID <= 0 || !MediaJobProviderExecuted(job) {
		// An imported job's task id is the customer's to make up; polling it
		// says nothing about the route, and must not trip or clear it.
		return
	}
	modelName := strings.TrimSpace(job.ModelName)
	lane := mediaJobRoutingFinalGroup(job.RequestSnapshot)
	if !ShouldRecordRouteHealthEvidence(modelName, lane, *job.ChannelID, !failed) {
		return
	}
	if err := RecordRouteHealthEvidence(modelName, lane, *job.ChannelID, !failed, reason, now); err != nil {
		common.SysLog("media job: failed to record route health evidence: " + err.Error())
	}
}

// MediaJobProviderExecutionSnapshotKey marks a job the gateway itself
// submitted to a provider, as opposed to one a customer imported.
const MediaJobProviderExecutionSnapshotKey = "_provider_execution"

// MediaJobProviderExecuted reports whether the gateway submitted the job to
// its provider. Only such a job's outcome is evidence about the route.
func MediaJobProviderExecuted(job MediaJob) bool {
	var snapshot map[string]any
	if common.Unmarshal(job.RequestSnapshot, &snapshot) != nil {
		return false
	}
	enabled, _ := snapshot[MediaJobProviderExecutionSnapshotKey].(bool)
	return enabled
}

func mediaJobRoutingFinalGroup(snapshot JSONValue) string {
	fields := map[string]any{}
	if err := json.Unmarshal(snapshot, &fields); err == nil {
		for _, key := range []string{"final_group", "channel_group"} {
			if lane, ok := fields[key].(string); ok {
				lane = NormalizeChannelLane(lane)
				if IsValidChannelLane(lane) {
					return lane
				}
			}
		}
	}
	return ChannelLaneDirect
}

func RescheduleMediaJob(jobID uint, nextPollAt time.Time) error {
	if CoreDB() == nil {
		return errors.New("database is not initialized")
	}
	nextPollAt = normalizeMediaJobTime(nextPollAt)
	result := CoreDB().Model(&MediaJob{}).
		Where("id = ? AND status = ?", jobID, MediaJobStatusRunning).
		Updates(map[string]any{
			"locked_at":    0,
			"next_poll_at": nextPollAt.Unix(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return mediaJobCompletionConflictError(CoreDB(), jobID)
	}
	return nil
}

func RescheduleMediaJobWithArtifactFile(jobID uint, userID int, artifactFileID string, requestSnapshot JSONValue, code string, message string, nextPollAt time.Time) (*MediaJob, error) {
	if CoreDB() == nil {
		return nil, errors.New("database is not initialized")
	}
	if jobID == 0 || userID <= 0 || strings.TrimSpace(artifactFileID) == "" {
		return nil, errors.New("media job artifact is required")
	}
	nextPollAt = normalizeMediaJobTime(nextPollAt)
	updates := map[string]any{
		"status":           MediaJobStatusRunning,
		"artifact_file_id": truncateMediaJobString(artifactFileID, 64),
		"error_code":       truncateMediaJobString(code, 64),
		"error_message":    strings.TrimSpace(message),
		"locked_at":        0,
		"next_poll_at":     nextPollAt.Unix(),
	}
	if len(requestSnapshot) > 0 {
		updates["request_snapshot"] = jsonValueOrDefault(requestSnapshot, "{}")
	}
	result := CoreDB().Model(&MediaJob{}).
		Where("id = ? AND user_id = ? AND status IN ?", jobID, userID, []string{MediaJobStatusPending, MediaJobStatusRunning}).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, mediaJobCompletionConflictError(CoreDB(), jobID)
	}
	var row MediaJob
	if err := CoreDB().First(&row, jobID).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func mediaJobCompletionConflictError(tx *gorm.DB, jobID uint) error {
	var row MediaJob
	if err := tx.Select("id", "status", "completed_at").First(&row, jobID).Error; err != nil {
		return err
	}
	if row.Status == MediaJobStatusSucceeded || row.Status == MediaJobStatusFailed || row.CompletedAt > 0 {
		return ErrMediaJobAlreadyCompleted
	}
	return gorm.ErrRecordNotFound
}

func IsValidMediaJobKind(kind string) bool {
	switch normalizeMediaJobKind(kind) {
	case MediaJobKindImage, MediaJobKindVideo:
		return true
	default:
		return false
	}
}

func IsValidMediaJobStatus(status string) bool {
	switch normalizeMediaJobStatus(status) {
	case MediaJobStatusPending, MediaJobStatusRunning, MediaJobStatusSucceeded, MediaJobStatusFailed:
		return true
	default:
		return false
	}
}

func (job MediaJob) ToDTO(includeRequest bool) dto.MediaJobObject {
	out := dto.MediaJobObject{
		ID:          job.PublicID,
		Object:      MediaJobObjectType,
		Kind:        job.Kind,
		Source:      mediaJobSource(job),
		Model:       job.ModelName,
		Status:      job.Status,
		CreatedAt:   unixOrZero(job.CreatedAt),
		UpdatedAt:   unixOrZero(job.UpdatedAt),
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
		SessionID:   job.SessionID,
		AgentID:     job.AgentID,
		RequestID:   job.RequestID,
	}
	if job.Status != MediaJobStatusFailed && (job.ArtifactFileID != "" || job.ArtifactURL != "") {
		out.Artifact = &dto.MediaJobArtifact{
			FileID: job.ArtifactFileID,
			URL:    job.ArtifactURL,
		}
	}
	if job.ErrorCode != "" || job.ErrorMessage != "" {
		out.Error = &dto.MediaJobError{
			Code:    job.ErrorCode,
			Message: job.ErrorMessage,
		}
	}
	if includeRequest && len(job.RequestSnapshot) > 0 {
		out.Request = publicMediaJobRequest(job.RequestSnapshot)
	}
	if len(job.CostSnapshot) > 0 {
		out.CostSnapshot = publicMediaJobCost(job.CostSnapshot)
	}
	if job.Kind == MediaJobKindVideo {
		// What the job was billed on: the provider's report, with the
		// submission's estimate for any dimension it left out (an aggregator
		// that reports only its cost still shows the clip's length and tier).
		if _, reported := mediaJobSnapshotFacts(job, MediaJobProviderFactsSnapshotKey); reported {
			if facts, ok := MediaJobProviderFacts(job); ok {
				out.Usage = facts.Public()
			}
		}
	}
	return out
}

func publicMediaJobRequest(snapshot JSONValue) json.RawMessage {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(snapshot, &fields); err != nil {
		return nil
	}
	for key := range fields {
		// Underscored keys, the channel's key index and the routing lanes are
		// Lazu's own bookkeeping, not part of what the customer asked for.
		if strings.HasPrefix(key, "_") || key == "channel_multi_key_index" || key == "channel_group" || key == "final_group" {
			delete(fields, key)
		}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil
	}
	return json.RawMessage(body)
}

func normalizeCreateMediaJobInput(input CreateMediaJobInput) CreateMediaJobInput {
	input.Now = normalizeMediaJobTime(input.Now)
	input.Source = normalizeMediaJobSource(input.Source)
	input.ModelName = truncateMediaJobString(input.ModelName, 191)
	input.Kind = normalizeMediaJobKind(input.Kind)
	input.Provider = truncateMediaJobString(strings.ToLower(strings.TrimSpace(input.Provider)), 128)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	return input
}

func normalizeMediaJobKind(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

func normalizeMediaJobStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func normalizeMediaJobSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if IsExtensionMediaJobSource(source) {
		return source
	}
	return MediaJobSourceAPI
}

func mediaJobSource(job MediaJob) string {
	return normalizeMediaJobSource(job.Source)
}

func normalizeMediaJobTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func jsonValueOrDefault(value JSONValue, fallback string) JSONValue {
	if len(strings.TrimSpace(string(value))) == 0 || !json.Valid([]byte(value)) {
		return JSONValue([]byte(fallback))
	}
	return value
}

func truncateMediaJobString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func unixOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

// BillingUserID is captured at acceptance, independent of later membership
// changes. Legacy jobs use their original personal account.
func (job MediaJob) BillingUserID() int {
	if job.ProjectID > 0 {
		return job.PayerUserID
	}
	return job.UserID
}
func (job MediaJob) ActorID() int {
	if job.ProjectID > 0 {
		return job.ActorUserID
	}
	return job.UserID
}

// Public cost snapshots expose customer billing only; the persisted snapshot
// remains available to internal settlement and platform accounting.
func publicMediaJobCost(snapshot JSONValue) json.RawMessage {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(snapshot, &fields); err != nil {
		return nil
	}
	safe := map[string]json.RawMessage{}
	for _, key := range []string{"pricing_version", "customer_charge_microusd", "customer_charge_usd", "charged_microusd", "line_items"} {
		if value, ok := fields[key]; ok {
			safe[key] = value
		}
	}
	body, err := json.Marshal(safe)
	if err != nil {
		return nil
	}
	return body
}
