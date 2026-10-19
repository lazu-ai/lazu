package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

const (
	precreatedMediaJobIDRequestKey        = "_lazu_media_job_id"
	videoSubmissionIndeterminateErrorCode = "video_submission_response_incomplete"
	videoSubmissionPersistencePending     = "video provider submission outcome is awaiting durable persistence"
)

func SubmitVideoJob(c *gin.Context) {
	body, err := common.GetRequestBody(c)
	if err != nil {
		writeControlPlaneAPIError(c, types.NewRequestBodyError(err))
		return
	}
	var payload dto.VideoRequest
	if err := common.Unmarshal(body, &payload); err != nil {
		writeControlPlaneCatalogError(c, types.ErrorCodeInvalidRequest, types.D{"field": "body", "reason": "the body is not valid JSON"})
		return
	}
	payload.Model = strings.TrimSpace(payload.Model)
	payload.Prompt = strings.TrimSpace(payload.Prompt)
	if payload.Model == "" {
		writeControlPlaneCatalogError(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
		return
	}
	if payload.Prompt == "" {
		writeControlPlaneCatalogError(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "prompt"})
		return
	}
	providerExecutionRequired := mediaJobProviderExecutionRequired(payload.ArtifactFileID, payload.ArtifactURL)
	routingOutcomeRecorded := false
	if providerExecutionRequired {
		defer func() {
			if !routingOutcomeRecorded {
				middleware.RecordManualRoutingOutcome(c)
			}
		}()
	}
	if payload.ArtifactFileID != "" {
		if _, err := model.GetFileByIDAndOwner(payload.ArtifactFileID, c.GetInt("id"), model.RequestResourcePrincipal(c)); err != nil {
			writeMediaJobArtifactLookupError(c, err, payload.ArtifactFileID)
			return
		}
	}
	modelName := firstNonEmptyMediaJobValue(c.GetString("original_model"), payload.Model)
	requestSnapshot := mediaJobSnapshotWithProviderExecution(
		mediaJobSnapshotWithContext(model.JSONValue(body), c),
		false,
	)
	job, err := resolvePrecreatedVideoJob(c, body, modelName)
	if err != nil {
		writeControlPlaneAPIError(c, err)
		return
	}
	if job == nil {
		_, vendorName := model.GetModelVendorMeta(modelName)
		channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		var channelIDPointer *int
		if channelID > 0 {
			channelIDPointer = &channelID
		}
		input := model.CreateMediaJobInput{
			ProjectID:       common.GetContextKeyInt(c, constant.ContextKeyProjectID),
			PayerUserID:     common.GetContextKeyInt(c, constant.ContextKeyPayerUserID),
			ActorUserID:     c.GetInt("id"),
			UserID:          c.GetInt("id"),
			TokenID:         c.GetInt("token_id"),
			Source:          mediaJobSourceForRequest(c),
			ModelName:       modelName,
			Kind:            model.MediaJobKindVideo,
			Provider:        firstNonEmptyMediaJobValue(payload.Provider, vendorName),
			ChannelID:       channelIDPointer,
			RequestSnapshot: requestSnapshot,
			ArtifactFileID:  payload.ArtifactFileID,
			ArtifactURL:     payload.ArtifactURL,
			UpstreamJobID:   payload.UpstreamJobID,
			SessionID:       common.GetContextKeyString(c, constant.ContextKeySessionID),
			AgentID:         common.GetContextKeyString(c, constant.ContextKeyAgentID),
			RequestID:       c.GetString(common.RequestIdKey),
			DeferProcessing: providerExecutionRequired,
		}
		snapshot, rates := mediaJobAdmissionFromContext(c)
		input.SetAdmission(snapshot, rates)
		job, err = model.CreateMediaJob(input)
		if err != nil {
			writeControlPlaneAPIError(c, mediaJobAdmissionError(c, err))
			return
		}
	}
	var billingProbe *model.MediaJob
	if providerExecutionRequired {
		submission, probe, submitErr := submitVideoProviderWithRetry(c, payload, job)
		if submitErr != nil {
			if markErr := model.MarkMediaJobFailed(job.ID, string(submitErr.GetErrorCode()), submitErr.Error(), time.Now().UTC()); markErr != nil {
				common.SysLog("failed to persist video submission failure: " + markErr.Error())
			}
			writeControlPlaneAPIError(c, submitErr)
			return
		}
		middleware.SetManualRoutingOutcome(c, http.StatusAccepted, "")
		// Do not make the job runnable until its accepted submission outcome is
		// durable. Otherwise a fast terminal poll can update zero rows while the
		// queued 202 later arrives and permanently erases the provider failure.
		if err := middleware.RecordManualRoutingOutcomeSync(c); err != nil {
			writeInternalControlPlaneError(c, err)
			return
		}
		routingOutcomeRecorded = true
		billingProbe = probe
		requestSnapshot = probe.RequestSnapshot
		payload.UpstreamJobID = submission.UpstreamID
		payload.ArtifactURL = submission.ArtifactURL
		payload.Provider = firstNonEmptyMediaJobValue(
			submission.ProviderName,
			common.GetContextKeyString(c, constant.ContextKeyChannelName),
		)
		errorCode := ""
		errorMessage := ""
		if submission.Indeterminate {
			errorCode = videoSubmissionIndeterminateErrorCode
			errorMessage = submission.IndeterminateReason
		}
		job, err = updateMediaJobSubmissionWithRetry(job.ID, c.GetInt("id"), model.UpdateMediaJobSubmissionInput{
			Provider:        payload.Provider,
			ChannelID:       probe.ChannelID,
			RequestSnapshot: requestSnapshot,
			CostSnapshot:    billingProbeCostSnapshot(billingProbe),
			ArtifactURL:     payload.ArtifactURL,
			UpstreamJobID:   payload.UpstreamJobID,
			ErrorCode:       errorCode,
			ErrorMessage:    errorMessage,
			Now:             time.Now().UTC(),
			Ready:           true,
		})
		if err != nil {
			writeInternalControlPlaneError(c, err)
			return
		}
	}
	response := job.ToDTO(false)
	response.Object = "video"
	c.JSON(http.StatusAccepted, response)
}

