package publicsite

import (
	"fmt"
	"github.com/lazu-ai/lazu/pkg/billing"
	"strconv"
	"strings"
)

type ImagePriceRow struct{ Label, Price string }

func ImagePriceRows(p *billing.ImagePrices, locale string) []ImagePriceRow {
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
	money := func(v float64) string { return "$" + strconv.FormatFloat(v, 'f', -1, 64) }
	label := word("Each successful output", "每张成功输出")
	if p.PixelThreshold > 0 {
		label += fmt.Sprintf(" ≤ %d px", p.PixelThreshold)
	}
	rows := []ImagePriceRow{{label, money(p.OutputUSD)}}
	if p.HighResolutionOutputUSD != nil {
		rows = append(rows, ImagePriceRow{fmt.Sprintf("> %d px", p.PixelThreshold), money(*p.HighResolutionOutputUSD)})
	}
	if p.LayerUSD != nil {
		rows = append(rows, ImagePriceRow{word("Each layer, including base", "每层（含底图）") + fmt.Sprintf(" ≤ %d px", p.PixelThreshold), money(*p.LayerUSD)})
	}
	if p.HighResolutionLayerUSD != nil {
		rows = append(rows, ImagePriceRow{word("Each layer, including base", "每层（含底图）") + fmt.Sprintf(" > %d px", p.PixelThreshold), money(*p.HighResolutionLayerUSD)})
	}
	refs := word("Free", "免费")
	if p.ReferenceUSD != nil && *p.ReferenceUSD > 0 {
		refs = fmt.Sprintf("%d %s; +%s / %s", p.FreeReferences, word("free", "张免费"), money(*p.ReferenceUSD), word("additional image", "额外图片"))
	}
	return append(rows, ImagePriceRow{word("Reference images", "参考图"), refs})
}

func laneImagePrices(lane Lane, model Model) *billing.ImagePrices {
	if lane.Pricing != nil && lane.Pricing.ImagePricing != nil {
		return lane.Pricing.ImagePricing
	}
	if lane.Key == "stable" && model.Pricing != nil {
		return model.Pricing.ImagePricing
	}
	return nil
}
