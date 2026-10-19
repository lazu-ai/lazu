package billing

import (
	"fmt"
	"math"

	"github.com/shopspring/decimal"
)

// ImagePrices describes prices per successful output, independently of token
// prices. Pixel tiers use each output's actual size, including cropped layers.
type ImagePrices struct {
	OutputUSD               float64  `json:"output_usd"`
	PixelThreshold          int64    `json:"pixel_threshold,omitempty"`
	HighResolutionOutputUSD *float64 `json:"high_resolution_output_usd,omitempty"`
	LayerUSD                *float64 `json:"layer_usd,omitempty"`
	HighResolutionLayerUSD  *float64 `json:"high_resolution_layer_usd,omitempty"`
	ReferenceUSD            *float64 `json:"reference_usd,omitempty"`
	FreeReferences          int      `json:"free_references,omitempty"`
}

type ImageOutput struct {
	Pixels int64 `json:"pixels"`
	Layer  bool  `json:"layer,omitempty"`
}

type ImageUsage struct {
	Outputs     []ImageOutput `json:"outputs"`
	InputImages int           `json:"input_images"`
}

func (p *ImagePrices) Validate() error {
	if p == nil {
		return nil
	}
	if p.OutputUSD <= 0 || p.PixelThreshold < 0 || p.FreeReferences < 0 || p.FreeReferences > 14 {
		return fmt.Errorf("invalid image pricing")
	}
	for _, value := range []*float64{&p.OutputUSD, p.HighResolutionOutputUSD, p.LayerUSD, p.HighResolutionLayerUSD, p.ReferenceUSD} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1000) {
			return fmt.Errorf("invalid image unit price")
		}
	}
	if p.HighResolutionOutputUSD != nil && p.PixelThreshold == 0 || p.HighResolutionLayerUSD != nil && (p.PixelThreshold == 0 || p.LayerUSD == nil) {
		return fmt.Errorf("image pixel tiers require a threshold and base price")
	}
	return nil
}

func (p *ImagePrices) Scaled(factor float64) *ImagePrices {
	if p == nil {
		return nil
	}
	out := *p
	scale := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		x := decimal.NewFromFloat(*v).Mul(decimal.NewFromFloat(factor)).Round(10).InexactFloat64()
		return &x
	}
	out.OutputUSD = *scale(&p.OutputUSD)
	out.HighResolutionOutputUSD = scale(p.HighResolutionOutputUSD)
	out.LayerUSD = scale(p.LayerUSD)
	out.HighResolutionLayerUSD = scale(p.HighResolutionLayerUSD)
	out.ReferenceUSD = scale(p.ReferenceUSD)
	return &out
}

func imageLines(u *ImageUsage, p *ImagePrices) ([]LineItem, []string) {
	if u == nil || len(u.Outputs) == 0 {
		return nil, nil
	}
	if p == nil {
		return nil, []string{"generated_image"}
	}
	counts := map[string]int64{}
	prices := map[string]*float64{}
	for _, output := range u.Outputs {
		name, price := "generated_image", &p.OutputUSD
		if output.Layer && p.LayerUSD != nil {
			name, price = "image_layer", p.LayerUSD
		}
		if p.PixelThreshold > 0 && output.Pixels > p.PixelThreshold {
			if output.Layer && p.HighResolutionLayerUSD != nil {
				name, price = "image_layer_high_resolution", p.HighResolutionLayerUSD
			} else if !output.Layer && p.HighResolutionOutputUSD != nil {
				name, price = "generated_image_high_resolution", p.HighResolutionOutputUSD
			}
		}
		counts[name]++
		prices[name] = price
	}
	if u.InputImages > p.FreeReferences && p.ReferenceUSD != nil {
		counts["image_reference"] = int64(u.InputImages - p.FreeReferences)
		prices["image_reference"] = p.ReferenceUSD
	}
	lines := make([]LineItem, 0, 5)
	for _, name := range []string{"generated_image", "generated_image_high_resolution", "image_layer", "image_layer_high_resolution", "image_reference"} {
		if counts[name] == 0 {
			continue
		}
		unit := MicroUSD(decimal.NewFromFloat(*prices[name]).Mul(decimal.NewFromInt(MicroUSDPerUSD)).Round(0).IntPart())
		lines = append(lines, LineItem{Dimension: name, Quantity: counts[name], UnitMicrousd: unit, AmountMicrousd: MicroUSD(counts[name]) * unit})
	}
	return lines, nil
}