func submitVideoProviderWithRetry(c *gin.Context, payload dto.VideoRequest, job *model.MediaJob) (*videoProviderSubmission, *model.MediaJob, *types.LazuError) {
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	originalModel := firstNonEmptyMediaJobValue(common.GetContextKeyString(c, constant.ContextKeyOriginalModel), payload.Model)
	var lastProbe *model.MediaJob
	var lastError *types.LazuError

	// A video submission is a single long-running job, not a stream: a failed
	// attempt prefers a different channel over the one that just refused it. If
	// no other channel can serve the model, getChannel falls back to the same
	// one, which submits a second job to it.
	for attempt := 0; attempt <= relayMaxChannelSwitches; attempt++ {
		common.SetContextKey(c, constant.ContextKeyProviderAttempted, false)
		pick := pickSameChannel
		if attempt > 0 && common.GetContextKeyString(c, constant.ContextKeyUsingGroup) != model.ChannelLaneAgent {
			// Same rule as the relay loop: another agent-lane channel may carry a
			// different price than the one the customer chose.
			pick = pickDifferentChannel
		}
		channel, channelErr := getChannel(c, group, originalModel, attempt, pick)
		if channelErr != nil {
			lastError = channelErr
			break
		}
		addUsedChannel(c, channel.Id)

		prepared, prepareErr := prepareVideoProviderRequest(c, payload)
		if prepareErr != nil {
			refundVideoBillingProbe(lastProbe)
			return nil, nil, videoProviderSubmissionLazuError(prepareErr)
		}
		probe, probeErr := buildVideoMediaJobBillingProbe(c, payload, model.JSONValue(prepared.Body))
		if probeErr != nil {
			refundVideoBillingProbe(lastProbe)
			return nil, nil, types.NewErrorWithStatusCode(
				probeErr,
				types.ErrorCodeUpdateDataError,
				http.StatusInternalServerError,
				types.ErrOptionWithSkipRetry(),
			)
		}
		alignVideoBillingProbeWithJob(probe, job)
		if lastProbe != nil && lastProbe.LedgerInputJSON != "" {
			// Safe upstream retries belong to the same admitted request. Keep
			// its original price, rate charge and hold until the final outcome.
			if err := model.CarryMediaJobBillingReservation(probe, lastProbe); err != nil {
				refundVideoBillingProbe(lastProbe)
				return nil, nil, types.NewErrorWithStatusCode(err, types.ErrorCodeUpdateDataError, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
			}
		}
		if _, billingErr := model.ReserveMediaJobBillingBeforeSubmit(probe, time.Now().UTC()); billingErr != nil {
			refundVideoBillingProbe(lastProbe)
			model.ReleaseMediaJobSpendBudgetReservation(*probe)
			return nil, nil, mediaJobAdmissionError(c, errors.Join(model.ErrMediaJobBillingAdmission, billingErr))
		}
		lastProbe = probe
		probe.RequestSnapshot = mediaJobSnapshotWithProviderExecution(probe.RequestSnapshot, true)
		probe.RequestSnapshot = mediaJobSnapshotWithIndeterminateSubmission(
			probe.RequestSnapshot,
			videoSubmissionPersistencePending,
		)
		if _, persistErr := model.UpdateMediaJobSubmission(job.ID, job.UserID, model.UpdateMediaJobSubmissionInput{
			Provider:        job.Provider,
			ChannelID:       probe.ChannelID,
			RequestSnapshot: probe.RequestSnapshot,
			CostSnapshot:    probe.CostSnapshot,
			Now:             time.Now().UTC(),
		}); persistErr != nil {
			refundVideoBillingProbe(lastProbe)
			return nil, nil, types.NewErrorWithStatusCode(
				persistErr,
				types.ErrorCodeUpdateDataError,
				http.StatusInternalServerError,
				types.ErrOptionWithSkipRetry(),
			)
		}

		attemptStartedAt := time.Now()
		middleware.RecordManualRelayPreCall(channel.Id)
		submission, submitErr := submitVideoProvider(c, prepared)
		if submitErr == nil {
			middleware.RecordManualRelayPostCall(channel.Id, attemptStartedAt, http.StatusAccepted, "")
			probe.RequestSnapshot = mediaJobSnapshotWithProviderExecution(probe.RequestSnapshot, true)
			probe.RequestSnapshot = mediaJobSnapshotWithoutIndeterminateSubmission(probe.RequestSnapshot)
			return submission, probe, nil
		}
		if videoProviderSubmissionAccepted(submitErr) {
			middleware.RecordManualRelayPostCall(channel.Id, attemptStartedAt, http.StatusAccepted, "")
			probe.RequestSnapshot = mediaJobSnapshotWithProviderExecution(probe.RequestSnapshot, true)
			probe.RequestSnapshot = mediaJobSnapshotWithIndeterminateSubmission(probe.RequestSnapshot, submitErr.Error())
			return &videoProviderSubmission{
				ProviderName:        strings.ToLower(strings.TrimSpace(prepared.Adaptor.GetChannelName())),
				Indeterminate:       true,
				IndeterminateReason: submitErr.Error(),
			}, probe, nil
		}
		lastError = videoProviderSubmissionLazuError(submitErr)
		middleware.RecordManualRelayPostCall(
			channel.Id,
			attemptStartedAt,
			lastError.StatusCode,
			string(lastError.GetErrorCode()),
		)

		if fullChannel, loadErr := model.GetChannelById(channel.Id, true); loadErr == nil {
			channel = fullChannel
		}
		retry := videoProviderSubmissionRetrySafe(submitErr) &&
			shouldRetry(c, lastError, relayMaxChannelSwitches-attempt)
		processChannelError(
			c,
			*types.NewChannelError(
				channel.Id,
				channel.Type,
				channel.Name,
				channel.ChannelInfo.IsMultiKey,
				common.GetContextKeyString(c, constant.ContextKeyChannelKey),
				channel.GetAutoBan(),
			),
			lastError,
			retry,
		)
		if !retry {
			break
		}
		if lastProbe.LedgerInputJSON == "" {
			if err := refundVideoBillingProbeBeforeRetry(lastProbe); err != nil {
				return nil, nil, types.NewErrorWithStatusCode(
					err,
					types.ErrorCodeUpdateDataError,
					http.StatusInternalServerError,
					types.ErrOptionWithSkipRetry(),
				)
			}
			lastProbe = nil
		}
		recordRetriedRoutingAttempt(c, channel.Id, attempt, attemptStartedAt, lastError)
	}

	refundVideoBillingProbe(lastProbe)
	if lastError == nil {
		lastError = types.NewErrorWithStatusCode(
			errors.New("video provider submission failed"),
			types.ErrorCodeUpstreamError,
			http.StatusBadGateway,
		)
	}
	return nil, nil, lastError
}

func alignVideoBillingProbeWithJob(probe *model.MediaJob, job *model.MediaJob) {
	if probe == nil || job == nil {
		return
	}
	probe.ID = job.ID
	probe.PublicID = job.PublicID
	probe.Source = job.Source
	if strings.TrimSpace(job.RequestID) != "" {
		probe.RequestID = job.RequestID
	}
}

func updateMediaJobSubmissionWithRetry(
	jobID uint,
	userID int,
	input model.UpdateMediaJobSubmissionInput,
) (*model.MediaJob, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		job, err := model.UpdateMediaJobSubmission(jobID, userID, input)
		if err == nil {
			return job, nil
		}
		lastErr = err
		if attempt < 4 {
			time.Sleep(time.Duration(25*(1<<attempt)) * time.Millisecond)
		}
	}
	return nil, fmt.Errorf("persist media job submission after retries: %w", lastErr)
}

