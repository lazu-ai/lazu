package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
)

const maxVideoProviderResponseBytes = int64(2 << 20)

type videoProviderSubmission struct {
	ArtifactURL         string
	ProviderName        string
	UpstreamID          string
	Indeterminate       bool
	IndeterminateReason string
}

type preparedVideoProviderRequest struct {
	Adaptor channel.BaseAdaptor
	Body    []byte
	Info    *relaycommon.RelayInfo
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
	if err := validateVideoProviderPollingConfiguration(info); err != nil {
		return nil, err
	}
	if err := helper.ModelMappedHelper(c, info, &request); err != nil {
		return nil, fmt.Errorf("map video model: %w", err)
	}
	adaptor := relay.GetAdaptor(info.ApiType)
	if adaptor == nil {
		return nil, fmt.Errorf("video provider protocol is unsupported")
	}
	adaptor.Init(info)
	converter, ok := adaptor.(channel.VideoRequestConverter)
	if !ok {
		return nil, fmt.Errorf("video provider %s does not support video generation", adaptor.GetChannelName())
	}
	converted, err := converter.ConvertVideoRequest(c, info, request)
	if err != nil {
		return nil, fmt.Errorf("convert video request: %w", err)
	}
	body, err := common.Marshal(converted)
	if err != nil {
		return nil, fmt.Errorf("encode video request: %w", err)
	}
	body, err = mergeVideoProviderExtraFields(body, request.Extra)
	if err != nil {
		return nil, fmt.Errorf("merge video extension fields: %w", err)
	}
	if len(info.ParamOverride) > 0 {
		body, err = relaycommon.ApplyParamOverride(body, info.ParamOverride)
		if err != nil {
			return nil, fmt.Errorf("apply video parameter override: %w", err)
		}
	}
	return &preparedVideoProviderRequest{
		Adaptor: adaptor,
		Body:    body,
		Info:    info,
	}, nil
}

func validateVideoProviderPollingConfiguration(info *relaycommon.RelayInfo) error {
	if info == nil || info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeCustom {
		return nil
	}
	if strings.TrimSpace(info.ChannelOtherSettings.VideoStatusURLTemplate) == "" {
		return fmt.Errorf("custom video channel requires video_status_url_template before submission")
	}
	return nil
}

func submitVideoProvider(c *gin.Context, prepared *preparedVideoProviderRequest) (*videoProviderSubmission, error) {
	if prepared == nil || prepared.Adaptor == nil || prepared.Info == nil {
		return nil, fmt.Errorf("video provider request is not prepared")
	}
	common.SetContextKey(c, constant.ContextKeyProviderAttempted, true)
	rawResponse, err := prepared.Adaptor.DoRequest(c, prepared.Info, bytes.NewReader(prepared.Body))
	if err != nil {
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Accepted:   videoProviderSubmissionMayHaveBeenDispatched(err),
			Err:        fmt.Errorf("submit video generation: %w", err),
		}
	}
	response, ok := rawResponse.(*http.Response)
	if !ok || response == nil {
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Err:        fmt.Errorf("video provider returned an invalid response"),
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
	upstreamID, artifactURL := parseVideoProviderSubmission(responseBody)
	if upstreamID == "" && artifactURL == "" {
		return nil, &videoProviderSubmitError{
			StatusCode: http.StatusBadGateway,
			Accepted:   true,
			Err:        fmt.Errorf("video provider response did not include a job id"),
		}
	}
	return &videoProviderSubmission{
		ArtifactURL:  artifactURL,
		ProviderName: strings.ToLower(strings.TrimSpace(prepared.Adaptor.GetChannelName())),
		UpstreamID:   upstreamID,
	}, nil
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
	if key == "" || strings.HasPrefix(key, "_") {
		return false
	}
	switch strings.ToLower(key) {
	case "channel_group",
		"channel_id",
		"channel_multi_key_index",
		"final_group",
		"original_model":
		return false
	default:
		return true
	}
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

func parseVideoProviderSubmission(body []byte) (string, string) {
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil {
		return "", ""
	}
	upstreamID := firstStringAtPaths(payload,
		"id",
		"name",
		"job_id",
		"task_id",
		"data.id",
		"data.name",
		"operation.name",
	)
	artifactURL := firstStringAtPaths(payload,
		"url",
		"video.url",
		"output.url",
		"data.url",
		"response.video.url",
	)
	return upstreamID, artifactURL
}

func firstStringAtPaths(payload map[string]any, paths ...string) string {
	for _, candidate := range paths {
		value := any(payload)
		for _, part := range strings.Split(candidate, ".") {
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
		if number, ok := value.(float64); ok &&
			!math.IsNaN(number) && !math.IsInf(number, 0) {
			return strconv.FormatFloat(number, 'f', -1, 64)
		}
	}
	return ""
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
