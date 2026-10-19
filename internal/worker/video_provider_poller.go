package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

const (
	videoProviderPollInterval  = 10 * time.Second
	videoIndeterminateInterval = 5 * time.Minute
	videoProviderMaxAttempts   = 180
	videoProviderResponseLimit = int64(2 << 20)
)

var errVideoProviderConfiguration = errors.New("video provider configuration is incomplete")

type videoProviderPollError struct {
	StatusCode int
	Message    string
}

func (err *videoProviderPollError) Error() string {
	return fmt.Sprintf("video status returned %d: %s", err.StatusCode, err.Message)
}

type videoArtifactDownloadError struct {
	StatusCode int
	Permanent  bool
	Message    string
}

func (err *videoArtifactDownloadError) Error() string {
	return err.Message
}

type videoProviderPollResult struct {
	ArtifactURL string
	ErrorCode   string
	Error       string
	State       string
}

func processUpstreamVideoJob(ctx context.Context, job model.MediaJob, now time.Time) error {
	artifactRecovery := mediaJobProviderArtifactRecovery(job)
	if artifactRecovery || strings.TrimSpace(job.ArtifactURL) != "" {
		channel, err := loadMediaJobChannel(job)
		if err != nil {
			return failMediaJob(job, "video_channel_unavailable", err.Error(), now)
		}
		return completeUpstreamVideoJob(ctx, job, channel, job.ArtifactURL, now)
	}
	if mediaJobProviderSubmissionIndeterminate(job) &&
		strings.TrimSpace(job.UpstreamJobID) == "" &&
		strings.TrimSpace(job.ArtifactURL) == "" {
		if job.Attempts > videoProviderMaxAttempts {
			return failMediaJob(job, "video_generation_timeout", "video generation timed out", now)
		}
		return model.RescheduleMediaJob(job.ID, now.Add(videoIndeterminateInterval))
	}
	channel, err := loadMediaJobChannel(job)
	if err != nil {
		return failMediaJob(job, "video_channel_unavailable", err.Error(), now)
	}
	result, err := pollVideoProvider(ctx, job, channel)
	if err != nil {
		if errors.Is(err, errVideoProviderConfiguration) {
			if job.Attempts > videoProviderMaxAttempts {
				return failMediaJob(job, "video_provider_configuration_invalid", err.Error(), now)
			}
			if rescheduleErr := model.RescheduleMediaJob(job.ID, now.Add(videoIndeterminateInterval)); rescheduleErr != nil {
				return rescheduleErr
			}
			common.SysLog(fmt.Sprintf("video job %s waiting for provider configuration: %v", job.PublicID, err))
			return nil
		}
		var providerErr *videoProviderPollError
		if errors.As(err, &providerErr) && isPermanentVideoProviderPollStatus(providerErr.StatusCode) {
			return failMediaProviderJob(job, "video_provider_poll_rejected", providerErr.Error(), now)
		}
		if job.Attempts > videoProviderMaxAttempts {
			return failMediaProviderJob(job, "video_generation_timeout", "video generation timed out", now)
		}
		if rescheduleErr := model.RescheduleMediaJob(job.ID, now.Add(videoProviderPollInterval)); rescheduleErr != nil {
			return rescheduleErr
		}
		common.SysLog(fmt.Sprintf("video job %s polling error: %v", job.PublicID, err))
		return nil
	}
	switch result.State {
	case model.MediaJobStatusFailed:
		code := result.ErrorCode
		if code == "" {
			code = "video_provider_failed"
		}
		return failMediaProviderJob(job, code, result.Error, now)
	case model.MediaJobStatusSucceeded:
		recoveryJob, err := persistVideoArtifactRecovery(job, result.ArtifactURL, now)
		if err != nil {
			if rescheduleErr := model.RescheduleMediaJob(job.ID, now.Add(videoProviderPollInterval)); rescheduleErr != nil {
				return fmt.Errorf("persist completed video recovery: %w; reschedule: %v", err, rescheduleErr)
			}
			return fmt.Errorf("persist completed video recovery: %w", err)
		}
		return completeUpstreamVideoJob(ctx, recoveryJob, channel, recoveryJob.ArtifactURL, now)
	default:
		if job.Attempts > videoProviderMaxAttempts {
			return failMediaProviderJob(job, "video_generation_timeout", "video generation timed out", now)
		}
		return model.RescheduleMediaJob(job.ID, now.Add(videoProviderPollInterval))
	}
}