func resolvePrecreatedVideoJob(c *gin.Context, body []byte, modelName string) (*model.MediaJob, error) {
	var fields map[string]any
	if err := common.Unmarshal(body, &fields); err != nil {
		return nil, types.WrapErr(err, types.ErrorCodeInvalidRequest, types.D{"field": "body", "reason": "the body is not valid JSON"})
	}
	publicID, _ := fields[precreatedMediaJobIDRequestKey].(string)
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return nil, nil
	}
	invalid := func(reason string) error {
		return types.Err(types.ErrorCodeInvalidPrecreatedMediaJob, types.D{"field": precreatedMediaJobIDRequestKey, "reason": reason})
	}
	job, err := model.GetMediaJobByPublicIDAndUser(publicID, c.GetInt("id"), model.RequestResourcePrincipal(c))
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, types.WrapErr(err, types.ErrorCodeInternalError)
		}
		return nil, invalid("was not found")
	}
	if !model.IsExtensionMediaJobSource(job.Source) ||
		job.Kind != model.MediaJobKindVideo ||
		job.ModelName != modelName ||
		(job.Status != model.MediaJobStatusPending && job.Status != model.MediaJobStatusRunning) {
		return nil, invalid("does not match this request")
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	if tokenID <= 0 || job.TokenID == nil || *job.TokenID != tokenID {
		return nil, invalid("belongs to a different API key")
	}
	return job, nil
}

