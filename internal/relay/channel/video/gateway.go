package video

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
)

// gatewayDriver speaks the OpenAI video object: POST /v1/videos, GET
// /v1/videos/{id}, GET /v1/videos/{id}/content. OpenRouter, LiteLLM proxies
// and other gateways expose it, with small extensions this driver passes on.
type gatewayDriver struct{}

func (gatewayDriver) Family() string { return dto.VideoFamilyGateway }

func (gatewayDriver) BuildSubmit(ch Channel, req dto.VideoRequest, upstreamModel string) (*SubmitRequest, error) {
	body := dto.OpenAIVideoRequest{
		Model:          upstreamModel,
		Prompt:         req.Prompt,
		Seconds:        req.Seconds,
		Size:           req.Size,
		AspectRatio:    req.AspectRatio,
		Resolution:     req.Resolution,
		Quality:        req.Quality,
		ResponseFormat: req.ResponseFormat,
		User:           req.User,
		FrameImages:    req.FrameImages,
		InputRefs:      req.InputRefs,
		GenerateAudio:  req.GenerateAudio,
		Seed:           req.Seed,
	}
	encoded, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &SubmitRequest{
		Method:  http.MethodPost,
		URL:     joinPath(ch, "/v1/videos"),
		Body:    encoded,
		Headers: jsonHeaders(bearerHeaders(ch)),
	}, nil
}

var gatewayJobIDPaths = []string{"id", "name", "job_id", "task_id", "generation_id", "data.id", "data.task_id", "data.0.id", "data.0.task_id", "operation.name"}
var gatewayArtifactPaths = []string{"url", "video.url", "output.url", "data.url", "urls.0", "unsigned_urls.0", "video_url", "data.video_url", "content.video_url", "response.video.url"}

func (gatewayDriver) ParseSubmit(_ Channel, body []byte) (*SubmitReceipt, error) {
	payload, ok := decodeObject(body)
	if !ok {
		return nil, fmt.Errorf("video provider response is not a JSON object")
	}
	receipt := &SubmitReceipt{
		UpstreamID:  firstString(payload, gatewayJobIDPaths...),
		ArtifactURL: firstString(payload, gatewayArtifactPaths...),
	}
	if receipt.UpstreamID == "" && receipt.ArtifactURL == "" {
		return nil, fmt.Errorf("video provider response did not include a job id")
	}
	return receipt, nil
}

func (gatewayDriver) BuildStatus(ch Channel, job Job) (*StatusRequest, error) {
	if strings.TrimSpace(ch.BaseURL) == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	if strings.TrimSpace(job.UpstreamID) == "" {
		return nil, fmt.Errorf("video upstream job id is empty")
	}
	return &StatusRequest{
		URL:     joinPath(ch, "/v1/videos/"+url.PathEscape(strings.TrimSpace(job.UpstreamID))),
		Headers: bearerHeaders(ch),
	}, nil
}

func (gatewayDriver) ParseStatus(_ Channel, body []byte) (*StatusResult, error) {
	payload, ok := decodeObject(body)
	if !ok {
		return nil, fmt.Errorf("decode video status: not a JSON object")
	}
	return parseOpenAIStatus(payload), nil
}

// parseOpenAIStatus reads the OpenAI-shaped job object and the words its
// relatives use for the same states.
func parseOpenAIStatus(payload map[string]any) *StatusResult {
	status := strings.ToLower(firstString(payload, "status", "state", "data.status"))
	artifactURL := firstString(payload, gatewayArtifactPaths...)
	facts := gatewayFacts(payload)
	switch status {
	case "completed", "complete", "succeeded", "success", "done", "ready", "finished":
		return &StatusResult{State: StateSucceeded, ArtifactURL: artifactURL, Facts: facts}
	case "failed", "failure", "error", "rejected":
		return &StatusResult{
			State:        StateFailed,
			ErrorCode:    firstString(payload, "error.code", "code", "data.error.code"),
			ErrorMessage: firstString(payload, "error.message", "message", "detail", "data.error.message"),
		}
	case "cancelled", "canceled", "expired":
		return &StatusResult{
			State:        StateCancelled,
			ErrorCode:    firstString(payload, "error.code", "code", status),
			ErrorMessage: firstString(payload, "error.message", "message", "detail"),
		}
	case "queued", "pending", "submitted":
		return &StatusResult{State: StateQueued}
	default:
		if artifactURL != "" {
			return &StatusResult{State: StateSucceeded, ArtifactURL: artifactURL, Facts: facts}
		}
		return &StatusResult{State: StateRunning}
	}
}

