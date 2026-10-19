package setting

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

func TestApplyCredentialConfigUsesPublicServerAddressEnvironment(t *testing.T) {
	previous := system_setting.ServerAddress
	t.Cleanup(func() {
		system_setting.ServerAddress = previous
	})

	t.Setenv("PUBLIC_SERVER_ADDRESS", "https://api.lazu.ai/")
	ApplyCredentialConfig()

	if system_setting.ServerAddress != "https://api.lazu.ai" {
		t.Fatalf("unexpected public server address %q", system_setting.ServerAddress)
	}
}

func TestApplyCredentialConfigUsesStripePriceEnvironment(t *testing.T) {
	previous := StripePriceId
	t.Cleanup(func() {
		StripePriceId = previous
	})

	t.Setenv("STRIPE_PRICE_ID", "price_test")
	ApplyCredentialConfig()

	if StripePriceId != "price_test" {
		t.Fatalf("unexpected Stripe price id %q", StripePriceId)
	}
}
