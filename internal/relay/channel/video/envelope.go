package video

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
)

// envelopeDriver serves aggregators whose video models all share one task
// envelope: the channel's VideoEnvelopeConfig says where to submit and poll
// and which JSON paths carry the id, state, artifact and cost. A preset
// supplies defaults for a known aggregator; the pre-family Custom channel
// (base URL = submit URL, {id} templates) maps onto the OpenAI preset.
type envelopeDriver struct{}

func (envelopeDriver) Family() string { return dto.VideoFamilyEnvelope }

// effectiveEnvelope merges the preset, the explicit configuration and the
// legacy URL templates into one complete envelope.
func effectiveEnvelope(ch Channel) dto.VideoEnvelopeConfig {
	explicit := dto.VideoEnvelopeConfig{}
	if ch.Settings.VideoEnvelope != nil {
		explicit = *ch.Settings.VideoEnvelope
	}
	preset := strings.ToLower(strings.TrimSpace(explicit.Preset))
	if preset == "" {
		preset = dto.VideoEnvelopePresetOpenAI
	}
	out := envelopePreset(preset)
	out.Preset = preset
	if explicit.SubmitURL != "" {
		out.SubmitURL = explicit.SubmitURL
	}
	if explicit.StatusURL != "" {
		out.StatusURL = explicit.StatusURL
	}
	if explicit.DownloadURL != "" {
		out.DownloadURL = explicit.DownloadURL
	}
	if len(explicit.JobIDPaths) > 0 {
		out.JobIDPaths = explicit.JobIDPaths
	}
	if explicit.StatusPath != "" {
		out.StatusPath = explicit.StatusPath
	}
	if len(explicit.StatusMap) > 0 {
		merged := make(map[string]string, len(out.StatusMap)+len(explicit.StatusMap))
		for word, state := range out.StatusMap {
			merged[word] = state
		}
		for word, state := range explicit.StatusMap {
			merged[strings.ToLower(strings.TrimSpace(word))] = strings.ToLower(strings.TrimSpace(state))
		}
		out.StatusMap = merged
	}
	if len(explicit.ArtifactPaths) > 0 {
		out.ArtifactPaths = explicit.ArtifactPaths
	}
	if explicit.ErrorCodePath != "" {
		out.ErrorCodePath = explicit.ErrorCodePath
	}
	if explicit.ErrorMessagePath != "" {
		out.ErrorMessagePath = explicit.ErrorMessagePath
	}
	if explicit.CostPath != "" {
		out.CostPath = explicit.CostPath
	}
	if explicit.SecondsPath != "" {
		out.SecondsPath = explicit.SecondsPath
	}
	if explicit.ResolutionPath != "" {
		out.ResolutionPath = explicit.ResolutionPath
	}
	if explicit.AuthHeader != "" {
		out.AuthHeader = explicit.AuthHeader
	}
	// The Custom channel's own two fields predate families and keep working.
	if out.StatusURL == "" && strings.TrimSpace(ch.Settings.VideoStatusURLTemplate) != "" {
		out.StatusURL = strings.TrimSpace(ch.Settings.VideoStatusURLTemplate)
	}
	if out.DownloadURL == "" && strings.TrimSpace(ch.Settings.VideoContentURLTemplate) != "" {
		out.DownloadURL = strings.TrimSpace(ch.Settings.VideoContentURLTemplate)
	}
	return out
}

var openAIStatusWords = map[string]string{
	"queued": StateQueued, "pending": StateQueued, "submitted": StateQueued,
	"in_progress": StateRunning, "processing": StateRunning, "running": StateRunning, "generating": StateRunning,
	"completed": StateSucceeded, "complete": StateSucceeded, "succeeded": StateSucceeded, "success": StateSucceeded, "succeed": StateSucceeded, "done": StateSucceeded, "finished": StateSucceeded, "ready": StateSucceeded,
	"failed": StateFailed, "failure": StateFailed, "error": StateFailed, "rejected": StateFailed,
	"cancelled": StateCancelled, "canceled": StateCancelled, "expired": StateCancelled,
}

func envelopePreset(name string) dto.VideoEnvelopeConfig {
	switch name {
	case dto.VideoEnvelopePresetTasks:
		return dto.VideoEnvelopeConfig{
			SubmitURL:        "/v1/videos/generations",
			StatusURL:        "/v1/tasks/{id}",
			JobIDPaths:       []string{"data.0.task_id", "data.task_id", "task_id", "data.0.id", "data.id", "id"},
			StatusPath:       "data.status",
			StatusMap:        copyStatusWords(),
			ArtifactPaths:    []string{"data.result.videos.0.url.0", "data.result.videos.0.url", "data.result.video_url", "data.result.url", "data.video_url", "data.url"},
			ErrorCodePath:    "data.error.code",
			ErrorMessagePath: "data.error.message",
			CostPath:         "data.cost",
			SecondsPath:      "data.result.duration",
			ResolutionPath:   "data.result.resolution",
		}
	default:
		return dto.VideoEnvelopeConfig{
			// SubmitURL empty: the Custom channel's base URL is the submit URL.
			JobIDPaths:       gatewayJobIDPaths,
			StatusPath:       "status",
			StatusMap:        copyStatusWords(),
			ArtifactPaths:    gatewayArtifactPaths,
			ErrorCodePath:    "error.code",
			ErrorMessagePath: "error.message",
			CostPath:         "usage.cost",
			SecondsPath:      "seconds",
			ResolutionPath:   "resolution",
		}
	}
}