func mediaJobProviderSubmissionIndeterminate(job model.MediaJob) bool {
	var snapshot map[string]any
	if common.Unmarshal(job.RequestSnapshot, &snapshot) != nil {
		return false
	}
	reason, _ := snapshot[model.MediaJobProviderSubmissionIndeterminateSnapshotKey].(string)
	return strings.TrimSpace(reason) != ""
}

func mediaJobProviderArtifactRecovery(job model.MediaJob) bool {
	var snapshot map[string]any
	if common.Unmarshal(job.RequestSnapshot, &snapshot) != nil {
		return false
	}
	recovery, _ := snapshot[model.MediaJobProviderArtifactRecoverySnapshotKey].(bool)
	return recovery
}

func persistVideoArtifactRecovery(job model.MediaJob, artifactURL string, now time.Time) (model.MediaJob, error) {
	var snapshot map[string]any
	if err := common.Unmarshal(job.RequestSnapshot, &snapshot); err != nil {
		return job, err
	}
	snapshot[model.MediaJobProviderArtifactRecoverySnapshotKey] = true
	snapshotBody, err := common.Marshal(snapshot)
	if err != nil {
		return job, err
	}
	updated, err := model.UpdateMediaJobSubmission(job.ID, job.UserID, model.UpdateMediaJobSubmissionInput{
		Provider:        job.Provider,
		ChannelID:       job.ChannelID,
		RequestSnapshot: model.JSONValue(snapshotBody),
		CostSnapshot:    job.CostSnapshot,
		ArtifactURL:     artifactURL,
		UpstreamJobID:   job.UpstreamJobID,
		ErrorCode:       job.ErrorCode,
		ErrorMessage:    job.ErrorMessage,
		Now:             now,
	})
	if err != nil {
		return job, err
	}
	return *updated, nil
}

func loadMediaJobChannel(job model.MediaJob) (*model.Channel, error) {
	if job.ChannelID == nil || *job.ChannelID <= 0 {
		return nil, fmt.Errorf("media job channel is missing")
	}
	return model.GetChannelById(*job.ChannelID, true)
}

func pollVideoProvider(ctx context.Context, job model.MediaJob, channel *model.Channel) (*videoProviderPollResult, error) {
	key, keyErr := mediaJobProviderKey(job, channel)
	if keyErr != nil {
		return nil, keyErr
	}
	requestURL, err := videoProviderStatusURL(job, channel)
	if err != nil {
		return nil, err
	}
	response, body, err := doVideoProviderRequest(ctx, http.MethodGet, requestURL, key, channel)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &videoProviderPollError{
			StatusCode: response.StatusCode,
			Message:    compactWorkerProviderError(body),
		}
	}
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode video status: %w", err)
	}
	if channel.Type == constant.ChannelTypeGemini {
		return parseGeminiVideoStatus(payload), nil
	}
	return parseOpenAIVideoStatus(payload), nil
}

func isPermanentVideoProviderPollStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return true
	default:
		return false
	}
}

func videoProviderStatusURL(job model.MediaJob, channel *model.Channel) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	if baseURL == "" {
		return "", fmt.Errorf("video channel base url is empty")
	}
	upstreamID := strings.TrimSpace(job.UpstreamJobID)
	if upstreamID == "" {
		return "", fmt.Errorf("video upstream job id is empty")
	}
	if channel.Type == constant.ChannelTypeGemini {
		if parsed, err := url.Parse(upstreamID); err == nil && parsed.IsAbs() {
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				return "", fmt.Errorf("unsupported video operation url scheme")
			}
			if !sameVideoProviderOrigin(baseURL, parsed.String()) {
				return "", fmt.Errorf("%w: Gemini operation url must use the configured provider origin", errVideoProviderConfiguration)
			}
			return parsed.String(), nil
		}
		upstreamID = strings.TrimPrefix(upstreamID, "/")
		upstreamID = strings.TrimPrefix(upstreamID, "v1beta/")
		return baseURL + "/v1beta/" + upstreamID, nil
	}
	if channel.Type == constant.ChannelTypeCustom {
		template := strings.TrimSpace(channel.GetOtherSettings().VideoStatusURLTemplate)
		if template == "" {
			return "", fmt.Errorf("%w: custom video channel requires video_status_url_template", errVideoProviderConfiguration)
		}
		return renderVideoProviderURLTemplate(baseURL, template, upstreamID)
	}
	return relaycommon.GetFullRequestURL(
		baseURL,
		"/v1/videos/"+url.PathEscape(upstreamID),
		channel.Type,
	), nil
}

