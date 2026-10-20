package controller

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
)

// videoCatalogParameters describes /v1/videos for one model from its
// capability row, so the catalog, the validation and clients agree on what the
// model takes. A model without a row shows the open contract.
func videoCatalogParameters(modelName string) map[string]dto.CatalogParam {
	caps := model.ModelVideoCapabilities(modelName)
	seconds := dto.CatalogParam{Type: "integer", Description: "Requested clip length in seconds."}
	if caps.DefaultSeconds > 0 {
		seconds.Default = caps.DefaultSeconds
	} else {
		seconds.Default = 5
	}
	if len(caps.Durations) > 0 {
		seconds.Enum = intEnum(caps.Durations)
	} else if caps.MinSeconds > 0 || caps.MaxSeconds > 0 {
		seconds.Range = []float64{float64(caps.MinSeconds), float64(caps.MaxSeconds)}
	}
	resolution := dto.CatalogParam{Type: "string", Description: "Resolution tier (480p, 720p, 1080p, 4k); also read from `quality` or a WIDTHxHEIGHT `size`."}
	if caps.DefaultResolution != "" {
		resolution.Default = caps.DefaultResolution
	}
	if len(caps.Resolutions) > 0 {
		resolution.Enum = append([]string(nil), caps.Resolutions...)
	}
	ratio := dto.CatalogParam{Type: "string", Description: "Aspect ratio such as 16:9; also read from a WIDTHxHEIGHT `size`."}
	if caps.DefaultRatio != "" {
		ratio.Default = caps.DefaultRatio
	}
	if len(caps.Ratios) > 0 {
		ratio.Enum = append([]string(nil), caps.Ratios...)
	}
	params := map[string]dto.CatalogParam{
		"model": {
			Type:        "string",
			Description: "Model ID from the catalog.",
			Required:    true,
		},
		"prompt": {
			Type:        "string",
			Description: "Video generation prompt.",
			Required:    true,
		},
		"seconds":      seconds,
		"resolution":   resolution,
		"aspect_ratio": ratio,
		"size": {
			Type:        "string",
			Description: "WIDTHxHEIGHT or an aspect ratio; a convenience for resolution plus aspect_ratio.",
		},
		"generate_audio": {
			Type:        "boolean",
			Description: "Generate synchronized audio when the model can.",
		},
		"seed": {
			Type:        "integer",
			Description: "Random seed; similar, not identical, results.",
		},
		"artifact_file_id": {
			Type:        "string",
			Description: "Register an already-produced artifact by Lazu file ID instead of generating.",
		},
		"artifact_url": {
			Type:        "string",
			Description: "Register an already-produced artifact by URL instead of generating.",
		},
	}
	inputs := caps.Inputs
	if len(inputs) == 0 || containsInput(inputs, dto.VideoInputFirstFrame) || containsInput(inputs, dto.VideoInputLastFrame) {
		frameTypes := []string{}
		if len(inputs) == 0 || containsInput(inputs, dto.VideoInputFirstFrame) {
			frameTypes = append(frameTypes, dto.VideoFrameFirst)
		}
		if len(inputs) == 0 || containsInput(inputs, dto.VideoInputLastFrame) {
			frameTypes = append(frameTypes, dto.VideoFrameLast)
		}
		params["frame_images"] = dto.CatalogParam{
			Type:        "array",
			Description: fmt.Sprintf("Image-to-video frames: objects with `url` and `frame_type` (%s).", strings.Join(frameTypes, ", ")),
		}
	}
	referenceKinds := []string{}
	for kind, label := range map[string]string{
		dto.VideoInputReferenceImage: dto.VideoReferenceImage,
		dto.VideoInputReferenceVideo: dto.VideoReferenceVideo,
		dto.VideoInputReferenceAudio: dto.VideoReferenceAudio,
	} {
		if len(inputs) == 0 || containsInput(inputs, kind) {
			referenceKinds = append(referenceKinds, label)
		}
	}
	if len(referenceKinds) > 0 {
		description := fmt.Sprintf("Reference material: objects with `url` and `type` (%s).", strings.Join(sortedStrings(referenceKinds), ", "))
		if caps.MaxReferences > 0 {
			description += fmt.Sprintf(" At most %d.", caps.MaxReferences)
		}
		params["input_references"] = dto.CatalogParam{Type: "array", Description: description}
	}
	if caps.Audio != nil && !*caps.Audio {
		delete(params, "generate_audio")
	}
	if len(caps.Passthrough) > 0 {
		params["provider_options"] = dto.CatalogParam{
			Type:        "object",
			Description: "Provider-specific fields passed through unchanged: " + strings.Join(caps.Passthrough, ", ") + ".",
		}
	}
	return params
}

func containsInput(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), wanted) {
			return true
		}
	}
	return false
}

func intEnum(values []int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strconv.Itoa(value))
	}
	return out
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// videoCatalogExample is what the catalog shows for a video model: the
// whole asynchronous flow (submit, poll, download), since a submission
// returns a job and not the clip.
func videoCatalogExample(baseURL string, path string, modelName string) string {
	caps := model.ModelVideoCapabilities(modelName)
	seconds := caps.DefaultSeconds
	if seconds <= 0 {
		seconds = 5
	}
	resolution := caps.DefaultResolution
	if resolution == "" {
		resolution = "720p"
	}
	ratio := caps.DefaultRatio
	if ratio == "" {
		ratio = "16:9"
	}
	return fmt.Sprintf(
		"# 1. Submit: HTTP 202 returns a job id (mjob_...) and the amount held\n"+
			"curl %[1]s%[2]s -H 'Authorization: Bearer YOUR_API_KEY' -H 'Content-Type: application/json' -d '{\"model\":\"%[3]s\",\"prompt\":\"A cinematic product reveal on a clean studio table\",\"seconds\":%[4]d,\"resolution\":\"%[5]s\",\"aspect_ratio\":\"%[6]s\"}'\n"+
			"# 2. Wait: answers when the status changes or the job finishes (60 s at most), with progress and eta_seconds;\n"+
			"#    repeat until status is succeeded or failed. GET .../JOB_ID/events streams the same live.\n"+
			"#    When status is succeeded, artifact.url is a download link valid for an hour.\n"+
			"curl '%[1]s%[2]s/JOB_ID?wait=60' -H 'Authorization: Bearer YOUR_API_KEY'\n"+
			"# 3. Download the video\n"+
			"curl --fail %[1]s%[2]s/JOB_ID/content -H 'Authorization: Bearer YOUR_API_KEY' -o video.mp4",
		baseURL,
		path,
		modelName,
		seconds,
		resolution,
		ratio,
	)
}
