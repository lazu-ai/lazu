package video

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
)

// DefaultCapabilities returns what the gateway knows about a model family
// before an operator writes a capability row: enough for a client's pickers and
// for an estimate, never a hard restriction beyond the vendor's published
// limits. Unknown models get an empty row (no restriction).
func DefaultCapabilities(modelName string) *dto.VideoCapabilities {
	name := strings.ToLower(strings.TrimSpace(modelName))
	audio := true
	switch {
	case strings.Contains(name, "seedance-2.5"), strings.Contains(name, "seedance-2-5"), strings.Contains(name, "seedance_2_5"):
		return &dto.VideoCapabilities{
			MinSeconds:               4,
			MaxSeconds:               30,
			Resolutions:              []string{"480p", "720p", "1080p"},
			Ratios:                   []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive"},
			Inputs:                   []string{dto.VideoInputText, dto.VideoInputFirstFrame, dto.VideoInputLastFrame, dto.VideoInputReferenceImage, dto.VideoInputReferenceVideo, dto.VideoInputReferenceAudio},
			MaxReferences:            30,
			MaxReferenceVideoSeconds: 30,
			Audio:                    &audio,
			Passthrough:              []string{"camera_fixed", "watermark", "return_last_frame", "frames", "service_tier", "callback_url", "draft", "omni_reference_task_type"},
			DefaultSeconds:           5,
			DefaultResolution:        "720p",
			DefaultRatio:             "16:9",
			FPS:                      arkDefaultFPS,
		}
	case strings.Contains(name, "seedance-2"), strings.Contains(name, "seedance_2"):
		return &dto.VideoCapabilities{
			MinSeconds:               2,
			MaxSeconds:               15,
			Resolutions:              []string{"480p", "720p", "1080p"},
			Ratios:                   []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive"},
			Inputs:                   []string{dto.VideoInputText, dto.VideoInputFirstFrame, dto.VideoInputLastFrame, dto.VideoInputReferenceImage, dto.VideoInputReferenceVideo, dto.VideoInputReferenceAudio},
			MaxReferences:            9,
			MaxReferenceVideoSeconds: 15,
			Audio:                    &audio,
			Passthrough:              []string{"camera_fixed", "watermark", "return_last_frame", "frames", "service_tier", "callback_url", "draft", "omni_reference_task_type"},
			DefaultSeconds:           5,
			DefaultResolution:        "720p",
			DefaultRatio:             "16:9",
			FPS:                      arkDefaultFPS,
		}
	case strings.Contains(name, "seedance-1-5"), strings.Contains(name, "seedance-1.5"):
		return &dto.VideoCapabilities{
			MinSeconds:        2,
			MaxSeconds:        12,
			Resolutions:       []string{"480p", "720p", "1080p"},
			Ratios:            []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive"},
			Inputs:            []string{dto.VideoInputText, dto.VideoInputFirstFrame, dto.VideoInputLastFrame, dto.VideoInputReferenceImage},
			MaxReferences:     4,
			Audio:             &audio,
			Passthrough:       []string{"camera_fixed", "watermark", "return_last_frame", "frames", "service_tier", "callback_url", "draft"},
			DefaultSeconds:    5,
			DefaultResolution: "720p",
			DefaultRatio:      "16:9",
			FPS:               arkDefaultFPS,
		}
	case strings.Contains(name, "seedance"):
		silent := false
		return &dto.VideoCapabilities{
			Durations:         []int{5, 10},
			Resolutions:       []string{"480p", "720p", "1080p"},
			Ratios:            []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive"},
			Inputs:            []string{dto.VideoInputText, dto.VideoInputFirstFrame, dto.VideoInputLastFrame, dto.VideoInputReferenceImage},
			MaxReferences:     4,
			Audio:             &silent,
			Passthrough:       []string{"camera_fixed", "watermark", "return_last_frame", "callback_url"},
			DefaultSeconds:    5,
			DefaultResolution: "720p",
			DefaultRatio:      "16:9",
			FPS:               arkDefaultFPS,
		}
	case strings.HasPrefix(name, "veo"):
		return &dto.VideoCapabilities{
			Durations:         []int{4, 6, 8},
			Resolutions:       []string{"720p", "1080p"},
			Ratios:            []string{"16:9", "9:16"},
			Inputs:            []string{dto.VideoInputText, dto.VideoInputFirstFrame, dto.VideoInputLastFrame},
			Audio:             &audio,
			DefaultSeconds:    8,
			DefaultResolution: "720p",
			DefaultRatio:      "16:9",
			FPS:               24,
		}
	default:
		return &dto.VideoCapabilities{}
	}
}

// DefaultMaxReferenceVideoSeconds is the reference-video hold when the
// model's row names no limit: the longest any supported provider accepts.
const DefaultMaxReferenceVideoSeconds = 30

// WithReferenceVideoHold adds, for a request with reference video, the
// longest reference the model accepts to the estimate a submission holds
// on: providers bill input video by the second (or, on Ark, as tokens of
// input plus output). Settlement prices the provider's own report.
func WithReferenceVideoHold(facts dto.VideoFacts, req dto.VideoRequest, caps *dto.VideoCapabilities) dto.VideoFacts {
	if !req.HasReferenceVideo() {
		return facts
	}
	seconds := DefaultMaxReferenceVideoSeconds
	if caps != nil && caps.MaxReferenceVideoSeconds > 0 {
		seconds = caps.MaxReferenceVideoSeconds
	}
	facts.ReferenceSeconds = int64(seconds)
	if facts.OutputTokens > 0 {
		facts.OutputTokens = ArkVideoTokens(facts.Resolution, facts.Ratio, facts.FPS, facts.Seconds+facts.ReferenceSeconds)
	}
	return facts
}

// AdaptiveRatioForReferences sets aspect_ratio to adaptive for a request
// that sends reference video or first/last frames and names no ratio, when
// the model takes adaptive: providers treat such tasks as editing,
// extending or framing the input, and then accept only the input's own
// ratio. An explicit ratio or size is left alone.
func AdaptiveRatioForReferences(req *dto.VideoRequest, caps *dto.VideoCapabilities) {
	if req == nil || caps == nil || strings.TrimSpace(req.AspectRatio) != "" || strings.TrimSpace(req.Size) != "" {
		return
	}
	if !req.HasReferenceVideo() && len(req.FrameImages) == 0 {
		return
	}
	for _, ratio := range caps.Ratios {
		if strings.EqualFold(strings.TrimSpace(ratio), "adaptive") {
			req.AspectRatio = "adaptive"
			return
		}
	}
}
