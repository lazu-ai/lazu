package setting

import (
	"fmt"
	"os"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

type credentialDefinition struct {
	Key     string
	Label   string
	Env     string
	YAML    func() string
	Current func() string
}

func credentialDefinitions() []credentialDefinition {
	return []credentialDefinition{
		{
			Key: "StripeSecretKey", Label: "payment.stripe.secret_key", Env: "STRIPE_SECRET_KEY",
			YAML:    func() string { return common.AppConfig.Payment.Stripe.SecretKey },
			Current: func() string { return StripeApiSecret },
		},
		{
			Key: "StripeWebhookSecret", Label: "payment.stripe.webhook_secret", Env: "STRIPE_WEBHOOK_SECRET",
			YAML:    func() string { return common.AppConfig.Payment.Stripe.WebhookSecret },
			Current: func() string { return StripeWebhookSecret },
		},
		{
			Key: "StripePriceId", Label: "payment.stripe.price_id", Env: "STRIPE_PRICE_ID",
			YAML:    func() string { return common.AppConfig.Payment.Stripe.PriceID },
			Current: func() string { return StripePriceId },
		},
		{
			Key: "StripeServerAddress", Label: "payment.stripe.server_address", Env: "PUBLIC_SERVER_ADDRESS",
			YAML:    func() string { return common.AppConfig.Payment.Stripe.ServerAddress },
			Current: func() string { return system_setting.ServerAddress },
		},
		{
			Key: "SMTPAccount", Label: "smtp.account", Env: "SMTP_ACCOUNT",
			YAML:    func() string { return common.AppConfig.SMTP.Account },
			Current: func() string { return common.SMTPAccount },
		},
		{
			Key: "SMTPToken", Label: "smtp.token", Env: "SMTP_TOKEN",
			YAML:    func() string { return common.AppConfig.SMTP.Token },
			Current: func() string { return common.SMTPToken },
		},
	}
}

func credentialDefinitionByKey(key string) (credentialDefinition, bool) {
	for _, def := range credentialDefinitions() {
		if def.Key == key {
			return def, true
		}
	}
	return credentialDefinition{}, false
}

func GetCredential(key string) string {
	def, ok := credentialDefinitionByKey(key)
	if !ok {
		return ""
	}
	value, _ := credentialValueAndSource(def)
	return value
}

func credentialValueAndSource(def credentialDefinition) (string, string) {
	if def.Env != "" {
		if value := strings.TrimSpace(os.Getenv(def.Env)); value != "" {
			return value, "env"
		}
	}
	if def.YAML != nil {
		if value := strings.TrimSpace(def.YAML()); value != "" {
			return value, "yaml"
		}
	}
	if def.Current != nil {
		if value := strings.TrimSpace(def.Current()); value != "" {
			return value, "runtime"
		}
	}
	return "", ""
}

func applyCredential(key string, apply func(string)) {
	if value := GetCredential(key); value != "" {
		apply(value)
	}
}

func ApplyCredentialConfig() {
	applyCredential("StripeSecretKey", func(value string) { StripeApiSecret = value })
	applyCredential("StripeWebhookSecret", func(value string) { StripeWebhookSecret = value })
	applyCredential("StripePriceId", func(value string) { StripePriceId = value })
	applyCredential("StripeServerAddress", func(value string) { system_setting.ServerAddress = strings.TrimRight(value, "/") })
	applyCredential("SMTPAccount", func(value string) { common.SMTPAccount = value })
	applyCredential("SMTPToken", func(value string) { common.SMTPToken = value })

	LogCredentialConfigStatus()
}

func LogCredentialConfigStatus() {
	setCount := 0
	unsetCount := 0
	verbose := strings.EqualFold(os.Getenv("CONFIG_LOG_VERBOSE"), "true")
	if verbose {
		common.SysLog("[config] loaded credentials from yaml/env:")
	}
	for _, def := range credentialDefinitions() {
		state := "unset"
		_, source := credentialValueAndSource(def)
		if source != "" {
			state = "set"
			setCount++
		} else {
			unsetCount++
		}
		if verbose {
			if source == "" {
				source = "none"
			}
			common.SysLog(fmt.Sprintf("[config]   - %s: %s (%s)", def.Label, state, source))
		}
	}
	if !verbose {
		common.SysLog(fmt.Sprintf(
			"[config] credentials loaded: %d set, %d unset (set CONFIG_LOG_VERBOSE=true for details)",
			setCount,
			unsetCount,
		))
	}
}
