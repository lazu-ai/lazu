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

// Fixed values - no longer configurable
var StripeMinTopUp = 1

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