func parseOpenAIVideoStatus(payload map[string]any) *videoProviderPollResult {
	status := strings.ToLower(firstWorkerStringAtPaths(payload, "status", "state", "data.status"))
	artifactURL := firstWorkerStringAtPaths(payload, "url", "video.url", "output.url", "data.url")
	switch status {
	case "completed", "complete", "succeeded", "success", "done", "ready":
		return &videoProviderPollResult{State: model.MediaJobStatusSucceeded, ArtifactURL: artifactURL}
	case "failed", "failure", "cancelled", "canceled", "expired", "rejected":
		return &videoProviderPollResult{
			State:     model.MediaJobStatusFailed,
			ErrorCode: firstWorkerStringAtPaths(payload, "error.code", "code"),
			Error:     firstWorkerStringAtPaths(payload, "error.message", "message", "detail"),
		}
	default:
		if artifactURL != "" {
			return &videoProviderPollResult{State: model.MediaJobStatusSucceeded, ArtifactURL: artifactURL}
		}
		return &videoProviderPollResult{State: model.MediaJobStatusRunning}
	}
}

func parseGeminiVideoStatus(payload map[string]any) *videoProviderPollResult {
	if errorMessage := firstWorkerStringAtPaths(payload, "error.message", "response.error.message"); errorMessage != "" {
		return &videoProviderPollResult{
			State:     model.MediaJobStatusFailed,
			ErrorCode: firstWorkerStringAtPaths(payload, "error.status", "error.code"),
			Error:     errorMessage,
		}
	}
	done, _ := payload["done"].(bool)
	artifactURL := firstWorkerStringAtPaths(payload,
		"response.generateVideoResponse.generatedSamples.0.video.uri",
		"response.generateVideoResponse.generatedSamples.0.video.url",
		"response.generatedVideos.0.video.uri",
		"response.generatedVideos.0.video.url",
	)
	if done && artifactURL != "" {
		return &videoProviderPollResult{State: model.MediaJobStatusSucceeded, ArtifactURL: artifactURL}
	}
	if done {
		return &videoProviderPollResult{State: model.MediaJobStatusFailed, ErrorCode: "video_artifact_missing", Error: "video provider completed without an artifact"}
	}
	return &videoProviderPollResult{State: model.MediaJobStatusRunning}
}