func copyStatusWords() map[string]string {
	out := make(map[string]string, len(openAIStatusWords))
	for word, state := range openAIStatusWords {
		out[word] = state
	}
	return out
}

func envelopeHeaders(ch Channel, env dto.VideoEnvelopeConfig) http.Header {
	header := http.Header{}
	auth := strings.TrimSpace(env.AuthHeader)
	if auth == "" {
		header.Set("Authorization", "Bearer "+ch.Key)
	} else if name, value, ok := strings.Cut(auth, ":"); ok {
		header.Set(strings.TrimSpace(name), strings.ReplaceAll(strings.TrimSpace(value), "{api_key}", ch.Key))
	}
	applyHeaderOverride(header, ch)
	return header
}

type tasksSubmitBody struct {
	Model          string           `json:"model"`
	Prompt         string           `json:"prompt,omitempty"`
	Duration       int              `json:"duration,omitempty"`
	Size           string           `json:"size,omitempty"`
	Resolution     string           `json:"resolution,omitempty"`
	GenerateAudio  *bool            `json:"generate_audio,omitempty"`
	Seed           *int64           `json:"seed,omitempty"`
	ImageURLs      []string         `json:"image_urls,omitempty"`
	ImageWithRoles []tasksImageRole `json:"image_with_roles,omitempty"`
	VideoURLs      []string         `json:"video_urls,omitempty"`
	AudioURLs      []string         `json:"audio_urls,omitempty"`
}

type tasksImageRole struct {
	URL  string `json:"url"`
	Role string `json:"role"`
}