// mediaJobSourceForRequest files a job under the extension whose client made
// the request (by its client framework), or under the public media API.
func mediaJobSourceForRequest(c *gin.Context) string {
	framework := strings.ToLower(strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyClientFramework)))
	if model.IsExtensionMediaJobSource(framework) {
		return framework
	}
	return model.MediaJobSourceAPI
}

func billingProbeCostSnapshot(probe *model.MediaJob) model.JSONValue {
	if probe == nil {
		return nil
	}
	return probe.CostSnapshot
}

func mediaJobSnapshotWithProviderExecution(snapshot model.JSONValue, enabled bool) model.JSONValue {
	fields := map[string]any{}
	if err := common.Unmarshal(snapshot, &fields); err != nil {
		return snapshot
	}
	delete(fields, "_provider_execution")
	if enabled {
		fields["_provider_execution"] = true
	}
	body, err := common.Marshal(fields)
	if err != nil {
		return snapshot
	}
	return model.JSONValue(body)
}

func mediaJobSnapshotWithIndeterminateSubmission(snapshot model.JSONValue, reason string) model.JSONValue {
	fields := map[string]any{}
	if err := common.Unmarshal(snapshot, &fields); err != nil {
		return snapshot
	}
	fields[model.MediaJobProviderSubmissionIndeterminateSnapshotKey] = strings.TrimSpace(reason)
	body, err := common.Marshal(fields)
	if err != nil {
		return snapshot
	}
	return model.JSONValue(body)
}

func mediaJobSnapshotWithoutIndeterminateSubmission(snapshot model.JSONValue) model.JSONValue {
	fields := map[string]any{}
	if err := common.Unmarshal(snapshot, &fields); err != nil {
		return snapshot
	}
	delete(fields, model.MediaJobProviderSubmissionIndeterminateSnapshotKey)
	body, err := common.Marshal(fields)
	if err != nil {
		return snapshot
	}
	return model.JSONValue(body)
}

