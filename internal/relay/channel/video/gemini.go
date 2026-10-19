package video

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
)

// geminiDriver speaks the Gemini API's long-running Veo operations:
// POST {base}/{version}/models/{model}:predictLongRunning, then GET the
// operation by name until done.
type geminiDriver struct{}

func (geminiDriver) Family() string { return dto.VideoFamilyGemini }

func geminiHeaders(ch Channel) http.Header {
	header := http.Header{}
	header.Set("x-goog-api-key", ch.Key)
	applyHeaderOverride(header, ch)
	return header
}

func (geminiDriver) BuildSubmit(ch Channel, req dto.VideoRequest, upstreamModel string) (*SubmitRequest, error) {
	if strings.TrimSpace(ch.BaseURL) == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	aspectRatio := req.EffectiveRatio()
	if aspectRatio == "" || aspectRatio == "adaptive" {
		aspectRatio = "16:9"
	}
	instance := dto.GeminiVideoInstance{Prompt: req.Prompt}
	for _, frame := range req.FrameImages {
		image := geminiImage(frame.URL)
		if image == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(frame.FrameType), dto.VideoFrameLast) {
			instance.LastFrame = image
		} else {
			instance.Image = image
		}
	}
	body := dto.GeminiVideoRequest{
		Instances: []dto.GeminiVideoInstance{instance},
		Parameters: dto.GeminiVideoParameters{
			DurationSeconds: req.Seconds,
			AspectRatio:     aspectRatio,
			SampleCount:     1,
			Resolution:      geminiResolution(req.EffectiveResolution()),
			GenerateAudio:   req.GenerateAudio,
			Seed:            req.Seed,
		},
	}
	encoded, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	version := model_setting.GetGeminiVersionSetting(upstreamModel)
	if strings.TrimSpace(version) == "" {
		version = "v1beta"
	}
	return &SubmitRequest{
		Method:  http.MethodPost,
		URL:     joinPath(ch, "/"+version+"/models/"+upstreamModel+":predictLongRunning"),
		Body:    encoded,
		Headers: jsonHeaders(geminiHeaders(ch)),
	}, nil
}

// geminiImage accepts the reference forms Veo takes: a Cloud Storage URI or
// inline base64 (data URL). A plain https URL is not accepted by Veo and is
// left out rather than sent to fail.
func geminiImage(raw string) *dto.GeminiVideoImage {
	value := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(value, "gs://"):
		return &dto.GeminiVideoImage{GcsURI: value}
	case strings.HasPrefix(value, "data:"):
		meta, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
		if !ok {
			return nil
		}
		mimeType, _, _ := strings.Cut(meta, ";")
		return &dto.GeminiVideoImage{BytesBase64Encoded: data, MimeType: mimeType}
	default:
		return nil
	}
}

func geminiResolution(tier string) string {
	switch tier {
	case "720p", "1080p", "4k":
		return tier
	default:
		return ""
	}
}

func (geminiDriver) ParseSubmit(_ Channel, body []byte) (*SubmitReceipt, error) {
	payload, ok := decodeObject(body)
	if !ok {
		return nil, fmt.Errorf("video provider response is not a JSON object")
	}
	name := firstString(payload, "name", "operation.name", "id")
	if name == "" {
		return nil, fmt.Errorf("video provider response did not include a job id")
	}
	return &SubmitReceipt{UpstreamID: name}, nil
}

func (geminiDriver) BuildStatus(ch Channel, job Job) (*StatusRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	upstreamID := strings.TrimSpace(job.UpstreamID)
	if upstreamID == "" {
		return nil, fmt.Errorf("video upstream job id is empty")
	}
	if parsed, err := url.Parse(upstreamID); err == nil && parsed.IsAbs() {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("unsupported video operation url scheme")
		}
		if !SameOrigin(base, parsed.String()) {
			return nil, fmt.Errorf("%w: Gemini operation url must use the configured provider origin", ErrConfiguration)
		}
		return &StatusRequest{URL: parsed.String(), Headers: geminiHeaders(ch)}, nil
	}
	upstreamID = strings.TrimPrefix(upstreamID, "/")
	upstreamID = strings.TrimPrefix(upstreamID, "v1beta/")
	return &StatusRequest{URL: base + "/v1beta/" + upstreamID, Headers: geminiHeaders(ch)}, nil
}

func (geminiDriver) ParseStatus(_ Channel, body []byte) (*StatusResult, error) {
	payload, ok := decodeObject(body)
	if !ok {
		return nil, fmt.Errorf("decode video status: not a JSON object")
	}
	return parseGeminiStatus(payload), nil
}

func parseGeminiStatus(payload map[string]any) *StatusResult {
	if message := firstString(payload, "error.message", "response.error.message"); message != "" {
		return &StatusResult{
			State:        StateFailed,
			ErrorCode:    firstString(payload, "error.status", "error.code"),
			ErrorMessage: message,
		}
	}
	done, _ := payload["done"].(bool)
	artifactURL := firstString(payload,
		"response.generateVideoResponse.generatedSamples.0.video.uri",
		"response.generateVideoResponse.generatedSamples.0.video.url",
		"response.generatedVideos.0.video.uri",
		"response.generatedVideos.0.video.url",
	)
	if done && artifactURL != "" {
		return &StatusResult{State: StateSucceeded, ArtifactURL: artifactURL}
	}
	if done {
		return &StatusResult{State: StateFailed, ErrorCode: "video_artifact_missing", ErrorMessage: "video provider completed without an artifact"}
	}
	return &StatusResult{State: StateRunning}
}

func (geminiDriver) BuildDownload(ch Channel, job Job, artifactURL string) (*DownloadRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	candidate := strings.TrimSpace(artifactURL)
	if candidate == "" {
		candidate = strings.TrimSpace(job.ArtifactURL)
	}
	if candidate == "" {
		return nil, fmt.Errorf("video provider returned no artifact url")
	}
	resolved, err := resolveURL(base, candidate)
	if err != nil {
		return nil, err
	}
	request := &DownloadRequest{URL: resolved, Authenticated: SameOrigin(base, resolved), Headers: http.Header{}}
	if request.Authenticated {
		request.Headers = geminiHeaders(ch)
	}
	return request, nil
}

func (geminiDriver) EstimateFacts(body []byte, caps *dto.VideoCapabilities) dto.VideoFacts {
	payload, _ := decodeObject(body)
	facts := dto.VideoFacts{
		Seconds:    firstInt(payload, "parameters.durationSeconds", "durationSeconds"),
		Resolution: dto.NormalizeVideoResolution(firstString(payload, "parameters.resolution")),
		Ratio:      dto.NormalizeVideoRatio(firstString(payload, "parameters.aspectRatio")),
	}
	if audio, ok := firstBool(payload, "parameters.generateAudio"); ok {
		facts.Audio = audio
	}
	return applyCapabilityDefaults(facts, caps)
}