func completeUpstreamVideoJob(ctx context.Context, job model.MediaJob, channel *model.Channel, artifactURL string, now time.Time) error {
	if model.MediaJobBillingReservationAmount(job) <= 0 {
		if err := model.CheckMediaJobBillingBeforeArtifactFetch(job, now); err != nil {
			return failMediaJobBilling(job, err, now)
		}
	}

	key, keyErr := mediaJobProviderKey(job, channel)
	if keyErr != nil {
		return failMediaJob(job, "video_channel_key_unavailable", keyErr.Error(), now)
	}
	downloadURL, authenticateDownload, err := videoProviderArtifactDownloadURL(job, channel, artifactURL)
	if err != nil {
		return failMediaJob(job, "artifact_download_url_invalid", err.Error(), now)
	}
	response, data, err := doVideoProviderDownload(ctx, downloadURL, key, channel, authenticateDownload)
	if err != nil {
		var downloadErr *videoArtifactDownloadError
		if errors.As(err, &downloadErr) && downloadErr.Permanent {
			return failMediaJob(job, "artifact_download_failed", downloadErr.Error(), now)
		}
		return rescheduleCompletedVideoArtifact(job, now, "download", err)
	}
	mimeType := mediaJobArtifactMime(response, data, downloadURL)
	file, err := service.StoreMediaJobArtifact(ctx, job, data, mimeType, downloadURL, now)
	if err != nil {
		return rescheduleCompletedVideoArtifact(job, now, "storage", err)
	}
	billingResult, err := model.SettleMediaJobBilling(job, now)
	if err != nil {
		cleanupMediaJobArtifact(ctx, file)
		if !permanentMediaJobBillingError(err) {
			return rescheduleCompletedVideoArtifact(job, now, "billing", err)
		}
		return failMediaJobBilling(job, err, now)
	}
	defer model.ReleaseMediaJobSpendBudgetReservation(job)
	if err := model.MarkMediaJobSucceeded(job.ID, model.CompleteMediaJobInput{
		ArtifactFileID: file.ID,
		CostSnapshot:   billingResult.CostSnapshot,
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

func permanentMediaJobBillingError(err error) bool {
	if errors.Is(err, model.ErrSpendBudgetExceeded) ||
		errors.Is(err, model.ErrMediaJobPriceNotConfigured) ||
		errors.Is(err, model.ErrMediaJobDurationRequired) ||
		errors.Is(err, model.ErrMediaJobBillingReservationUnverified) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "quota is not enough")
}

func rescheduleCompletedVideoArtifact(job model.MediaJob, now time.Time, stage string, err error) error {
	if rescheduleErr := model.RescheduleMediaJob(job.ID, now.Add(videoProviderPollInterval)); rescheduleErr != nil {
		return rescheduleErr
	}
	common.SysLog(fmt.Sprintf("video job %s artifact %s error: %v", job.PublicID, stage, err))
	return nil
}

func doVideoProviderRequest(ctx context.Context, method string, requestURL string, key string, channel *model.Channel) (*http.Response, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, nil, err
	}
	setVideoProviderHeaders(request.Header, key, channel)
	client, err := videoProviderHTTPClient(channel, requestURL, true)
	if err != nil {
		return nil, nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, videoProviderResponseLimit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(body)) > videoProviderResponseLimit {
		return nil, nil, fmt.Errorf("video provider response is too large")
	}
	return response, body, nil
}

func doVideoProviderDownload(ctx context.Context, requestURL string, key string, channel *model.Channel, authenticated bool) (*http.Response, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, nil, err
	}
	if authenticated {
		setVideoProviderHeaders(request.Header, key, channel)
	}
	client, err := videoProviderHTTPClient(channel, requestURL, authenticated)
	if err != nil {
		return nil, nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, videoProviderResponseLimit))
		return nil, nil, &videoArtifactDownloadError{
			StatusCode: response.StatusCode,
			Permanent:  isPermanentVideoArtifactDownloadStatus(response.StatusCode),
			Message:    fmt.Sprintf("video download returned %d: %s", response.StatusCode, compactWorkerProviderError(body)),
		}
	}
	maxSize := constant.MaxFileDownloadMB
	if maxSize <= 0 {
		maxSize = 20
	}
	maxBytes := int64(maxSize * 1024 * 1024)
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, nil, &videoArtifactDownloadError{
			Permanent: true,
			Message:   fmt.Sprintf("video artifact exceeds maximum allowed size: %dMB", maxSize),
		}
	}
	return response, data, nil
}

func isPermanentVideoArtifactDownloadStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusGone:
		return true
	default:
		return false
	}
}

func videoProviderHTTPClient(channel *model.Channel, requestURL string, authenticated bool) (*http.Client, error) {
	if channel == nil {
		return nil, fmt.Errorf("video channel is missing")
	}
	if !authenticated {
		if err := validateExternalVideoArtifactURL(requestURL); err != nil {
			return nil, err
		}
	}
	proxyURL := strings.TrimSpace(channel.GetSetting().Proxy)
	var client *http.Client
	if proxyURL == "" {
		if authenticated {
			client = service.GetRelayHttpClient()
		} else {
			client = service.GetHttpClient()
		}
	} else {
		var err error
		client, err = service.NewProxyHttpClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("configure video channel proxy: %w", err)
		}
	}
	secured := *client
	previousRedirectCheck := client.CheckRedirect
	secured.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if authenticated {
			if len(via) > 0 && !sameVideoProviderOrigin(requestURL, request.URL.String()) {
				return fmt.Errorf("authenticated video artifact redirect changed origin")
			}
		} else if err := validateExternalVideoArtifactURL(request.URL.String()); err != nil {
			return err
		}
		if previousRedirectCheck != nil {
			return previousRedirectCheck(request, via)
		}
		return nil
	}
	return &secured, nil
}

func validateExternalVideoArtifactURL(rawURL string) error {
	fetchSetting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(
		rawURL,
		fetchSetting.EnableSSRFProtection,
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	); err != nil {
		return fmt.Errorf("external video artifact url blocked: %w", err)
	}
	return nil
}

