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
	case strings.Contains(name, "seedance-2"), strings.Contains(name, "seedance_2"):
		return &dto.VideoCapabilities{
			MinSeconds:        2,
			MaxSeconds:        15,
			Resolutions:       []string{"480p", "720p", "1080p"},
			Ratios:            []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive"},
			Inputs:            []string{dto.VideoInputText, dto.VideoInputFirstFrame, dto.VideoInputLastFrame, dto.VideoInputReferenceImage, dto.VideoInputReferenceVideo, dto.VideoInputReferenceAudio},
			MaxReferences:     9,
			Audio:             &audio,
			Passthrough:       []string{"camera_fixed", "watermark", "return_last_frame", "frames", "service_tier", "callback_url", "draft", "omni_reference_task_type"},
			DefaultSeconds:    5,
			DefaultResolution: "720p",
			DefaultRatio:      "16:9",
			FPS:               arkDefaultFPS,
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
