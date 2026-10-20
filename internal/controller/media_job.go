package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

type createMediaJobPayload struct {
	Kind           string          `json:"kind"`
	Model          string          `json:"model"`
	Provider       string          `json:"provider"`
	ArtifactFileID string          `json:"artifact_file_id"`
	ArtifactURL    string          `json:"artifact_url"`
	UpstreamJobID  string          `json:"upstream_job_id"`
	Request        model.JSONValue `json:"request"`
}

func CreateMediaJob(c *gin.Context) {
	body, err := common.GetRequestBody(c)
	if err != nil {
		writeControlPlaneAPIError(c, types.NewRequestBodyError(err))
		return
	}
	var payload createMediaJobPayload
	if err := common.Unmarshal(body, &payload); err != nil {
		writeControlPlaneCatalogError(c, types.ErrorCodeInvalidRequest, types.D{"field": "body", "reason": "the body is not valid JSON"})
		return
	}
	payload.Kind = strings.TrimSpace(payload.Kind)
	payload.Model = strings.TrimSpace(payload.Model)
	if payload.Kind == "" {
		writeControlPlaneCatalogError(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "kind"})
		return
	}
	if payload.Model == "" {
		writeControlPlaneCatalogError(c, types.ErrorCodeMissingRequiredParameter, types.D{"field": "model"})
		return
	}
	if !model.IsValidMediaJobKind(payload.Kind) {
		writeControlPlaneCatalogError(c, types.ErrorCodeUnsupportedMediaKind, types.D{"field": "kind", "kind": payload.Kind})
		return
	}
	if !authorizeMediaJobTokenAccess(c, payload.Model) {
		return
	}
	if mediaJobProviderExecutionRequired(payload.ArtifactFileID, payload.ArtifactURL) {
		writeMediaJobProviderExecutionUnavailable(c)
		return
	}
	if payload.ArtifactFileID != "" {
		if _, err := model.GetFileByIDAndOwner(payload.ArtifactFileID, c.GetInt("id"), model.RequestResourcePrincipal(c)); err != nil {
			writeMediaJobArtifactLookupError(c, err, payload.ArtifactFileID)
			return
		}
	}
	snapshot := model.JSONValue(body)
	if len(payload.Request) > 0 {
		snapshot = payload.Request
	}
	snapshot = mediaJobSnapshotWithContext(snapshot, c)
	input := model.CreateMediaJobInput{
		ProjectID:       common.GetContextKeyInt(c, constant.ContextKeyProjectID),
		PayerUserID:     common.GetContextKeyInt(c, constant.ContextKeyPayerUserID),
		ActorUserID:     c.GetInt("id"),
		UserID:          c.GetInt("id"),
		TokenID:         c.GetInt("token_id"),
		ModelName:       payload.Model,
		Kind:            payload.Kind,
		Provider:        payload.Provider,
		RequestSnapshot: snapshot,
		ArtifactFileID:  payload.ArtifactFileID,
		ArtifactURL:     payload.ArtifactURL,
		UpstreamJobID:   payload.UpstreamJobID,
		SessionID:       firstNonEmptyMediaJobValue(c.GetHeader("X-Session-Id"), c.GetHeader("X-Lazu-Session-Id")),
		AgentID:         firstNonEmptyMediaJobValue(c.GetHeader("X-Agent-Id"), c.GetHeader("X-Lazu-Agent-Id")),
		RequestID:       c.GetString(common.RequestIdKey),
	}
	admissionSnapshot, rates := mediaJobAdmissionFromContext(c)
	input.SetAdmission(admissionSnapshot, rates)
	job, err := model.CreateMediaJob(input)
	if err != nil {
		writeControlPlaneAPIError(c, mediaJobAdmissionError(c, err))
		return
	}
	model.RecordMediaJobPendingLog(*job, time.Now().UTC())
	c.JSON(http.StatusAccepted, job.ToDTO(false))
}

func mediaJobAdmissionFromContext(c *gin.Context) (*model.IdentitySnapshot, []ledger.RateLimit) {
	value, _ := c.Get(model.IdentitySnapshotContextKey)
	snapshot, _ := value.(*model.IdentitySnapshot)
	value, _ = c.Get(ledger.RateLimitsContextKey)
	rates, _ := value.([]ledger.RateLimit)
	return snapshot, rates
}

