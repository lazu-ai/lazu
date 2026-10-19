package setting

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

// Active Stripe credentials (selected based on environment)
var StripeApiSecret = ""
var StripeWebhookSecret = ""
var StripePriceId = ""
var StripeReturnBase = ""
var StripePromotionCodesEnabled = false

// StripeMinTopUp is the smallest single card top-up in whole USD. It is an
// operator setting (option MinTopUpUSD, 充值与计费 › 钱怎么进来) because card
// fees are a fixed cent amount plus a percentage: a $1 top-up nets about
// $0.67 after Stripe's cut. The compiled value is the default for a
// deployment that has never saved the option.
var StripeMinTopUp = 5

// StripeMaxTopUp is the largest single card top-up in whole USD. Fixed in
// code; larger amounts are split into several payments.
const StripeMaxTopUp = 10000

// LoadStripeFromAppConfig loads the single canonical Stripe config path:
// payment.stripe.*. Secrets should be provided through env-expanded YAML or
// direct env lookups in config_layer.go.
func LoadStripeFromAppConfig(cfg common.PaymentStripeConfig) {
	if cfg.SecretKey != "" {
		StripeApiSecret = cfg.SecretKey
	}
	if cfg.WebhookSecret != "" {
		StripeWebhookSecret = cfg.WebhookSecret
	}
	if cfg.PriceID != "" {
		StripePriceId = cfg.PriceID
	}
	if cfg.ServerAddress != "" {
		system_setting.ServerAddress = strings.TrimRight(cfg.ServerAddress, "/")
	}
	StripeReturnBase = strings.TrimRight(cfg.ReturnBase, "/")
	StripePromotionCodesEnabled = cfg.PromotionCodesEnabled
}

// NormalizeMinTopUp clamps an operator-entered minimum into the range a card
// top-up accepts: at least $1, never above the single-payment ceiling.
func NormalizeMinTopUp(value int) int {
	if value < 1 {
		return 1
	}
	if value > StripeMaxTopUp {
		return StripeMaxTopUp
	}
	return value
}
