package video

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
)

// arkDriver speaks the Volcengine Ark / BytePlus ModelArk task API that
// serves Seedance: POST {base}/contents/generations/tasks and GET
// {base}/contents/generations/tasks/{id}, where base ends in /api/v3 on the
// vendor and on gateways that mount it under a prefix of their own.
type arkDriver struct{}

const (
	arkTasksPath = "/contents/generations/tasks"
	// arkDefaultFPS is what Seedance renders at; Ark bills
	// width × height × fps × seconds / 1024 tokens.
	arkDefaultFPS = 24
)

func (arkDriver) Family() string { return dto.VideoFamilyArk }

type arkContentItem struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *arkMediaURL `json:"image_url,omitempty"`
	VideoURL *arkMediaURL `json:"video_url,omitempty"`
	AudioURL *arkMediaURL `json:"audio_url,omitempty"`
	Role     string       `json:"role,omitempty"`
}

type arkMediaURL struct {
	URL string `json:"url"`
}

type arkSubmitBody struct {
	Model           string           `json:"model"`
	Content         []arkContentItem `json:"content"`
	Ratio           string           `json:"ratio,omitempty"`
	Duration        int              `json:"duration,omitempty"`
	Resolution      string           `json:"resolution,omitempty"`
	GenerateAudio   *bool            `json:"generate_audio,omitempty"`
	Seed            *int64           `json:"seed,omitempty"`
	ReturnLastFrame *bool            `json:"return_last_frame,omitempty"`
}

func (arkDriver) BuildSubmit(ch Channel, req dto.VideoRequest, upstreamModel string) (*SubmitRequest, error) {
	if strings.TrimSpace(ch.BaseURL) == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	content := []arkContentItem{{Type: "text", Text: req.Prompt}}
	for _, frame := range req.FrameImages {
		if strings.TrimSpace(frame.URL) == "" {
			continue
		}
		role := "first_frame"
		if strings.EqualFold(strings.TrimSpace(frame.FrameType), dto.VideoFrameLast) {
			role = "last_frame"
		}
		content = append(content, arkContentItem{Type: "image_url", ImageURL: &arkMediaURL{URL: frame.URL}, Role: role})
	}
	for _, reference := range req.InputRefs {
		if strings.TrimSpace(reference.URL) == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(reference.Type)) {
		case dto.VideoReferenceVideo:
			content = append(content, arkContentItem{Type: "video_url", VideoURL: &arkMediaURL{URL: reference.URL}, Role: "reference_video"})
		case dto.VideoReferenceAudio:
			content = append(content, arkContentItem{Type: "audio_url", AudioURL: &arkMediaURL{URL: reference.URL}})
		default:
			content = append(content, arkContentItem{Type: "image_url", ImageURL: &arkMediaURL{URL: reference.URL}, Role: "reference_image"})
		}
	}
	body := arkSubmitBody{
		Model:         upstreamModel,
		Content:       content,
		Ratio:         arkRatio(req.EffectiveRatio()),
		Duration:      req.Seconds,
		Resolution:    req.EffectiveResolution(),
		GenerateAudio: req.GenerateAudio,
		Seed:          req.Seed,
	}
	encoded, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return &SubmitRequest{
		Method:  http.MethodPost,
		URL:     joinPath(ch, arkTasksPath),
		Body:    encoded,
		Headers: jsonHeaders(bearerHeaders(ch)),
	}, nil
}

// arkRatio keeps the ratios Ark names and leaves anything else to the model.
func arkRatio(ratio string) string {
	switch ratio {
	case "16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive":
		return ratio
	default:
		return ""
	}
}

func (arkDriver) ParseSubmit(_ Channel, body []byte) (*SubmitReceipt, error) {
	payload, ok := decodeObject(body)
	if !ok {
		return nil, fmt.Errorf("video provider response is not a JSON object")
	}
	id := firstString(payload, "id", "task_id", "data.id")
	if id == "" {
		return nil, fmt.Errorf("video provider response did not include a job id")
	}
	return &SubmitReceipt{UpstreamID: id}, nil
}

func (arkDriver) BuildStatus(ch Channel, job Job) (*StatusRequest, error) {
	if strings.TrimSpace(ch.BaseURL) == "" {
		return nil, fmt.Errorf("%w: video channel base url is empty", ErrConfiguration)
	}
	if strings.TrimSpace(job.UpstreamID) == "" {
		return nil, fmt.Errorf("video upstream job id is empty")
	}
	return &StatusRequest{
		URL:     joinPath(ch, arkTasksPath+"/"+url.PathEscape(strings.TrimSpace(job.UpstreamID))),
		Headers: bearerHeaders(ch),
	}, nil
}

