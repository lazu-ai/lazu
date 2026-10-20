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
//     ProviderCostMultiplierByResolution sets the multiplier per tier, so a
//     lane can follow an official price list whose markup over the
//     aggregator differs by resolution.
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
	// ProviderCostMultiplierByResolution overrides ProviderCostMultiplier for
	// the listed tiers ("720p": 1.07).
	ProviderCostMultiplierByResolution map[string]float64 `json:"provider_cost_multiplier_by_resolution,omitempty"`
	// ProviderCostBilled is set only on the public copy (Public): the lane is
	// billed on the provider's cost, so per-second figures are what the hold
	// is taken on rather than the exact charge. The markup itself is not
	// published.
	ProviderCostBilled bool `json:"provider_cost_billed,omitempty"`
}

// VideoUsage is what one finished (or estimated) generation measured.
type VideoUsage struct {
	Seconds    int64  `json:"seconds,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	// ReferenceSeconds is input video the provider bills alongside the output
	// (reference-to-video). A submission holds for the longest reference the
	// model accepts; settlement prices the provider's own report instead.
	ReferenceSeconds int64    `json:"reference_seconds,omitempty"`
	OutputTokens     int64    `json:"output_tokens,omitempty"`
	ProviderCostUSD  *float64 `json:"provider_cost_usd,omitempty"`
	Audio            bool     `json:"audio,omitempty"`
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
	for tier, value := range p.ProviderCostMultiplierByResolution {
		value := value
		if strings.TrimSpace(tier) == "" || value <= 0 {
			return fmt.Errorf("invalid video provider cost multiplier for resolution %q", tier)
		}
		if err := check("provider cost multiplier", &value, 100); err != nil {
			return err
		}
	}
	return check("provider cost multiplier", p.ProviderCostMultiplier, 100)
}

// FollowsProviderCost reports whether the lane bills a reported provider cost.
func (p *VideoPrices) FollowsProviderCost() bool {
	return p != nil && (p.ProviderCostAuthoritative || p.ProviderCostMultiplier != nil || len(p.ProviderCostMultiplierByResolution) > 0)
}

// ProviderCostMultiplierFor returns the multiplier for a resolution tier: the
// tier's own entry, else ProviderCostMultiplier, else 1.
func (p *VideoPrices) ProviderCostMultiplierFor(resolution string) float64 {
	if p == nil {
		return 1
	}
	wanted := strings.ToLower(strings.TrimSpace(resolution))
	if wanted != "" {
		for tier, value := range p.ProviderCostMultiplierByResolution {
			if strings.ToLower(strings.TrimSpace(tier)) == wanted && value > 0 {
				return value
			}
		}
	}
	if p.ProviderCostMultiplier != nil && *p.ProviderCostMultiplier > 0 {
		return *p.ProviderCostMultiplier
	}
	return 1
}

// Public is the copy customers may see: per-second prices and the audio
// surcharge, with whether the lane follows the provider's cost but not the
// multiplier (dividing a charge by it would reveal the purchase price).
func (p *VideoPrices) Public() *VideoPrices {
	if p == nil {
		return nil
	}
	out := VideoPrices{
		SecondUSD:           p.SecondUSD,
		ResolutionSecondUSD: p.ResolutionSecondUSD,
		AudioSurchargeRatio: p.AudioSurchargeRatio,
		ProviderCostBilled:  p.FollowsProviderCost() || p.ProviderCostBilled,
	}
	return &out
}

// IsEmpty reports whether the row prices nothing on its own.
func (p *VideoPrices) IsEmpty() bool {
	return p == nil || (p.SecondUSD == nil && len(p.ResolutionSecondUSD) == 0 && !p.FollowsProviderCost() && !p.ProviderCostBilled)
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

// LowestSecondUSD is the cheapest per-second price the row lists (a tier or
// the flat second price), or nil when it lists none.
func (p *VideoPrices) LowestSecondUSD() *float64 {
	if p == nil {
		return nil
	}
	var lowest *float64
	consider := func(value float64) {
		if value > 0 && (lowest == nil || value < *lowest) {
			v := value
			lowest = &v
		}
	}
	for _, value := range p.ResolutionSecondUSD {
		consider(value)
	}
	if p.SecondUSD != nil {
		consider(*p.SecondUSD)
	}
	return lowest
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
	if v != nil && u.ProviderCostUSD != nil && *u.ProviderCostUSD > 0 && v.FollowsProviderCost() {
		unit := USD(*u.ProviderCostUSD * v.ProviderCostMultiplierFor(u.Resolution))
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
	// Reference video is billed by the second alongside the output.
	seconds := u.Seconds + u.ReferenceSeconds
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
	amount := MicroUSD(decimal.NewFromInt(seconds).Mul(decimal.NewFromInt(perSecond.Int64())).Round(0).IntPart())
	return []LineItem{{Dimension: VideoDimensionSecond, Quantity: seconds, UnitMicrousd: perSecond, AmountMicrousd: amount}}, nil
}
