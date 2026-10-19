package dto

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/pkg/billing"
)

type SequentialImageOptions struct {
	MaxImages int `json:"max_images"`
}

func IsSeedream(name string) bool { return strings.HasPrefix(SeedreamName(name), "seedream-") }

func SeedreamName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{"bytedance/", "dola-", "doubao-"} {
		name = strings.TrimPrefix(name, prefix)
	}
	return strings.ReplaceAll(name, ".", "-")
}

func SeedreamSupportsLayers(name string) bool {
	name = SeedreamName(name)
	return strings.HasPrefix(name, "seedream-5-0-pro") || strings.HasPrefix(name, "seedream-5-0-flash")
}

func ImagePixels(size string) (int64, error) {
	parts := strings.Split(size, "x")
	if len(parts) != 2 {
		return 0, fmt.Errorf("size must be widthxheight")
	}
	w, ew := strconv.ParseInt(parts[0], 10, 32)
	h, eh := strconv.ParseInt(parts[1], 10, 32)
	if ew != nil || eh != nil || w <= 0 || h <= 0 || w > 32768 || h > 32768 {
		return 0, fmt.Errorf("invalid image dimensions")
	}
	return w * h, nil
}

func (r ImageRequest) ReferenceImages() ([]string, error) {
	if len(r.Image) == 0 || string(r.Image) == "null" {
		return nil, nil
	}
	var single string
	if json.Unmarshal(r.Image, &single) == nil && single != "" {
		return []string{single}, nil
	}
	var images []string
	if json.Unmarshal(r.Image, &images) != nil {
		return nil, fmt.Errorf("image must be a URL/data URL or an array of them")
	}
	for _, image := range images {
		if strings.TrimSpace(image) == "" {
			return nil, fmt.Errorf("image must not be empty")
		}
	}
	return images, nil
}

// PrepareSeedream validates output bounds before financial admission. It also
// translates OpenAI's n to the native sequential-generation contract.
func (r *ImageRequest) PrepareSeedream() error {
	if !IsSeedream(r.Model) {
		return nil
	}
	refs, err := r.ReferenceImages()
	if err != nil {
		return err
	}
	layers := SeedreamSupportsLayers(r.Model)
	maxRefs := 14
	if layers {
		maxRefs = 10
	}
	if len(refs) > maxRefs {
		return fmt.Errorf("this Seedream model supports at most %d reference images", maxRefs)
	}
	if r.LayerDecomposition {
		if !layers || len(refs) != 1 {
			return fmt.Errorf("layer_decomposition requires Seedream Pro/Flash and exactly one image")
		}
		if r.N > 1 || r.SequentialImageGeneration == "auto" {
			return fmt.Errorf("layer decomposition cannot be combined with batch generation")
		}
	} else if strings.TrimSpace(r.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if r.N > 15 {
		return fmt.Errorf("n must not exceed 15")
	}
	if layers && (r.N > 1 || r.SequentialImageGeneration == "auto" || r.SequentialImageGenerationOptions != nil) {
		return fmt.Errorf("Seedream Pro/Flash support one generated image per request; sequential generation is unsupported")
	}
	if r.SequentialImageGeneration != "" && r.SequentialImageGeneration != "auto" && r.SequentialImageGeneration != "disabled" {
		return fmt.Errorf("sequential_image_generation must be auto or disabled")
	}
	if r.N > 1 {
		if r.SequentialImageGeneration == "disabled" {
			return fmt.Errorf("n > 1 conflicts with disabled sequential generation")
		}
		if r.SequentialImageGenerationOptions != nil && r.SequentialImageGenerationOptions.MaxImages != int(r.N) {
			return fmt.Errorf("n and max_images must agree")
		}
		r.SequentialImageGeneration = "auto"
		r.SequentialImageGenerationOptions = &SequentialImageOptions{MaxImages: int(r.N)}
	}
	if r.SequentialImageGenerationOptions != nil && r.SequentialImageGeneration != "auto" {
		return fmt.Errorf("max_images requires sequential_image_generation=auto")
	}
	if r.SequentialImageGeneration == "auto" {
		if r.SequentialImageGenerationOptions == nil {
			r.SequentialImageGenerationOptions = &SequentialImageOptions{MaxImages: 15 - len(refs)}
		}
		max := r.SequentialImageGenerationOptions.MaxImages
		if max < 1 || max+len(refs) > 15 {
			return fmt.Errorf("reference images + max_images must be at most 15")
		}
	}
	if r.Size == "" {
		if r.LayerDecomposition {
			r.Size = "auto"
		} else {
			r.Size = "2K"
		}
	}
	allowed := map[string]bool{"2K": true}
	if layers {
		allowed["1K"], allowed["1.5K"] = true, true
		if r.LayerDecomposition {
			allowed["auto"] = true
		}
	} else {
		allowed["4K"] = true
		if strings.HasPrefix(SeedreamName(r.Model), "seedream-5-0") {
			allowed["3K"] = true
		}
	}
	if !allowed[r.Size] {
		pixels, err := ImagePixels(r.Size)
		if err == nil {
			parts := strings.Split(r.Size, "x")
			w, _ := strconv.ParseInt(parts[0], 10, 32)
			h, _ := strconv.ParseInt(parts[1], 10, 32)
			if w > h*16 || h > w*16 {
				return fmt.Errorf("image aspect ratio must be between 1:16 and 16:1")
			}
		}
		min, max := int64(3686400), int64(16777216)
		if layers {
			min, max = 921600, 4624220
		}
		if strings.HasPrefix(SeedreamName(r.Model), "seedream-4-0") {
			min = 921600
			allowed["1K"] = true
		}
		if !allowed[r.Size] && (err != nil || pixels < min || pixels > max || r.LayerDecomposition) {
			return fmt.Errorf("unsupported size for this Seedream model")
		}
	}
	if r.ResponseFormat != "" && r.ResponseFormat != "url" && r.ResponseFormat != "b64_json" {
		return fmt.Errorf("response_format must be url or b64_json")
	}
	return nil
}

func (r ImageRequest) SeedreamReservation() *billing.ImageUsage {
	refs, _ := r.ReferenceImages()
	count := 1
	if r.SequentialImageGeneration == "auto" && r.SequentialImageGenerationOptions != nil {
		count = r.SequentialImageGenerationOptions.MaxImages
	}
	if r.LayerDecomposition {
		count = 17
	}
	result := &billing.ImageUsage{InputImages: len(refs), Outputs: make([]billing.ImageOutput, count)}
	// Reserve the maximum pixel tier; settle from actual output dimensions.
	for i := range result.Outputs {
		result.Outputs[i] = billing.ImageOutput{Pixels: 16777216, Layer: r.LayerDecomposition}
	}
	return result
}

// SeedreamPresetSize adapts the legacy square and shape presets to supported pixels.
func SeedreamPresetSize(model, size string) string {
	if !IsSeedream(model) {
		return size
	}
	pro := strings.HasPrefix(SeedreamName(model), "seedream-5-0-pro")
	switch size {
	case "", "1024x1024":
		if pro {
			return "1536x1536"
		}
		return "2048x2048"
	case "1536x1024":
		if pro {
			return "1872x1248"
		}
		return "2496x1664"
	case "1024x1536":
		if pro {
			return "1248x1872"
		}
		return "1664x2496"
	}
	return size
}