func mediaJobAdmissionError(c *gin.Context, err error) *types.LazuError {
	code, status := types.ErrorCodeUpdateDataError, http.StatusInternalServerError
	if errors.Is(err, model.ErrMediaJobBillingAdmission) {
		code, status = types.ErrorCodeInsufficientQuota, http.StatusPaymentRequired
	}
	if errors.Is(err, model.ErrProjectAccessSuspended) {
		code, status = types.ErrorCodeProjectAccessSuspended, http.StatusForbidden
	}
	if errors.Is(err, model.ErrInvalidAPIKey) || errors.Is(err, model.ErrTokenAccessInactive) {
		code, status = types.ErrorCodeInvalidAPIKey, http.StatusUnauthorized
	}
	if errors.Is(err, model.ErrTokenExpired) {
		code, status = types.ErrorCodeTokenExpired, http.StatusUnauthorized
	}
	if errors.Is(err, model.ErrTokenDisabled) {
		code, status = types.ErrorCodeTokenDisabled, http.StatusUnauthorized
	}
	if errors.Is(err, model.ErrMediaJobPriceNotConfigured) {
		code, status = types.ErrorCodePricingNotConfigured, http.StatusBadRequest
	}
	if errors.Is(err, model.ErrMediaJobAdmissionMissing) || errors.Is(err, ledger.ErrUnavailable) || errors.Is(err, ledger.ErrStale) || errors.Is(err, model.ErrIdentityStale) {
		code, status = types.ErrorCodeGatewayTemporarilyUnavailable, http.StatusServiceUnavailable
		c.Header("Retry-After", "1")
	}
	if errors.Is(err, ledger.ErrRebuilding) {
		code, status = types.ErrorCodeGatewayOverloaded, http.StatusServiceUnavailable
		c.Header("Retry-After", "1")
	}
	if errors.Is(err, ledger.ErrAdmissionPaused) {
		code, status = types.ErrorCodeGatewayTemporarilyUnavailable, http.StatusServiceUnavailable
		c.Header("Retry-After", strconv.Itoa(service.AdmissionPausedRetryAfterSeconds))
	}
	var rejection *ledger.Rejection
	if errors.As(err, &rejection) && rejection.Code == "rate" {
		code, status = types.ErrorCodeRequestRateLimitExceeded, http.StatusTooManyRequests
		if rejection.RateDecision == "token_rpm_exceeded" || rejection.RateDecision == "token_tpm_exceeded" {
			code = types.ErrorCodeTokenRateLimitExceeded
		}
		if rejection.RateDecision == "total_rpm_exceeded" {
			code = types.ErrorCodeTotalRequestRateLimitExceeded
		}
		if rejection.RetryAfterSeconds > 0 {
			c.Header("Retry-After", strconv.FormatInt(rejection.RetryAfterSeconds, 10))
		}
		return types.NewErrorWithStatusCode(err, code, status, types.D{"limit": rejection.RateLimit, "window_minutes": rejection.WindowMinutes}, types.ErrOptionWithSkipRetry())
	}
	return types.NewErrorWithStatusCode(err, code, status, types.ErrOptionWithSkipRetry())
}

func ListMediaJobs(c *gin.Context) {
	limit := parseMediaJobLimit(c.DefaultQuery("limit", "100"))
	kind := strings.TrimSpace(c.Query("kind"))
	status := strings.TrimSpace(c.Query("status"))
	if kind != "" && !model.IsValidMediaJobKind(kind) {
		writeControlPlaneCatalogError(c, types.ErrorCodeUnsupportedMediaKind, types.D{"field": "kind", "kind": kind})
		return
	}
	if status != "" && !model.IsValidMediaJobStatus(status) {
		writeControlPlaneCatalogError(c, types.ErrorCodeUnsupportedMediaStatus, types.D{"field": "status", "status": status})
		return
	}
	rows, err := model.ListMediaJobsByUser(c.GetInt("id"), kind, status, limit, model.RequestResourcePrincipal(c))
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}
	data := make([]dto.MediaJobObject, 0, len(rows))
	for _, row := range rows {
		data = append(data, row.ToDTO(false))
	}
	c.JSON(http.StatusOK, dto.MediaJobListResponse{Object: "list", Data: data})
}

