package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/channel/video"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/service"
)

const maxVideoProviderResponseBytes = int64(2 << 20)

type videoProviderSubmission struct {
	ArtifactURL         string
	ProviderName        string
	UpstreamID          string
	Indeterminate       bool
	IndeterminateReason string
}

// preparedVideoProviderRequest is one submission ready to send: the family
// driver that built it, the channel it goes to, the final body (extension
// fields and the channel's parameter override applied) and the billing
// estimate read off that body.
type preparedVideoProviderRequest struct {
	Driver  video.Driver
	Channel video.Channel
	Submit  *video.SubmitRequest
	Body    []byte
	Info    *relaycommon.RelayInfo
	Facts   dto.VideoFacts
	Proxy   string
}

type videoProviderSubmitError struct {
	StatusCode int
	RetrySafe  bool
	Accepted   bool
	Err        error
}

func (e *videoProviderSubmitError) Error() string {
	if e == nil || e.Err == nil {
		return "video provider submission failed"
	}
	return e.Err.Error()
}

func (e *videoProviderSubmitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func prepareVideoProviderRequest(c *gin.Context, request dto.VideoRequest) (*preparedVideoProviderRequest, error) {
	info := relaycommon.GenRelayInfoOpenAI(c, &request)
	info.InitChannelMeta(c)
	originalModel := request.Model
	if err := helper.ModelMappedHelper(c, info, &request); err != nil {
		return nil, fmt.Errorf("map video model: %w", err)
	}
	upstreamModel := firstNonEmptyMediaJobValue(info.UpstreamModelName, request.Model, originalModel)
	ch := videoChannelFromRelayInfo(info)
	driver, err := video.Select(ch, originalModel, upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("video provider: %w", err)
	}
	if err := validateVideoProviderPollingConfiguration(driver, ch); err != nil {
		return nil, err
	}
	submit, err := driver.BuildSubmit(ch, request, upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("convert video request: %w", err)
	}
	body, err := mergeVideoProviderExtraFields(submit.Body, request.Extra)
	if err != nil {
		return nil, fmt.Errorf("merge video extension fields: %w", err)
	}
	if len(info.ParamOverride) > 0 {
		body, err = relaycommon.ApplyParamOverride(body, info.ParamOverride)
		if err != nil {
			return nil, fmt.Errorf("apply video parameter override: %w", err)
		}
	}
	submit.Body = body
	proxy := ""
	if info.ChannelMeta != nil {
		proxy = info.ChannelSetting.Proxy
	}
	return &preparedVideoProviderRequest{
		Driver:  driver,
		Channel: ch,
		Submit:  submit,
		Body:    body,
		Info:    info,
		Facts:   driver.EstimateFacts(body, model.ModelVideoCapabilities(originalModel)),
		Proxy:   proxy,
	}, nil
}

func videoChannelFromRelayInfo(info *relaycommon.RelayInfo) video.Channel {
	if info == nil || info.ChannelMeta == nil {
		return video.Channel{}
	}
	return video.Channel{
		Type:           info.ChannelType,
		BaseURL:        info.ChannelBaseUrl,
		Key:            info.ApiKey,
		Settings:       info.ChannelOtherSettings,
		HeaderOverride: info.HeadersOverride,
	}
}

// validateVideoProviderPollingConfiguration refuses to submit a job the
// worker could never poll: a Custom channel without its status template
// would otherwise park every submission as indeterminate.
func validateVideoProviderPollingConfiguration(driver video.Driver, ch video.Channel) error {
	if driver == nil {
		return nil
	}
	_, err := driver.BuildStatus(ch, video.Job{UpstreamID: "pending"})
	if errors.Is(err, video.ErrConfiguration) {
		return err
	}
	return nil
}

func submitVideoProvider(c *gin.Context, prepared *preparedVideoProviderRequest) (*videoProviderSubmission, error) {
	if prepared == nil || prepared.Driver == nil || prepared.Submit == nil {
		return nil, fmt.Errorf("video provider request is not prepared")
	}
	common.SetContextKey(c, constant.ContextKeyProviderAttempted, true)
	client, err := service.VideoUpstreamClient(prepared.Proxy, prepared.Submit.URL, true)
	if err != nil {
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Err:        fmt.Errorf("prepare video provider client: %w", err),
		}
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	method := prepared.Submit.Method
	if method == "" {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, prepared.Submit.URL, bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Err:        fmt.Errorf("build video provider request: %w", err),
		}
	}
	for name, values := range prepared.Submit.Headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Accepted:   videoProviderSubmissionMayHaveBeenDispatched(err),
			Err:        fmt.Errorf("submit video generation: %w", err),
		}
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxVideoProviderResponseBytes+1))
	if err != nil {
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Accepted:   response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices,
			Err:        fmt.Errorf("read video provider response: %w", err),
		}
	}
	if int64(len(responseBody)) > maxVideoProviderResponseBytes {
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Accepted:   response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices,
			Err:        fmt.Errorf("video provider response is too large"),
		}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &videoProviderSubmitError{
			StatusCode: response.StatusCode,
			RetrySafe:  videoProviderStatusRetrySafe(response.StatusCode),
			Err:        fmt.Errorf("video provider returned status %d: %s", response.StatusCode, compactProviderError(responseBody)),
		}
	}
	receipt, err := prepared.Driver.ParseSubmit(prepared.Channel, responseBody)
	if err != nil {
		// The provider answered 2xx: the job may well be running. Keep it
		// recoverable instead of resubmitting paid work.
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Accepted:   true,
			Err:        err,
		}
	}
	return &videoProviderSubmission{
		ArtifactURL:  receipt.ArtifactURL,
		ProviderName: videoProviderName(prepared.Driver, prepared.Channel),
		UpstreamID:   receipt.UpstreamID,
	}, nil
}

// videoProviderName is the provider recorded on the job. It keeps the values
// the pre-family adaptors wrote ("gemini" for Veo, "openai" for every other
// channel) because ephemeral credentials scope jobs by this string.
func videoProviderName(driver video.Driver, _ video.Channel) string {
	if driver != nil && driver.Family() == dto.VideoFamilyGemini {
		return "gemini"
	}
	return "openai"
}

func videoProviderSubmissionMayHaveBeenDispatched(err error) bool {
	var requestErr *url.Error
	return errors.As(err, &requestErr)
}

func mergeVideoProviderExtraFields(body []byte, extra map[string]json.RawMessage) ([]byte, error) {
	if len(extra) == 0 {
		return body, nil
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	for key, value := range extra {
		key = strings.TrimSpace(key)
		if !isSafeVideoProviderExtraField(key) {
			continue
		}
		if _, exists := fields[key]; exists {
			continue
		}
		if !json.Valid(value) {
			return nil, fmt.Errorf("extension field %q is invalid JSON", key)
		}
		fields[key] = value
	}
	return common.Marshal(fields)
}

func isSafeVideoProviderExtraField(key string) bool {
	return !dto.IsInternalVideoRequestField(key)
}

func videoProviderStatusRetrySafe(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

func videoProviderSubmissionRetrySafe(err error) bool {
	var providerErr *videoProviderSubmitError
	return errors.As(err, &providerErr) && providerErr.RetrySafe
}

func videoProviderSubmissionAccepted(err error) bool {
	var providerErr *videoProviderSubmitError
	return errors.As(err, &providerErr) && providerErr.Accepted
}

func compactProviderError(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 500 {
		text = text[:500]
	}
	if text == "" {
		return "empty response"
	}
	return text
}
