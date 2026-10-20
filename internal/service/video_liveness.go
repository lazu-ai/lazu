package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/channel/video"
)

// videoProviderResponseLimit bounds a status answer. Status bodies are small
// JSON; anything larger is not one.
const videoProviderResponseLimit = int64(2 << 20)

// DoVideoProviderRequest sends one authenticated, body-less call to a video
// provider through the channel's proxy and returns the bounded answer. The
// job poller and the liveness check share it, so a route is checked exactly
// the way its jobs are polled.
func DoVideoProviderRequest(ctx context.Context, method string, requestURL string, headers http.Header, proxy string) (*http.Response, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, nil, err
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	client, err := VideoUpstreamClient(proxy, requestURL, true)
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

// CheckVideoLiveness asks a video route's provider about one task, generating
// nothing (see video.ReadLiveness for what each answer means). hint is the
// route's most recent delivered task id, or empty. The second result is
// false when no request was sent: a local configuration gap is neither
// provider evidence nor spend.
func CheckVideoLiveness(ctx context.Context, channel *model.Channel, modelName string, hint string) (video.Liveness, bool) {
	if channel == nil {
		return video.Liveness{Message: "channel unavailable"}, false
	}
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil {
		return video.Liveness{Message: "no enabled channel key: " + keyErr.Error()}, false
	}
	ch := video.Channel{
		Type:           channel.Type,
		BaseURL:        channel.GetBaseURL(),
		Key:            key,
		Settings:       channel.GetOtherSettings(),
		HeaderOverride: channel.GetHeaderOverride(),
	}
	driver, err := video.Select(ch, modelName)
	if err != nil {
		return video.Liveness{Message: "video provider: " + err.Error()}, false
	}
	hint = strings.TrimSpace(hint)
	taskID := hint
	if taskID == "" {
		taskID = video.LivenessSentinelID
	}
	status, err := driver.BuildStatus(ch, video.Job{UpstreamID: taskID, Model: modelName})
	if err != nil {
		return video.Liveness{Message: "build video status read: " + err.Error()}, false
	}
	response, body, err := DoVideoProviderRequest(ctx, http.MethodGet, status.URL, status.Headers, channel.GetSetting().Proxy)
	if err != nil {
		return video.TransportLiveness(err), true
	}
	return video.ReadLiveness(response.StatusCode, body, hint != ""), true
}
