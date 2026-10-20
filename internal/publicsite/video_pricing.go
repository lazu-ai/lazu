package publicsite

import (
	"sort"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/pkg/billing"
)

// VideoPriceRows lists a video lane's per-second prices by resolution, then
// how reference video and the final charge work, for the model page.
func VideoPriceRows(p *billing.VideoPrices, locale string) []ImagePriceRow {
	if p == nil {
		return nil
	}
	zh := strings.HasPrefix(locale, "zh")
	word := func(en, cn string) string {
		if zh {
			return cn
		}
		return en
	}
	perSecond := func(v float64) string {
		return "$" + strconv.FormatFloat(v, 'f', -1, 64) + " " + word("/ second", "/ 秒")
	}
	tiers := make([]string, 0, len(p.ResolutionSecondUSD))
	for tier := range p.ResolutionSecondUSD {
		tiers = append(tiers, tier)
	}
	sort.Slice(tiers, func(i, j int) bool { return p.ResolutionSecondUSD[tiers[i]] < p.ResolutionSecondUSD[tiers[j]] })
	rows := make([]ImagePriceRow, 0, len(tiers)+3)
	for _, tier := range tiers {
		rows = append(rows, ImagePriceRow{Label: tier, Price: perSecond(p.ResolutionSecondUSD[tier])})
	}
	if len(tiers) == 0 && p.SecondUSD != nil {
		rows = append(rows, ImagePriceRow{Label: word("Each second", "每秒"), Price: perSecond(*p.SecondUSD)})
	}
	if p.AudioSurchargeRatio != nil && *p.AudioSurchargeRatio > 0 {
		rows = append(rows, ImagePriceRow{Label: word("With audio", "带声音"), Price: "× " + strconv.FormatFloat(*p.AudioSurchargeRatio, 'f', -1, 64)})
	}
	if p.ProviderCostBilled {
		rows = append(rows,
			ImagePriceRow{Label: word("Reference video", "参考视频"), Price: word("Billed by the second with the output", "与输出时长一起按秒计费")},
			ImagePriceRow{Label: word("Charge", "结算"), Price: word("What the provider reports for the finished clip", "按完成后的实际用量结算")},
		)
	}
	return rows
}

func laneVideoPrices(lane Lane, model Model) *billing.VideoPrices {
	if lane.Pricing != nil && lane.Pricing.VideoPricing != nil {
		return lane.Pricing.VideoPricing
	}
	if lane.Key == "stable" && model.Pricing != nil {
		return model.Pricing.VideoPricing
	}
	return nil
}
