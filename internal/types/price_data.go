package types

import (
	"fmt"

	"github.com/lazu-ai/lazu/pkg/billing"
)

type PriceData struct {
	FreeModel                   bool
	PerCallPriceUSD             float64
	PricingVersion              int64
	OtherPriceAdjustments       map[string]float64
	OtherPriceAdjustmentReasons map[string]string
	UsePrice                    bool
	QuotaToPreConsume           billing.MicroUSD // 预消耗额度
}

func (p PriceData) ToSetting() string {
	return fmt.Sprintf("PerCallPriceUSD: %f, PricingVersion: %d, UsePrice: %t, QuotaToPreConsume: %d", p.PerCallPriceUSD, p.PricingVersion, p.UsePrice, p.QuotaToPreConsume)
}
