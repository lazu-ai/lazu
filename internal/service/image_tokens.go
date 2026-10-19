package service

import (
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"strings"

	"github.com/lazu-ai/lazu/internal/types"
)

const imageHeaderBytes = 64 << 10

// Local estimates, not provider measurements. Rules checked 2026-09-05:
// https://developers.openai.com/api/docs/guides/images-vision
// Keep explicit families so future model names cannot inherit an unrelated rule.
type imageTokenRule struct {
	base, tile, maxSide, patches int
	factor                       float64
}

func visionTokenRule(model, detail string) (imageTokenRule, bool) {
	model = strings.ToLower(model)
	family := func(name string) bool { return model == name || strings.HasPrefix(model, name+"-") }
	patch := func(maxSide, patches int, factor float64) (imageTokenRule, bool) {
		return imageTokenRule{maxSide: maxSide, patches: patches, factor: factor}, true
	}
	switch {
	case family("gpt-5.4"):
		switch detail {
		case "original":
			return patch(6000, 10000, 1.2)
		case "low":
			return patch(2048, 6144, 1.2)
		default:
			return patch(2048, 2500, 1.2)
		}
	case family("gpt-5.2"):
		return patch(2048, 6144, 1.2)
	case family("gpt-4.1-mini"):
		return patch(2048, 6144, 1.62)
	case family("gpt-4.1-nano"):
		return patch(0, 1536, 2.46)
	case family("gpt-5-mini"):
		return patch(0, 1536, 1.2)
	case family("gpt-5-nano"):
		return patch(0, 1536, 1.5)
	case family("o4-mini"):
		return patch(0, 1536, 1.72)
	case family("gpt-4o-mini"):
		return imageTokenRule{base: 2833, tile: 5667}, true
	case family("gpt-4o"), family("gpt-4.1"), family("gpt-4.5"):
		return imageTokenRule{base: 85, tile: 170}, true
	case family("gpt-5"), family("gpt-5.1"):
		return imageTokenRule{base: 70, tile: 140}, true
	case family("o1"), family("o3"):
		return imageTokenRule{base: 75, tile: 150}, true
	case family("glm-4"):
		return imageTokenRule{base: 1047}, true
	default:
		return imageTokenRule{}, false
	}
}

func (r imageTokenRule) reserve(detail string) int {
	if r.patches > 0 {
		patches := r.patches
		if r.maxSide > 0 {
			patches = min(patches, ((r.maxSide+31)/32)*((r.maxSide+31)/32))
		}
		return int(math.Ceil(float64(patches) * r.factor))
	}
	if detail == "low" || r.tile == 0 {
		return r.base
	}
	// Fit to 2048, then shortest side <= 768: at most 4 * 2 tiles.
	return r.base + 8*r.tile
}

func (r imageTokenRule) count(width, height int, detail string) int {
	if width <= 0 || height <= 0 {
		return r.reserve(detail)
	}
	if r.patches == 0 && (detail == "low" || r.tile == 0) {
		return r.base
	}
	w, h := float64(width), float64(height)
	maxSide := r.maxSide
	if r.patches == 0 {
		maxSide = 2048
	}
	if maxSide > 0 && math.Max(w, h) > float64(maxSide) {
		scale := float64(maxSide) / math.Max(w, h)
		w, h = math.Max(1, math.Floor(w*scale)), math.Max(1, math.Floor(h*scale))
	}
	if r.patches > 0 {
		if math.Ceil(w/32)*math.Ceil(h/32) > float64(r.patches) {
			scale := math.Sqrt(float64(32*32*r.patches) / (w * h))
			pw, ph := w*scale/32, h*scale/32
			adjust := math.Min(math.Floor(pw)/pw, math.Floor(ph)/ph)
			if adjust > 0 {
				scale *= adjust
			}
			w, h = math.Max(1, math.Floor(w*scale)), math.Max(1, math.Floor(h*scale))
			// Extremely narrow images still need one patch across. Fit the
			// other axis instead of overflowing the budget or counting zero.
			if w < 32 {
				h = math.Min(h, float64(r.patches*32))
			}
			if h < 32 {
				w = math.Min(w, float64(r.patches*32))
			}
		}
		return int(math.Ceil(math.Ceil(w/32) * math.Ceil(h/32) * r.factor))
	}
	// The current tile-based rule only scales down. Small high/auto images
	// are not enlarged to 768; see the provider guide cited above.
	if short := math.Min(w, h); short > 768 {
		scale := 768 / short
		w, h = math.Floor(w*scale), math.Floor(h*scale)
	}
	return r.base + int(math.Ceil(w/512)*math.Ceil(h/512))*r.tile
}

// inlineImageConfig reads only a bounded decoded header. It neither allocates
// a full decoded image nor fetches URLs, even for malformed/very large inputs.
func inlineImageConfig(file *types.FileMeta) (image.Config, error) {
	if file == nil {
		return image.Config{}, errors.New("missing image")
	}
	data := file.OriginData
	if file.ParsedData != nil {
		data = file.ParsedData.Base64Data
	}
	if strings.HasPrefix(data, "data:") {
		comma := strings.IndexByte(data[:min(len(data), 256)], ',')
		if comma < 0 || !strings.HasSuffix(data[:comma], ";base64") {
			return image.Config{}, errors.New("unsupported image data header")
		}
		data = data[comma+1:]
	}
	if strings.HasPrefix(data, "http://") || strings.HasPrefix(data, "https://") {
		return image.Config{}, errors.New("remote image dimensions unavailable locally")
	}
	// Bound encoded bytes too: base64's decoder otherwise skips unbounded
	// CR/LF input before producing a single decoded byte.
	encoded := strings.NewReader(data[:min(len(data), base64.StdEncoding.EncodedLen(imageHeaderBytes))])
	cfg, _, err := image.DecodeConfig(io.LimitReader(base64.NewDecoder(base64.StdEncoding, encoded), imageHeaderBytes))
	return cfg, err
}

func imageAdmissionTokens(file *types.FileMeta, model string) int {
	rule, known := visionTokenRule(model, file.Detail)
	if !known {
		return imageTPMReserve(model)
	}
	if (rule.patches == 0 && file.Detail == "low") || rule.tile == 0 && rule.patches == 0 {
		return rule.base
	}
	if cfg, err := inlineImageConfig(file); err == nil && cfg.Width > 0 && cfg.Height > 0 {
		return rule.count(cfg.Width, cfg.Height, file.Detail)
	}
	return rule.reserve(file.Detail)
}

func getImageToken(file *types.FileMeta, model string, _ bool) (int, error) {
	if file == nil {
		return 0, errors.New("missing image")
	}
	rule, known := visionTokenRule(model, file.Detail)
	if !known {
		return 0, errors.New("image token rule unavailable for model")
	}
	if (rule.patches == 0 && file.Detail == "low") || rule.tile == 0 && rule.patches == 0 {
		return rule.base, nil
	}
	cfg, err := inlineImageConfig(file)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, errors.New("image dimensions unavailable within local header limit")
	}
	return rule.count(cfg.Width, cfg.Height, file.Detail), nil
}