func mediaJobProviderKey(job model.MediaJob, channel *model.Channel) (string, error) {
	if channel == nil {
		return "", fmt.Errorf("video channel is missing")
	}
	var snapshot map[string]any
	if common.Unmarshal(job.RequestSnapshot, &snapshot) == nil {
		if encrypted, ok := snapshot[model.MediaJobProviderCredentialSnapshotKey].(string); ok && strings.TrimSpace(encrypted) != "" {
			key, err := model.DecryptMediaJobProviderCredential(encrypted)
			if err != nil {
				return "", fmt.Errorf("%w: decrypt stored provider credential: %v", errVideoProviderConfiguration, err)
			}
			return key, nil
		}
	}
	if !channel.ChannelInfo.IsMultiKey {
		return channel.Key, nil
	}
	if snapshot != nil {
		if rawIndex, exists := snapshot["channel_multi_key_index"]; exists {
			index := -1
			switch value := rawIndex.(type) {
			case float64:
				index = int(value)
			case int:
				index = value
			}
			keys := channel.GetKeys()
			if index < 0 || index >= len(keys) {
				return "", fmt.Errorf("%w: stored channel key index is unavailable", errVideoProviderConfiguration)
			}
			return keys[index], nil
		}
	}
	return "", fmt.Errorf("%w: stored channel key index is missing", errVideoProviderConfiguration)
}

func videoProviderArtifactDownloadURL(job model.MediaJob, channel *model.Channel, artifactURL string) (string, bool, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	if baseURL == "" {
		return "", false, fmt.Errorf("%w: video channel base url is empty", errVideoProviderConfiguration)
	}
	if candidate := strings.TrimSpace(artifactURL); candidate != "" {
		resolved, err := resolveVideoProviderURL(baseURL, candidate)
		if err != nil {
			return "", false, err
		}
		return resolved, sameVideoProviderOrigin(baseURL, resolved), nil
	}
	if channel.Type == constant.ChannelTypeCustom {
		template := strings.TrimSpace(channel.GetOtherSettings().VideoContentURLTemplate)
		if template == "" {
			return "", false, fmt.Errorf("%w: custom video channel requires video_content_url_template when no artifact url is returned", errVideoProviderConfiguration)
		}
		resolved, err := renderVideoProviderURLTemplate(baseURL, template, job.UpstreamJobID)
		return resolved, err == nil && sameVideoProviderOrigin(baseURL, resolved), err
	}
	return relaycommon.GetFullRequestURL(
		baseURL,
		"/v1/videos/"+url.PathEscape(job.UpstreamJobID)+"/content",
		channel.Type,
	), true, nil
}

func renderVideoProviderURLTemplate(baseURL string, template string, upstreamID string) (string, error) {
	if !strings.Contains(template, "{id}") {
		return "", fmt.Errorf("%w: video url template must contain {id}", errVideoProviderConfiguration)
	}
	return resolveVideoProviderURL(baseURL, strings.ReplaceAll(template, "{id}", url.PathEscape(strings.TrimSpace(upstreamID))))
}

func resolveVideoProviderURL(baseURL string, candidate string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("%w: invalid video channel base url", errVideoProviderConfiguration)
	}
	target, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil {
		return "", fmt.Errorf("invalid video provider url: %w", err)
	}
	if !target.IsAbs() {
		if !strings.HasPrefix(target.Path, "/") {
			base.Path = strings.TrimRight(base.Path, "/") + "/"
		}
		target = base.ResolveReference(target)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", fmt.Errorf("unsupported video provider url scheme")
	}
	if target.Host == "" || target.User != nil {
		return "", fmt.Errorf("invalid video provider url")
	}
	return target.String(), nil
}

func sameVideoProviderOrigin(left string, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Hostname(), rightURL.Hostname()) &&
		effectiveVideoProviderPort(leftURL) == effectiveVideoProviderPort(rightURL)
}

func effectiveVideoProviderPort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
}

func setVideoProviderHeaders(header http.Header, key string, channel *model.Channel) {
	if channel.Type == constant.ChannelTypeGemini {
		header.Set("x-goog-api-key", key)
	} else {
		header.Set("Authorization", "Bearer "+key)
	}
	for name, value := range channel.GetHeaderOverride() {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			header.Set(name, strings.ReplaceAll(text, "{api_key}", key))
		}
	}
}

func firstWorkerStringAtPaths(payload map[string]any, paths ...string) string {
	for _, candidate := range paths {
		value := any(payload)
		for _, part := range strings.Split(candidate, ".") {
			if items, ok := value.([]any); ok {
				index := -1
				if part == "0" {
					index = 0
				}
				if index < 0 || index >= len(items) {
					value = nil
					break
				}
				value = items[index]
				continue
			}
			record, ok := value.(map[string]any)
			if !ok {
				value = nil
				break
			}
			value = record[part]
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		if number, ok := value.(float64); ok {
			return fmt.Sprintf("%.0f", number)
		}
	}
	return ""
}

func compactWorkerProviderError(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 500 {
		return text[:500]
	}
	if text == "" {
		return "empty response"
	}
	return text
}
