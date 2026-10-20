package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/channel/video"
	"github.com/lazu-ai/lazu/internal/service"
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
	Facts       *dto.VideoFacts
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
		recoveryJob, err := persistVideoArtifactRecovery(job, result.ArtifactURL, result.Facts, now)
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

// persistVideoArtifactRecovery records a completed upstream job before its
// artifact is fetched, with the facts the provider reported, so a download
// or billing retry neither re-polls nor loses the reported dimensions.
func persistVideoArtifactRecovery(job model.MediaJob, artifactURL string, facts *dto.VideoFacts, now time.Time) (model.MediaJob, error) {
	var snapshot map[string]any
	if err := common.Unmarshal(job.RequestSnapshot, &snapshot); err != nil {
		return job, err
	}
	snapshot[model.MediaJobProviderArtifactRecoverySnapshotKey] = true
	snapshotBody, err := common.Marshal(snapshot)
	if err != nil {
		return job, err
	}
	requestSnapshot := model.JSONValue(snapshotBody)
	if facts != nil && !facts.IsZero() {
		requestSnapshot, err = model.SnapshotWithVideoFacts(requestSnapshot, model.MediaJobProviderFactsSnapshotKey, *facts)
		if err != nil {
			return job, err
		}
	}
	updated, err := model.UpdateMediaJobSubmission(job.ID, job.UserID, model.UpdateMediaJobSubmissionInput{
		Provider:        job.Provider,
		ChannelID:       job.ChannelID,
		RequestSnapshot: requestSnapshot,
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

// videoDriverFor resolves the family driver and the channel view a job's
// upstream calls need, with the credential the submission pinned.
func videoDriverFor(job model.MediaJob, channel *model.Channel) (video.Driver, video.Channel, error) {
	if channel == nil {
		return nil, video.Channel{}, fmt.Errorf("video channel is missing")
	}
	key, err := mediaJobProviderKey(job, channel)
	if err != nil {
		return nil, video.Channel{}, err
	}
	ch := video.Channel{
		Type:           channel.Type,
		BaseURL:        channel.GetBaseURL(),
		Key:            key,
		Settings:       channel.GetOtherSettings(),
		HeaderOverride: channel.GetHeaderOverride(),
	}
	// The submission pinned its family; a mapped model name must not send the
	// poll down another family's path. A job submitted before families were
	// pinned was sent by channel type alone, so it is polled the same way.
	if pinned := model.MediaJobVideoFamily(job); pinned != "" {
		ch.Settings.VideoFamily = pinned
	} else {
		ch.Settings.VideoFamily = video.LegacyFamily(channel.Type)
	}
	driver, err := video.Select(ch, job.ModelName)
	if err != nil {
		return nil, ch, fmt.Errorf("%w: %v", errVideoProviderConfiguration, err)
	}
	return driver, ch, nil
}

func pollVideoProvider(ctx context.Context, job model.MediaJob, channel *model.Channel) (*videoProviderPollResult, error) {
	driver, ch, err := videoDriverFor(job, channel)
	if err != nil {
		return nil, err
	}
	statusRequest, err := driver.BuildStatus(ch, video.Job{UpstreamID: job.UpstreamJobID, ArtifactURL: job.ArtifactURL, Model: job.ModelName})
	if err != nil {
		if errors.Is(err, video.ErrConfiguration) {
			return nil, fmt.Errorf("%w: %v", errVideoProviderConfiguration, err)
		}
		return nil, err
	}
	response, body, err := service.DoVideoProviderRequest(ctx, http.MethodGet, statusRequest.URL, statusRequest.Headers, channel.GetSetting().Proxy)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &videoProviderPollError{
			StatusCode: response.StatusCode,
			Message:    compactWorkerProviderError(body),
		}
	}
	parsed, err := driver.ParseStatus(ch, body)
	if err != nil {
		return nil, err
	}
	return pollResultFromStatus(parsed), nil
}

// pollResultFromStatus maps a driver's state words onto the job states.
func pollResultFromStatus(status *video.StatusResult) *videoProviderPollResult {
	switch status.State {
	case video.StateSucceeded:
		return &videoProviderPollResult{State: model.MediaJobStatusSucceeded, ArtifactURL: status.ArtifactURL, Facts: status.Facts}
	case video.StateFailed:
		return &videoProviderPollResult{State: model.MediaJobStatusFailed, ErrorCode: status.ErrorCode, Error: status.ErrorMessage}
	case video.StateCancelled:
		code := status.ErrorCode
		if code == "" {
			code = "video_provider_cancelled"
		}
		message := status.ErrorMessage
		if message == "" {
			message = "video generation was cancelled upstream"
		}
		return &videoProviderPollResult{State: model.MediaJobStatusFailed, ErrorCode: code, Error: message}
	default:
		return &videoProviderPollResult{State: model.MediaJobStatusRunning}
	}
}

func isPermanentVideoProviderPollStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return true
	default:
		return false
	}
}