func refundVideoBillingProbe(probe *model.MediaJob) {
	if probe == nil {
		return
	}
	if err := model.RefundMediaJobBillingReservationDurably(*probe, time.Now().UTC()); err != nil {
		common.SysLog("failed to refund video billing reservation: " + err.Error())
	}
}

func refundVideoBillingProbeBeforeRetry(probe *model.MediaJob) error {
	if probe == nil {
		return nil
	}
	if err := model.RefundMediaJobBillingReservationDurably(*probe, time.Now().UTC()); err != nil {
		return fmt.Errorf("refund video billing reservation before provider retry: %w", err)
	}
	return nil
}

func buildVideoMediaJobBillingProbe(c *gin.Context, payload dto.VideoRequest, body model.JSONValue) (*model.MediaJob, error) {
	modelName := firstNonEmptyMediaJobValue(c.GetString("original_model"), payload.Model)
	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	var channelIDPointer *int
	if channelID > 0 {
		channelIDPointer = &channelID
	}
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	var tokenIDPointer *int
	if tokenID > 0 {
		tokenIDPointer = &tokenID
	}
	requestSnapshot, err := mediaJobSnapshotWithProviderCredential(mediaJobSnapshotWithContext(body, c), c)
	if err != nil {
		return nil, err
	}
	job := &model.MediaJob{
		ProjectID:       common.GetContextKeyInt(c, constant.ContextKeyProjectID),
		PayerUserID:     common.GetContextKeyInt(c, constant.ContextKeyPayerUserID),
		ActorUserID:     c.GetInt("id"),
		UserID:          c.GetInt("id"),
		TokenID:         tokenIDPointer,
		Source:          model.MediaJobSourceAPI,
		ModelName:       modelName,
		Kind:            model.MediaJobKindVideo,
		ChannelID:       channelIDPointer,
		RequestSnapshot: requestSnapshot,
		SessionID:       common.GetContextKeyString(c, constant.ContextKeySessionID),
		AgentID:         common.GetContextKeyString(c, constant.ContextKeyAgentID),
		RequestID:       c.GetString(common.RequestIdKey),
	}
	snapshot, rates := mediaJobAdmissionFromContext(c)
	job.SetAdmission(snapshot, rates)
	return job, nil
}

func mediaJobSnapshotWithProviderCredential(snapshot model.JSONValue, c *gin.Context) (model.JSONValue, error) {
	credential := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	if strings.TrimSpace(credential) == "" {
		if common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
			return nil, errors.New("selected video provider credential is missing")
		}
		return snapshot, nil
	}
	encrypted, err := model.EncryptMediaJobProviderCredential(credential)
	if err != nil {
		return nil, fmt.Errorf("encrypt selected video provider credential: %w", err)
	}
	fields := map[string]any{}
	if err := common.Unmarshal(snapshot, &fields); err != nil {
		return nil, fmt.Errorf("decode video request snapshot: %w", err)
	}
	delete(fields, "channel_multi_key_index")
	fields[model.MediaJobProviderCredentialSnapshotKey] = encrypted
	body, err := common.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode video request snapshot: %w", err)
	}
	return model.JSONValue(body), nil
}

func videoProviderSubmissionLazuError(err error) *types.LazuError {
	status := http.StatusBadRequest
	code := types.ErrorCodeUpstreamError
	var providerErr *videoProviderSubmitError
	if errors.As(err, &providerErr) && providerErr.StatusCode > 0 {
		status = providerErr.StatusCode
	}
	switch {
	case status == http.StatusTooManyRequests:
		code = types.ErrorCodeUpstreamRateLimited
	case status >= http.StatusInternalServerError:
		code = types.ErrorCodeUpstreamOverloaded
	}
	return types.NewErrorWithStatusCode(err, code, status)
}