func gatewayFacts(payload map[string]any) *dto.VideoFacts {
	facts := dto.VideoFacts{
		Seconds:    firstInt(payload, "seconds", "duration", "duration_seconds", "data.duration"),
		Resolution: dto.NormalizeVideoResolution(firstString(payload, "resolution", "size", "data.resolution")),
		Ratio:      dto.NormalizeVideoRatio(firstString(payload, "aspect_ratio", "ratio", "size")),
		FPS:        int(firstInt(payload, "fps", "framespersecond")),
	}
	if cost, ok := firstNumber(payload, "usage.cost", "cost", "data.cost"); ok && cost > 0 {
		facts.ProviderCostUSD = &cost
	}
	if audio, ok := firstBool(payload, "generate_audio", "audio"); ok {
		facts.Audio = audio
	}
	if facts.IsZero() {
		return nil
	}
	// Whatever the finished job says about itself (its length, its tier,
	// its cost) is the provider's account of what it delivered.
	facts.Reported = true
	return &facts
}

func (gatewayDriver) BuildDownload(ch Channel, job Job, artifactURL string) (*DownloadRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	if candidate := strings.TrimSpace(artifactURL); candidate != "" {
		resolved, err := resolveURL(base, candidate)
		if err != nil {
			return nil, err
		}
		return authenticatedDownload(ch, resolved, SameOrigin(base, resolved)), nil
	}
	if strings.TrimSpace(job.UpstreamID) == "" {
		return nil, fmt.Errorf("video upstream job id is empty")
	}
	return authenticatedDownload(ch, joinPath(ch, "/v1/videos/"+url.PathEscape(strings.TrimSpace(job.UpstreamID))+"/content"), true), nil
}

func authenticatedDownload(ch Channel, target string, authenticated bool) *DownloadRequest {
	request := &DownloadRequest{URL: target, Authenticated: authenticated, Headers: http.Header{}}
	if authenticated {
		request.Headers = bearerHeaders(ch)
	}
	return request
}

func (gatewayDriver) EstimateFacts(body []byte, caps *dto.VideoCapabilities) dto.VideoFacts {
	payload, _ := decodeObject(body)
	facts := dto.VideoFacts{
		Seconds:    firstInt(payload, "seconds", "duration", "duration_seconds"),
		Resolution: dto.NormalizeVideoResolution(firstString(payload, "resolution", "quality", "size")),
		Ratio:      dto.NormalizeVideoRatio(firstString(payload, "aspect_ratio", "size")),
	}
	if audio, ok := firstBool(payload, "generate_audio"); ok {
		facts.Audio = audio
	}
	return applyCapabilityDefaults(facts, caps)
}

// applyCapabilityDefaults fills the dimensions a request left unsaid with
// the model's defaults, so an estimate never prices zero seconds.
func applyCapabilityDefaults(facts dto.VideoFacts, caps *dto.VideoCapabilities) dto.VideoFacts {
	if caps == nil {
		return facts
	}
	if facts.Seconds <= 0 && caps.DefaultSeconds > 0 {
		facts.Seconds = int64(caps.DefaultSeconds)
	}
	if facts.Resolution == "" && caps.DefaultResolution != "" {
		facts.Resolution = dto.NormalizeVideoResolution(caps.DefaultResolution)
	}
	if facts.Ratio == "" && caps.DefaultRatio != "" {
		facts.Ratio = dto.NormalizeVideoRatio(caps.DefaultRatio)
	}
	if facts.FPS <= 0 && caps.FPS > 0 {
		facts.FPS = caps.FPS
	}
	return facts
}