func (envelopeDriver) BuildSubmit(ch Channel, req dto.VideoRequest, upstreamModel string) (*SubmitRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	env := effectiveEnvelope(ch)
	// A Custom channel's base URL may carry {model}, as chat relays allow.
	target := strings.ReplaceAll(base, "{model}", upstreamModel)
	if strings.TrimSpace(env.SubmitURL) != "" {
		resolved, err := resolveURL(base, strings.ReplaceAll(env.SubmitURL, "{model}", upstreamModel))
		if err != nil {
			return nil, err
		}
		target = resolved
	}
	var payload any
	switch env.Preset {
	case dto.VideoEnvelopePresetTasks:
		body := tasksSubmitBody{
			Model:         upstreamModel,
			Prompt:        req.Prompt,
			Duration:      req.Seconds,
			Size:          req.EffectiveRatio(),
			Resolution:    req.EffectiveResolution(),
			GenerateAudio: req.GenerateAudio,
			Seed:          req.Seed,
		}
		for _, frame := range req.FrameImages {
			role := "first_frame"
			if strings.EqualFold(strings.TrimSpace(frame.FrameType), dto.VideoFrameLast) {
				role = "last_frame"
			}
			body.ImageWithRoles = append(body.ImageWithRoles, tasksImageRole{URL: frame.URL, Role: role})
		}
		for _, reference := range req.InputRefs {
			switch strings.ToLower(strings.TrimSpace(reference.Type)) {
			case dto.VideoReferenceVideo:
				body.VideoURLs = append(body.VideoURLs, reference.URL)
			case dto.VideoReferenceAudio:
				body.AudioURLs = append(body.AudioURLs, reference.URL)
			default:
				if len(body.ImageWithRoles) > 0 {
					body.ImageWithRoles = append(body.ImageWithRoles, tasksImageRole{URL: reference.URL, Role: "reference_image"})
				} else {
					body.ImageURLs = append(body.ImageURLs, reference.URL)
				}
			}
		}
		payload = body
	default:
		payload = dto.OpenAIVideoRequest{
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
	}
	encoded, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &SubmitRequest{
		Method:  http.MethodPost,
		URL:     target,
		Body:    encoded,
		Headers: jsonHeaders(envelopeHeaders(ch, env)),
	}, nil
}

func (envelopeDriver) ParseSubmit(ch Channel, body []byte) (*SubmitReceipt, error) {
	env := effectiveEnvelope(ch)
	payload, ok := decodeObject(body)
	if !ok {
		return nil, fmt.Errorf("video provider response is not a JSON object")
	}
	receipt := &SubmitReceipt{
		UpstreamID:  firstString(payload, env.JobIDPaths...),
		ArtifactURL: firstString(payload, env.ArtifactPaths...),
	}
	if receipt.UpstreamID == "" && receipt.ArtifactURL == "" {
		return nil, fmt.Errorf("video provider response did not include a job id")
	}
	return receipt, nil
}

func (envelopeDriver) BuildStatus(ch Channel, job Job) (*StatusRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	if strings.TrimSpace(job.UpstreamID) == "" {
		return nil, fmt.Errorf("video upstream job id is empty")
	}
	env := effectiveEnvelope(ch)
	if strings.TrimSpace(env.StatusURL) == "" {
		return nil, fmt.Errorf("%w: custom video channel requires video_status_url_template", ErrConfiguration)
	}
	target, err := renderTemplate(base, env.StatusURL, job.UpstreamID)
	if err != nil {
		return nil, err
	}
	return &StatusRequest{URL: target, Headers: envelopeHeaders(ch, env)}, nil
}

func (envelopeDriver) ParseStatus(ch Channel, body []byte) (*StatusResult, error) {
	env := effectiveEnvelope(ch)
	payload, ok := decodeObject(body)
	if !ok {
		return nil, fmt.Errorf("decode video status: not a JSON object")
	}
	word := strings.ToLower(firstString(payload, env.StatusPath, "status", "state"))
	state, known := env.StatusMap[word]
	artifactURL := firstString(payload, env.ArtifactPaths...)
	facts := envelopeFacts(payload, env)
	errorCode := firstString(payload, env.ErrorCodePath, "error.code")
	errorMessage := firstString(payload, env.ErrorMessagePath, "error.message", "message")
	switch {
	case known && state == StateSucceeded:
		return &StatusResult{State: StateSucceeded, ArtifactURL: artifactURL, Facts: facts}, nil
	case known && state == StateFailed:
		return &StatusResult{State: StateFailed, ErrorCode: errorCode, ErrorMessage: errorMessage}, nil
	case known && state == StateCancelled:
		if errorCode == "" {
			errorCode = word
		}
		return &StatusResult{State: StateCancelled, ErrorCode: errorCode, ErrorMessage: errorMessage}, nil
	case known && state == StateQueued:
		return &StatusResult{State: StateQueued}, nil
	case artifactURL != "":
		return &StatusResult{State: StateSucceeded, ArtifactURL: artifactURL, Facts: facts}, nil
	default:
		return &StatusResult{State: StateRunning}, nil
	}
}

func envelopeFacts(payload map[string]any, env dto.VideoEnvelopeConfig) *dto.VideoFacts {
	facts := dto.VideoFacts{}
	if env.SecondsPath != "" {
		facts.Seconds = firstInt(payload, env.SecondsPath)
	}
	if env.ResolutionPath != "" {
		facts.Resolution = dto.NormalizeVideoResolution(firstString(payload, env.ResolutionPath))
	}
	if env.CostPath != "" {
		if cost, ok := firstNumber(payload, env.CostPath); ok && cost > 0 {
			facts.ProviderCostUSD = &cost
		}
	}
	if facts.IsZero() {
		return nil
	}
	facts.Reported = true
	return &facts
}

func (envelopeDriver) BuildDownload(ch Channel, job Job, artifactURL string) (*DownloadRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	env := effectiveEnvelope(ch)
	if candidate := strings.TrimSpace(artifactURL); candidate != "" {
		resolved, err := resolveURL(base, candidate)
		if err != nil {
			return nil, err
		}
		request := &DownloadRequest{URL: resolved, Authenticated: SameOrigin(base, resolved), Headers: http.Header{}}
		if request.Authenticated {
			request.Headers = envelopeHeaders(ch, env)
		}
		return request, nil
	}
	if strings.TrimSpace(env.DownloadURL) == "" {
		return nil, fmt.Errorf("%w: custom video channel requires video_content_url_template when no artifact url is returned", ErrConfiguration)
	}
	resolved, err := renderTemplate(base, env.DownloadURL, job.UpstreamID)
	if err != nil {
		return nil, err
	}
	request := &DownloadRequest{URL: resolved, Authenticated: SameOrigin(base, resolved), Headers: http.Header{}}
	if request.Authenticated {
		request.Headers = envelopeHeaders(ch, env)
	}
	return request, nil
}

func (envelopeDriver) EstimateFacts(body []byte, caps *dto.VideoCapabilities) dto.VideoFacts {
	payload, _ := decodeObject(body)
	facts := dto.VideoFacts{
		Seconds:    firstInt(payload, "seconds", "duration", "duration_seconds"),
		Resolution: dto.NormalizeVideoResolution(firstString(payload, "resolution", "quality")),
		Ratio:      dto.NormalizeVideoRatio(firstString(payload, "aspect_ratio", "size", "ratio")),
	}
	if audio, ok := firstBool(payload, "generate_audio"); ok {
		facts.Audio = audio
	}
	return applyCapabilityDefaults(facts, caps)
}