func ListVideoJobs(c *gin.Context) {
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && !model.IsValidMediaJobStatus(status) {
		writeControlPlaneCatalogError(c, types.ErrorCodeUnsupportedMediaStatus, types.D{"field": "status", "status": status})
		return
	}
	rows, err := model.ListMediaJobsByUser(c.GetInt("id"), model.MediaJobKindVideo, status, parseMediaJobLimit(c.DefaultQuery("limit", "100")), model.RequestResourcePrincipal(c))
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}
	rows = filterVideoJobsForEphemeralCredential(c, rows)
	data := make([]dto.MediaJobObject, 0, len(rows))
	for _, row := range rows {
		item := row.ToDTO(false)
		item.Object = "video"
		data = append(data, item)
	}
	c.JSON(http.StatusOK, dto.MediaJobListResponse{Object: "list", Data: data})
}

func GetVideoJob(c *gin.Context) {
	job, err := model.GetMediaJobByPublicIDAndUser(c.Param("id"), c.GetInt("id"), model.RequestResourcePrincipal(c))
	if err != nil {
		writeMediaJobLookupError(c, err, c.Param("id"))
		return
	}
	if job.Kind != model.MediaJobKindVideo {
		writeControlPlaneCatalogError(c, types.ErrorCodeVideoNotFound, types.D{"field": "id", "job_id": c.Param("id")})
		return
	}
	if !authorizeEphemeralVideoJobRead(c, job) {
		return
	}
	response := job.ToDTO(true)
	response.Object = "video"
	c.JSON(http.StatusOK, response)
}

func filterVideoJobsForEphemeralCredential(c *gin.Context, rows []model.MediaJob) []model.MediaJob {
	credential, ok := common.GetContextKeyType[*model.EphemeralCredential](c, constant.ContextKeyEphemeralCredential)
	if !ok || credential == nil {
		return rows
	}
	filtered := make([]model.MediaJob, 0, len(rows))
	for _, row := range rows {
		if validateEphemeralVideoJobAccess(c, credential, &row) == nil {
			filtered = append(filtered, row)
		}
	}
	if len(filtered) > 0 {
		recordEphemeralVideoJobAudit(c, credential, &filtered[0], model.EphemeralCredentialAuditUse, "")
		model.MarkEphemeralCredentialUsed(credential.ID, time.Now().UTC())
	}
	return filtered
}

func authorizeEphemeralVideoJobRead(c *gin.Context, job *model.MediaJob) bool {
	credential, ok := common.GetContextKeyType[*model.EphemeralCredential](c, constant.ContextKeyEphemeralCredential)
	if !ok || credential == nil {
		return true
	}
	if err := validateEphemeralVideoJobAccess(c, credential, job); err != nil {
		recordEphemeralVideoJobAudit(c, credential, job, model.EphemeralCredentialAuditDenied, err.Error())
		writeControlPlaneCatalogError(c, types.ErrorCodeEphemeralCredentialDenied, types.D{"reason": err.Error()})
		return false
	}
	recordEphemeralVideoJobAudit(c, credential, job, model.EphemeralCredentialAuditUse, "")
	model.MarkEphemeralCredentialUsed(credential.ID, time.Now().UTC())
	return true
}

func validateEphemeralVideoJobAccess(_ *gin.Context, credential *model.EphemeralCredential, job *model.MediaJob) error {
	if credential == nil || job == nil {
		return nil
	}
	return model.ValidateEphemeralCredentialRequest(credential, job.ModelName, job.Provider, job.SessionID)
}

func recordEphemeralVideoJobAudit(c *gin.Context, credential *model.EphemeralCredential, job *model.MediaJob, eventType string, reason string) {
	if credential == nil {
		return
	}
	audit := model.EphemeralCredentialAudit{
		CredentialID:       credential.ID,
		CredentialPublicID: credential.PublicID,
		EventType:          eventType,
		UserID:             common.GetContextKeyInt(c, constant.ContextKeyUserId),
		TokenID:            common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		SessionID:          firstNonEmptyMediaJobValue(c.GetHeader("X-Session-Id"), c.GetHeader("X-Lazu-Session-Id")),
		RequestID:          c.GetString(common.RequestIdKey),
		Reason:             reason,
		CreatedAt:          common.GetTimestamp(),
	}
	if job != nil {
		audit.ModelName = job.ModelName
		audit.Provider = strings.ToLower(strings.TrimSpace(job.Provider))
		if audit.SessionID == "" {
			audit.SessionID = job.SessionID
		}
	}
	model.RecordEphemeralCredentialAudit(audit)
}
