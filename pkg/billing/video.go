package billing

import (
	"fmt"
	"math"
	"strings"

	"github.com/shopspring/decimal"
)

// VideoPrices prices one video model beyond a flat per-second rate. Three
// modes, tried in this order at quote time:
//
//  1. ProviderCostAuthoritative / ProviderCostMultiplier: the upstream
//     reported what the job cost (aggregators that return a `cost`);
//     the customer pays that figure times the multiplier (1 when unset).
//  2. Tokens: the price sheet's OutputPerMtok prices the provider's reported
//     video tokens (Ark bills Seedance as width × height × fps × seconds / 1024).
//  3. Seconds: ResolutionSecondUSD per tier ("720p"), else SecondUSD here,
//     else the sheet's flat SecondMicrousd. AudioSurchargeRatio scales the
//     per-second price when the clip carries generated audio.
type VideoPrices struct {
	SecondUSD                 *float64           `json:"second_usd,omitempty"`
	ResolutionSecondUSD       map[string]float64 `json:"resolution_second_usd,omitempty"`
	AudioSurchargeRatio       *float64           `json:"audio_surcharge_ratio,omitempty"`
	ProviderCostMultiplier    *float64           `json:"provider_cost_multiplier,omitempty"`
	ProviderCostAuthoritative bool               `json:"provider_cost_authoritative,omitempty"`
}

// VideoUsage is what one finished (or estimated) generation measured.
type VideoUsage struct {
	Seconds         int64    `json:"seconds,omitempty"`
	Resolution      string   `json:"resolution,omitempty"`
	OutputTokens    int64    `json:"output_tokens,omitempty"`
	ProviderCostUSD *float64 `json:"provider_cost_usd,omitempty"`
	Audio           bool     `json:"audio,omitempty"`
}

func (p *VideoPrices) Validate() error {
	if p == nil {
		return nil
	}
	check := func(name string, value *float64, max float64) error {
		if value == nil {
			return nil
		}
		if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > max {
			return fmt.Errorf("invalid video %s", name)
		}
		return nil
	}
	if err := check("second price", p.SecondUSD, 1000); err != nil {
		return err
	}
	for tier, value := range p.ResolutionSecondUSD {
		value := value
		if strings.TrimSpace(tier) == "" {
			return fmt.Errorf("invalid video resolution tier")
		}
		if err := check("resolution second price", &value, 1000); err != nil {
			return err
		}
	}
	if err := check("audio surcharge ratio", p.AudioSurchargeRatio, 100); err != nil {
		return err
	}
	return check("provider cost multiplier", p.ProviderCostMultiplier, 100)
}

// IsEmpty reports whether the row prices nothing on its own.
func (p *VideoPrices) IsEmpty() bool {
	return p == nil || (p.SecondUSD == nil && len(p.ResolutionSecondUSD) == 0 &&
		p.ProviderCostMultiplier == nil && !p.ProviderCostAuthoritative)
}

// Scaled multiplies every price by factor (peak hours); ratios stay.
func (p *VideoPrices) Scaled(factor float64) *VideoPrices {
	if p == nil {
		return nil
	}
	out := *p
	scale := func(v float64) float64 {
		return decimal.NewFromFloat(v).Mul(decimal.NewFromFloat(factor)).Round(10).InexactFloat64()
	}
	if p.SecondUSD != nil {
		value := scale(*p.SecondUSD)
		out.SecondUSD = &value
	}
	if len(p.ResolutionSecondUSD) > 0 {
		out.ResolutionSecondUSD = make(map[string]float64, len(p.ResolutionSecondUSD))
		for tier, value := range p.ResolutionSecondUSD {
			out.ResolutionSecondUSD[tier] = scale(value)
		}
	}
	return &out
}

// SecondPriceFor returns the per-second price for a resolution tier, or nil.
func (p *VideoPrices) SecondPriceFor(resolution string) *float64 {
	if p == nil {
		return nil
	}
	wanted := strings.ToLower(strings.TrimSpace(resolution))
	for tier, value := range p.ResolutionSecondUSD {
		if strings.ToLower(strings.TrimSpace(tier)) == wanted && wanted != "" {
			value := value
			return &value
		}
	}
	return p.SecondUSD
}

const (
	VideoDimensionProviderCost = "video_provider_cost"
	VideoDimensionOutputTokens = "video_output_tokens"
	VideoDimensionSecond       = "second"
)

// videoLines prices a video usage against a sheet. It replaces the flat
// "second" line, so a sheet with only SecondMicrousd prices exactly as before.
func videoLines(u *VideoUsage, p PriceSheet) ([]LineItem, []string) {
	if u == nil {
		return nil, nil
	}
	v := p.Video
	if v != nil && u.ProviderCostUSD != nil && *u.ProviderCostUSD > 0 &&
		(v.ProviderCostAuthoritative || v.ProviderCostMultiplier != nil) {
		multiplier := 1.0
		if v.ProviderCostMultiplier != nil && *v.ProviderCostMultiplier > 0 {
			multiplier = *v.ProviderCostMultiplier
		}
		unit := USD(*u.ProviderCostUSD * multiplier)
		return []LineItem{{Dimension: VideoDimensionProviderCost, Quantity: 1, UnitMicrousd: unit, AmountMicrousd: unit}}, nil
	}
	if u.OutputTokens > 0 && p.OutputPerMtokMicrousd != nil {
		amount := tokenAmountMicrousd(u.OutputTokens, *p.OutputPerMtokMicrousd)
		return []LineItem{{Dimension: VideoDimensionOutputTokens, Quantity: u.OutputTokens, UnitMicrousd: *p.OutputPerMtokMicrousd, AmountMicrousd: amount}}, nil
	}
	if u.Seconds <= 0 {
		if u.OutputTokens > 0 {
			return nil, []string{VideoDimensionOutputTokens}
		}
		return nil, nil
	}
	var unit *MicroUSD
	if price := v.SecondPriceFor(u.Resolution); price != nil {
		value := USD(*price)
		unit = &value
	}
	if unit == nil {
		unit = p.SecondMicrousd
	}
	if unit == nil {
		return nil, []string{VideoDimensionSecond}
	}
	perSecond := *unit
	if u.Audio && v != nil && v.AudioSurchargeRatio != nil && *v.AudioSurchargeRatio > 0 {
		perSecond = MicroUSD(decimal.NewFromInt(perSecond.Int64()).Mul(decimal.NewFromFloat(*v.AudioSurchargeRatio)).Round(0).IntPart())
	}
	amount := MicroUSD(decimal.NewFromInt(u.Seconds).Mul(decimal.NewFromInt(perSecond.Int64())).Round(0).IntPart())
	return []LineItem{{Dimension: VideoDimensionSecond, Quantity: u.Seconds, UnitMicrousd: perSecond, AmountMicrousd: amount}}, nil
}
