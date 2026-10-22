package controller

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/operation_setting"
)

// GetDeploymentStatus exposes only the readiness facts that retained console
// workflows need. It deliberately does not return configuration values,
// credential sources, or masked secret material.
func GetDeploymentStatus(c *gin.Context) {
	stripeConfigured := strings.TrimSpace(setting.StripeApiSecret) != "" &&
		strings.TrimSpace(setting.StripeWebhookSecret) != ""
	provider := "smtp"
	if strings.TrimSpace(common.AppConfig.Resend.APIKey) != "" {
		provider = "resend"
	}
	_, probeEnvManaged := operation_setting.ChannelTestFrequencyOverride()
	common.ApiSuccess(c, gin.H{
		"payment": gin.H{
			"stripe_configured":       stripeConfigured,
			"stripe_min_topup":        getStripeMinTopup(),
			"stripe_max_topup":        getStripeMaxTopup(),
			"promotion_codes_enabled": setting.StripePromotionCodesEnabled,
		},
		"email": gin.H{
			"provider":   provider,
			"configured": common.EmailTransportConfigured(),
		},
		// CHANNEL_TEST_FREQUENCY is reapplied on every read of the monitor
		// setting, so where it is set the console's probe controls cannot
		// change anything. Report that it owns them rather than leaving an
		// operator to discover it from a save that reports success and does
		// nothing. The value itself stays out of the response.
		"channel_probe": gin.H{
			"env_managed": probeEnvManaged,
		},
		"telegram": telegramStatus(c.Request.Context()),
	})
}