func completeUpstreamVideoJob(ctx context.Context, job model.MediaJob, channel *model.Channel, artifactURL string, now time.Time) error {
	if model.MediaJobBillingReservationAmount(job) <= 0 {
		if err := model.CheckMediaJobBillingBeforeArtifactFetch(job, now); err != nil {
			return failMediaJobBilling(job, err, now)
		}
	}

	driver, ch, err := videoDriverFor(job, channel)
	if err != nil {
		return failMediaJob(job, "video_channel_key_unavailable", err.Error(), now)
	}
	download, err := driver.BuildDownload(ch, video.Job{UpstreamID: job.UpstreamJobID, ArtifactURL: job.ArtifactURL, Model: job.ModelName}, artifactURL)
	if err != nil {
		return failMediaJob(job, "artifact_download_url_invalid", err.Error(), now)
	}
	response, data, err := doVideoProviderDownload(ctx, download, channel)
	if err != nil {
		var downloadErr *videoArtifactDownloadError
		if errors.As(err, &downloadErr) && downloadErr.Permanent {
			return failMediaJob(job, "artifact_download_failed", downloadErr.Error(), now)
		}
		return rescheduleCompletedVideoArtifact(job, now, "download", err)
	}
	mimeType := mediaJobArtifactMime(response, data, download.URL)
	file, err := service.StoreMediaJobArtifact(ctx, job, data, mimeType, download.URL, now)
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
	alertVideoRequoteCapped(ctx, job, billingResult)
	alertVideoReferenceUnpriced(ctx, job, billingResult)
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
	if err := model.RecordMediaJobProviderSuccess(job, now); err != nil {
		common.SysLog(fmt.Sprintf("video job %s: failed to record delivered outcome: %v", job.PublicID, err))
	}
	return nil
}

// alertVideoRequoteCapped tells the operator when a provider's report priced
// a job far above its hold. The job settled at the hold, so the customer was
// not overcharged, but the report is either a unit mix-up on the channel or
// a real cost the lane's price does not cover.
func alertVideoRequoteCapped(ctx context.Context, job model.MediaJob, result model.MediaJobBillingResult) {
	if !result.RequoteCapped {
		return
	}
	hold := fmt.Sprintf("$%.6f", result.CustomerCharge.Dollars())
	quoted := fmt.Sprintf("$%.6f", result.RequoteQuotedMicrousd.Dollars())
	// Settlement already logged the job; this is the operator's mail.
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	mail := common.Mail{
		Severity:  "ALERT",
		Source:    "video-billing",
		Title:     "视频结算超出预扣上限",
		Preheader: fmt.Sprintf("%s 按上游回报应收 %s，已按预扣 %s 结算", job.ModelName, quoted, hold),
		Lead:      "上游回报的计费数据把这条视频任务算成了预扣的两倍以上。为了不多收用户的钱，这次按预扣结算。请检查这个渠道回报的 cost 或 token 单位是否正确，以及这条通道的售价能否覆盖上游成本。",
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: "任务", Value: job.PublicID, Mono: true},
				{Label: "模型", Value: job.ModelName, Mono: true},
				{Label: "预扣（已结算）", Value: hold, Mono: true},
				{Label: "按回报重算", Value: quoted, Mono: true},
			}},
		},
	}
	// One mail per model every 6 hours: a misreporting channel repeats on
	// every job, and the settlement log keeps each one.
	if err := sendLeasedOpsMail(ctx, "video-requote-capped:"+job.ModelName, 6*time.Hour, "视频结算超出预扣上限", mail); err != nil {
		common.SysLog("video requote cap alert for " + job.PublicID + ": " + err.Error())
	}
}

// alertVideoReferenceUnpriced tells the operator when a job held for
// reference video but the provider reported neither a cost nor tokens, so it
// settled on the output alone and the reference input went unbilled.
func alertVideoReferenceUnpriced(ctx context.Context, job model.MediaJob, result model.MediaJobBillingResult) {
	if !result.ReferenceUnpriced || !common.RedisEnabled || common.RDB == nil {
		return
	}
	charged := fmt.Sprintf("$%.6f", result.CustomerCharge.Dollars())
	mail := common.Mail{
		Severity:  "ALERT",
		Source:    "video-billing",
		Title:     "参考视频没有计费回报",
		Preheader: fmt.Sprintf("%s 的任务带参考视频，上游没回报成本或 token，已按生成时长 %s 结算", job.ModelName, charged),
		Lead:      "这条视频任务带了参考视频，预扣时按模型允许的最长参考时长冻结了余额。上游完成后既没回报 cost 也没回报 token，无法知道参考视频的实际计费，所以只按生成时长向用户收费，参考视频部分没有收。请把这个模型的售价配成跟随上游成本（provider_cost_multiplier），或确认渠道会回报 cost。",
		Blocks: []common.MailBlock{
			{Facts: []common.MailFact{
				{Label: "任务", Value: job.PublicID, Mono: true},
				{Label: "模型", Value: job.ModelName, Mono: true},
				{Label: "已结算", Value: charged, Mono: true},
			}},
		},
	}
	if err := sendLeasedOpsMail(ctx, "video-reference-unpriced:"+job.ModelName, 6*time.Hour, "参考视频没有计费回报", mail); err != nil {
		common.SysLog("video reference alert for " + job.PublicID + ": " + err.Error())
	}
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

func doVideoProviderDownload(ctx context.Context, download *video.DownloadRequest, channel *model.Channel) (*http.Response, []byte, error) {
	if download == nil {
		return nil, nil, fmt.Errorf("video download request is missing")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, download.URL, nil)
	if err != nil {
		return nil, nil, err
	}
	if download.Authenticated {
		for name, values := range download.Headers {
			for _, value := range values {
				request.Header.Add(name, value)
			}
		}
	}
	client, err := videoProviderHTTPClient(channel, download.URL, download.Authenticated)
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
	maxSize := constant.MaxVideoDownloadMB
	if maxSize <= 0 {
		maxSize = 200
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

// videoProviderHTTPClient is the channel-aware client for one upstream call;
// the policy (proxy, SSRF filter, redirect origin) lives in service.
func videoProviderHTTPClient(channel *model.Channel, requestURL string, authenticated bool) (*http.Client, error) {
	if channel == nil {
		return nil, fmt.Errorf("video channel is missing")
	}
	return service.VideoUpstreamClient(channel.GetSetting().Proxy, requestURL, authenticated)
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