func GetMediaJob(c *gin.Context) {
	job, err := model.GetMediaJobByPublicIDAndUser(c.Param("id"), c.GetInt("id"), model.RequestResourcePrincipal(c))
	if err != nil {
		writeMediaJobLookupError(c, err, c.Param("id"))
		return
	}
	c.JSON(http.StatusOK, job.ToDTO(true))
}

func writeMediaJobLookupError(c *gin.Context, err error, jobID string) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeControlPlaneCatalogError(c, types.ErrorCodeMediaJobNotFound, types.D{"field": "id", "job_id": jobID})
		return
	}
	writeInternalControlPlaneError(c, err)
}

func writeMediaJobArtifactLookupError(c *gin.Context, err error, fileID string) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeControlPlaneCatalogError(c, types.ErrorCodeArtifactFileNotFound, types.D{"field": "artifact_file_id", "file_id": fileID})
		return
	}
	writeInternalControlPlaneError(c, err)
}

func mediaJobProviderExecutionRequired(artifactFileID string, artifactURL string) bool {
	return strings.TrimSpace(artifactFileID) == "" && strings.TrimSpace(artifactURL) == ""
}

func mediaJobSnapshotWithContext(snapshot model.JSONValue, c *gin.Context) model.JSONValue {
	lane := selectedMediaJobLaneFromContext(c)
	if len(snapshot) == 0 {
		return snapshot
	}
	var fields map[string]any
	if err := common.Unmarshal(snapshot, &fields); err != nil || len(fields) == 0 {
		return snapshot
	}
	if lane != "" {
		fields["final_group"] = lane
		fields["channel_group"] = lane
	}
	if common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) {
		fields["channel_multi_key_index"] = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
	}
	body, err := common.Marshal(fields)
	if err != nil {
		return snapshot
	}
	return model.JSONValue(body)
}

func selectedMediaJobLaneFromContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if lane := normalizeConcreteMediaJobLane(c.GetString("final_group")); lane != "" {
		return lane
	}
	if common.GetContextKeyInt(c, constant.ContextKeyChannelId) <= 0 {
		return ""
	}
	return normalizeConcreteMediaJobLane(common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
}

func normalizeConcreteMediaJobLane(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || raw == "default" || raw == "auto" {
		return ""
	}
	lane := model.NormalizeChannelLane(raw)
	if !model.IsValidChannelLane(lane) {
		return ""
	}
	return lane
}

func authorizeMediaJobTokenAccess(c *gin.Context, modelName string) bool {
	if memberModelLimit, ok := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyMemberModelLimit); ok && len(memberModelLimit) > 0 && !model.TokenModelScopeAllows(memberModelLimit, nil, modelName) {
		writeControlPlaneCatalogError(c, types.ErrorCodeTokenModelDenied, types.D{"model": modelName})
		return false
	}
	if common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		tokenModelLimit, ok := common.GetContextKeyType[map[string]bool](c, constant.ContextKeyTokenModelLimit)
		if !ok {
			writeControlPlaneCatalogError(c, types.ErrorCodeTokenNoModelAccess)
			return false
		}
		scopeVendors, _ := common.GetContextKeyType[map[int]struct{}](c, constant.ContextKeyTokenModelScopeVendors)
		if !model.TokenModelScopeAllows(tokenModelLimit, scopeVendors, modelName) {
			writeControlPlaneCatalogError(c, types.ErrorCodeTokenModelDenied, types.D{"model": modelName})
			return false
		}
	}

	vendorID, vendorName := model.GetModelVendorMeta(modelName)
	if common.GetContextKeyBool(c, constant.ContextKeyTokenVendorLimitEnabled) {
		if vendorLimits, ok := common.GetContextKeyType[model.IntSlice](c, constant.ContextKeyTokenVendorLimits); ok {
			for _, blockedVendor := range vendorLimits {
				if blockedVendor == vendorID {
					name := vendorName
					if name == "" {
						name = fmt.Sprintf("ID=%d", vendorID)
					}
					writeControlPlaneCatalogError(c, types.ErrorCodeTokenVendorDenied, types.D{"vendor": name})
					return false
				}
			}
		}
	}

	return true
}

func writeMediaJobProviderExecutionUnavailable(c *gin.Context) {
	writeControlPlaneCatalogError(c, types.ErrorCodeNotImplemented, types.D{"field": "artifact_file_id"})
}

func parseMediaJobLimit(raw string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return 100
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func firstNonEmptyMediaJobValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