func (arkDriver) ParseStatus(_ Channel, body []byte) (*StatusResult, error) {
	payload, ok := decodeObject(body)
	if !ok {
		return nil, fmt.Errorf("decode video status: not a JSON object")
	}
	status := strings.ToLower(firstString(payload, "status"))
	switch status {
	case "succeeded":
		artifact := firstString(payload, "content.video_url", "content.url")
		if artifact == "" {
			return &StatusResult{State: StateFailed, ErrorCode: "video_artifact_missing", ErrorMessage: "video provider completed without an artifact"}, nil
		}
		return &StatusResult{State: StateSucceeded, ArtifactURL: artifact, Facts: arkFacts(payload)}, nil
	case "failed", "expired":
		return &StatusResult{
			State:        StateFailed,
			ErrorCode:    firstString(payload, "error.code", "code"),
			ErrorMessage: firstString(payload, "error.message", "message"),
		}, nil
	case "cancelled", "canceled":
		return &StatusResult{State: StateCancelled, ErrorCode: "cancelled", ErrorMessage: firstString(payload, "error.message", "message")}, nil
	case "queued":
		return &StatusResult{State: StateQueued}, nil
	default:
		return &StatusResult{State: StateRunning}, nil
	}
}

// arkFacts reads the dimensions Ark bills on: the returned usage is the
// authoritative token count, and duration / resolution / fps are the clip's
// own, which can differ from the request. A result that reports none of them
// has no facts, as in the other families.
func arkFacts(payload map[string]any) *dto.VideoFacts {
	facts := &dto.VideoFacts{
		Seconds:      firstInt(payload, "duration"),
		Resolution:   dto.NormalizeVideoResolution(firstString(payload, "resolution")),
		Ratio:        dto.NormalizeVideoRatio(firstString(payload, "ratio")),
		FPS:          int(firstInt(payload, "framespersecond", "fps")),
		OutputTokens: firstInt(payload, "usage.completion_tokens", "usage.total_tokens"),
	}
	if audio, ok := firstBool(payload, "generate_audio"); ok {
		facts.Audio = audio
	}
	if facts.IsZero() {
		return nil
	}
	facts.Reported = true
	return facts
}

func (arkDriver) BuildDownload(ch Channel, job Job, artifactURL string) (*DownloadRequest, error) {
	candidate := strings.TrimSpace(artifactURL)
	if candidate == "" {
		candidate = strings.TrimSpace(job.ArtifactURL)
	}
	if candidate == "" {
		return nil, fmt.Errorf("video provider returned no artifact url")
	}
	resolved, err := resolveURL(ch.BaseURL, candidate)
	if err != nil {
		return nil, err
	}
	// Ark hands out signed object-storage URLs on another origin; the key
	// never travels there.
	return authenticatedDownload(ch, resolved, SameOrigin(ch.BaseURL, resolved)), nil
}

func (arkDriver) EstimateFacts(body []byte, caps *dto.VideoCapabilities) dto.VideoFacts {
	payload, _ := decodeObject(body)
	facts := dto.VideoFacts{
		Seconds:    firstInt(payload, "duration", "seconds"),
		Resolution: dto.NormalizeVideoResolution(firstString(payload, "resolution")),
		Ratio:      dto.NormalizeVideoRatio(firstString(payload, "ratio")),
		FPS:        arkDefaultFPS,
	}
	if audio, ok := firstBool(payload, "generate_audio"); ok {
		facts.Audio = audio
	} else {
		// Ark generates audio unless told not to.
		facts.Audio = true
	}
	facts = applyCapabilityDefaults(facts, caps)
	if facts.Seconds <= 0 {
		facts.Seconds = 5
	}
	if facts.Resolution == "" {
		facts.Resolution = "720p"
	}
	if facts.Ratio == "" || facts.Ratio == "adaptive" {
		facts.Ratio = "16:9"
	}
	facts.OutputTokens = ArkVideoTokens(facts.Resolution, facts.Ratio, facts.FPS, facts.Seconds)
	return facts
}

// ArkVideoTokens is Ark's published estimate: width × height × fps × seconds
// / 1024, with the frame size taken from the resolution tier and ratio.
func ArkVideoTokens(resolution string, ratio string, fps int, seconds int64) int64 {
	width, height := dto.VideoSizeForResolution(resolution, ratio)
	if width == 0 || height == 0 || seconds <= 0 {
		return 0
	}
	if fps <= 0 {
		fps = arkDefaultFPS
	}
	return int64(width) * int64(height) * int64(fps) * seconds / 1024
}
