package system_setting

import (
	"strings"
	"testing"
)

func TestEmbeddedLegalDefaultsCoverOAuthAndPayments(t *testing.T) {
	t.Parallel()

	privacyPolicies := map[string]struct {
		content        string
		web3Disclosure string
	}{
		"en":    {defaultPrivacyPolicyEn, "stablecoin"},
		"zh":    {defaultPrivacyPolicyZh, "稳定币"},
		"zh-TW": {defaultPrivacyPolicyZhTW, "穩定幣"},
		"ja":    {defaultPrivacyPolicyJa, "ステーブルコイン"},
	}
	for locale, policy := range privacyPolicies {
		policy := policy
		t.Run("privacy_"+locale, func(t *testing.T) {
			t.Parallel()
			for _, required := range []string{
				"developers.google.com/terms/api-services-user-data-policy",
				"Stripe",
				"support@lazu.ai",
				policy.web3Disclosure,
			} {
				if !strings.Contains(policy.content, required) {
					t.Fatalf("embedded %s privacy policy is missing %q", locale, required)
				}
			}
		})
	}

	userAgreements := map[string]struct {
		content        string
		privacyURL     string
		web3Disclosure string
	}{
		"en":    {defaultUserAgreementEn, "https://lazu.ai/privacy", "stablecoin"},
		"zh":    {defaultUserAgreementZh, "https://lazu.ai/zh/privacy", "稳定币"},
		"zh-TW": {defaultUserAgreementZhTW, "https://lazu.ai/zh-TW/privacy", "穩定幣"},
		"ja":    {defaultUserAgreementJa, "https://lazu.ai/ja/privacy", "ステーブルコイン"},
	}
	for locale, agreement := range userAgreements {
		agreement := agreement
		t.Run("agreement_"+locale, func(t *testing.T) {
			t.Parallel()
			for _, required := range []string{
				"Google",
				"Stripe",
				"support@lazu.ai",
				agreement.privacyURL,
				agreement.web3Disclosure,
			} {
				if !strings.Contains(agreement.content, required) {
					t.Fatalf("embedded %s user agreement is missing %q", locale, required)
				}
			}
			if strings.Contains(agreement.content, "https://lazu.ai/privacy-policy") {
				t.Fatalf("embedded %s user agreement still links to the legacy privacy URL", locale)
			}
		})
	}
}

func TestCurrentLegalDefaultsAreNotLegacy(t *testing.T) {
	t.Parallel()

	current := map[string]string{
		"legal.user_agreement_zh":    defaultUserAgreementZh,
		"legal.user_agreement_en":    defaultUserAgreementEn,
		"legal.user_agreement_zh_tw": defaultUserAgreementZhTW,
		"legal.user_agreement_ja":    defaultUserAgreementJa,
		"legal.privacy_policy_zh":    defaultPrivacyPolicyZh,
		"legal.privacy_policy_en":    defaultPrivacyPolicyEn,
		"legal.privacy_policy_zh_tw": defaultPrivacyPolicyZhTW,
		"legal.privacy_policy_ja":    defaultPrivacyPolicyJa,
	}

	for key, content := range current {
		if matchesPreviousLegalDefault(key, content) {
			t.Fatalf("current default %s must not be re-migrated on every startup", key)
		}
	}
}
