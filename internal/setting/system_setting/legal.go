package system_setting

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"strings"

	"github.com/lazu-ai/lazu/internal/setting/config"
)

//go:embed legal_defaults/user_agreement.zh.md
var defaultUserAgreementZh string

//go:embed legal_defaults/user_agreement.en.md
var defaultUserAgreementEn string

//go:embed legal_defaults/user_agreement.zh-TW.md
var defaultUserAgreementZhTW string

//go:embed legal_defaults/user_agreement.ja.md
var defaultUserAgreementJa string

//go:embed legal_defaults/privacy_policy.zh.md
var defaultPrivacyPolicyZh string

//go:embed legal_defaults/privacy_policy.en.md
var defaultPrivacyPolicyEn string

//go:embed legal_defaults/privacy_policy.zh-TW.md
var defaultPrivacyPolicyZhTW string

//go:embed legal_defaults/privacy_policy.ja.md
var defaultPrivacyPolicyJa string

// LegalSettings stores the user agreement and privacy policy per UI locale
// (zh / en / zh-TW / ja). Each locale is an independently editable document.
type LegalSettings struct {
	UserAgreementZh   string `json:"user_agreement_zh"`
	UserAgreementEn   string `json:"user_agreement_en"`
	UserAgreementZhTW string `json:"user_agreement_zh_tw"`
	UserAgreementJa   string `json:"user_agreement_ja"`
	PrivacyPolicyZh   string `json:"privacy_policy_zh"`
	PrivacyPolicyEn   string `json:"privacy_policy_en"`
	PrivacyPolicyZhTW string `json:"privacy_policy_zh_tw"`
	PrivacyPolicyJa   string `json:"privacy_policy_ja"`
}

var defaultLegalSettings = LegalSettings{
	UserAgreementZh:   defaultUserAgreementZh,
	UserAgreementEn:   defaultUserAgreementEn,
	UserAgreementZhTW: defaultUserAgreementZhTW,
	UserAgreementJa:   defaultUserAgreementJa,
	PrivacyPolicyZh:   defaultPrivacyPolicyZh,
	PrivacyPolicyEn:   defaultPrivacyPolicyEn,
	PrivacyPolicyZhTW: defaultPrivacyPolicyZhTW,
	PrivacyPolicyJa:   defaultPrivacyPolicyJa,
}

// previousLegalDefaultHashes identifies exact defaults shipped by earlier
// releases. Deployments can persist these defaults as runtime options, so a
// binary update alone would otherwise leave public pages stale. A key may have
// several known release hashes; administrator-edited documents never match and
// are never overwritten.
var previousLegalDefaultHashes = map[string][]string{
	"legal.user_agreement_zh": {
		"aef738cfae2625ff89aab5033778f94d8205de692af3038547ed043472652293",
		"252733812815a32cffcaad5f361e020495388c929f0a2db9fc5b7651741eefe7",
		"f0fb510693b11227750a533f095e401569e12ab5a5536966bbf16f6e5a66f3a8",
	},
	"legal.user_agreement_en": {
		"0214d23c34da815f10216aa3af0ce2c4f30f417e9bf12ecbfc93cbf887e1ff70",
		"b37d71b08c8cdb4a942d39c35dfc6e1643efedd9fc71fdb7a52996b7c4f689c4",
		"148de1ec47b20828176cc598d8ba63b32e4dd6f4860e161dd8ea670a1e417f03",
	},
	"legal.user_agreement_zh_tw": {
		"6b585d99781c59619b805eccabc0e9b74b9fd663199069c3d92ef7849a1d6533",
		"45a1751289aedeacc49091ad1d4179a223d11f82fff5697a37eee04c5467abc1",
		"b22c8d309cec15209ce13ac78edc0e688814806d0609e9bbdc636f256e09ee0a",
	},
	"legal.user_agreement_ja": {
		"aec2496d4ba3c5ccde1ecbef68e5eaf23ef431c149fbc98213a84032ac82e33d",
		"8a87d74ad1630ee18186816cee148b9ba2a711c3335626ce4acae598b4265a3f",
		"bb8aa69d00b2cda16a7a8986405ef6fa30df529e36c4b0fb520d85d36f688a44",
	},
	"legal.privacy_policy_zh": {
		"59d9a81f819702a9db7f95ce0f4358e94fab8a8536aac9419c1845f4b5e8040e",
		"12d542e52d02cf5c4a911a2160a64b1a84ff893895eeaae05adfcfee60c52694",
		"dfb82e6a92273a2d73ef6bc2c65dedce3e2136c4420fda76bcf57701d4e76b43",
		"450e66e337f9f73d6206e2eaae91c95a633e3a5eef30dbad83f6974aa03e9e2a",
	},
	"legal.privacy_policy_en": {
		"9b5a9c36fc6ca39d9d9dab8cb0d32b22a057ca8a3239f6aadd95676a1f20b9b7",
		"3e1b0e51befb56636d907ee276c5831d5fe7a4b2fc5714e3a5f20f5e071a6f81",
		"48eff294b2d03b0b59d8cf868f5bca6c7bf28f591d4febe831129c5cbb5b7612",
		"4019845a6f88625ef6e28b77b8fa7aa9a4e4c436db0978a422251f73234b4d47",
	},
	"legal.privacy_policy_zh_tw": {
		"5a5493699dd89e03d5ce7b46d5e47b6c1869bff21bd610e16a01986d40862136",
		"c527161250f28add59a66b835199cb1dceecaa44775b08dfa6415bc11ad1ce60",
		"27c2669021c4dc88eb081eaf9ed522b8c59d6d48eb9806e2093239d23e34f07b",
		"18dab09cf01cc96bb2eb213025b30ac57f36a9c79a817e27081ac44b2dd933fb",
	},
	"legal.privacy_policy_ja": {
		"3b019873879fc1d5f1724e9724bbdd627ba4721ea7baeff8bb9bf51d97709178",
		"f66e8e6b2d5fda6626ccdc9381ff4bd3c028bbf3caaa7162f8eb4f1ec3e1c5ca",
		"3cb2f81639c9ea9e145072a746c0a3ef458bdacb1d7ae9936c8b7e1a3121d96a",
		"ce88133fd3ad491ce66c49927dc6e02f9d036c2b625ca8079262272ffa29c373",
	},
}

func matchesPreviousLegalDefault(key, content string) bool {
	sum := sha256.Sum256([]byte(content))
	contentHash := hex.EncodeToString(sum[:])
	for _, knownHash := range previousLegalDefaultHashes[key] {
		if contentHash == knownHash {
			return true
		}
	}
	return false
}

func init() {
	config.GlobalConfig.Register("legal", &defaultLegalSettings)
}

func GetLegalSettings() *LegalSettings {
	return &defaultLegalSettings
}

// LegacyLegalDefaultUpdates returns persisted legal options that are still an
// exact copy of the previous release defaults and can therefore be upgraded
// safely. The caller is responsible for persisting and applying each update.
func LegacyLegalDefaultUpdates() map[string]string {
	current := map[string]string{
		"legal.user_agreement_zh":    defaultLegalSettings.UserAgreementZh,
		"legal.user_agreement_en":    defaultLegalSettings.UserAgreementEn,
		"legal.user_agreement_zh_tw": defaultLegalSettings.UserAgreementZhTW,
		"legal.user_agreement_ja":    defaultLegalSettings.UserAgreementJa,
		"legal.privacy_policy_zh":    defaultLegalSettings.PrivacyPolicyZh,
		"legal.privacy_policy_en":    defaultLegalSettings.PrivacyPolicyEn,
		"legal.privacy_policy_zh_tw": defaultLegalSettings.PrivacyPolicyZhTW,
		"legal.privacy_policy_ja":    defaultLegalSettings.PrivacyPolicyJa,
	}
	replacements := map[string]string{
		"legal.user_agreement_zh":    defaultUserAgreementZh,
		"legal.user_agreement_en":    defaultUserAgreementEn,
		"legal.user_agreement_zh_tw": defaultUserAgreementZhTW,
		"legal.user_agreement_ja":    defaultUserAgreementJa,
		"legal.privacy_policy_zh":    defaultPrivacyPolicyZh,
		"legal.privacy_policy_en":    defaultPrivacyPolicyEn,
		"legal.privacy_policy_zh_tw": defaultPrivacyPolicyZhTW,
		"legal.privacy_policy_ja":    defaultPrivacyPolicyJa,
	}

	updates := make(map[string]string)
	for key, content := range current {
		if matchesPreviousLegalDefault(key, content) {
			updates[key] = replacements[key]
		}
	}
	return updates
}

// normalizeLegalLang maps an arbitrary UI language tag to one of the four
// supported legal-document locales, defaulting to zh.
func normalizeLegalLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "en", "en-us", "en-gb":
		return "en"
	case "ja", "ja-jp":
		return "ja"
	case "zh-tw", "zh-hant", "zh-hk", "zh-mo":
		return "zh-TW"
	default:
		return "zh"
	}
}

// pickLocalized returns the document for the requested locale, falling back
// to the first non-empty locale so a user never sees a blank page when only
// some locales have been filled in.
func pickLocalized(lang, zh, en, zhTW, ja string) string {
	switch normalizeLegalLang(lang) {
	case "en":
		if en != "" {
			return en
		}
	case "ja":
		if ja != "" {
			return ja
		}
	case "zh-TW":
		if zhTW != "" {
			return zhTW
		}
	default:
		if zh != "" {
			return zh
		}
	}
	for _, v := range []string{zh, en, zhTW, ja} {
		if v != "" {
			return v
		}
	}
	return ""
}

// UserAgreementFor returns the user agreement for the given UI language.
func (s *LegalSettings) UserAgreementFor(lang string) string {
	return pickLocalized(
		lang,
		s.UserAgreementZh,
		s.UserAgreementEn,
		s.UserAgreementZhTW,
		s.UserAgreementJa,
	)
}

// PrivacyPolicyFor returns the privacy policy for the given UI language.
func (s *LegalSettings) PrivacyPolicyFor(lang string) string {
	return pickLocalized(
		lang,
		s.PrivacyPolicyZh,
		s.PrivacyPolicyEn,
		s.PrivacyPolicyZhTW,
		s.PrivacyPolicyJa,
	)
}

// HasUserAgreement reports whether any locale of the user agreement is set.
func (s *LegalSettings) HasUserAgreement() bool {
	return s.UserAgreementZh != "" || s.UserAgreementEn != "" ||
		s.UserAgreementZhTW != "" || s.UserAgreementJa != ""
}

// HasPrivacyPolicy reports whether any locale of the privacy policy is set.
func (s *LegalSettings) HasPrivacyPolicy() bool {
	return s.PrivacyPolicyZh != "" || s.PrivacyPolicyEn != "" ||
		s.PrivacyPolicyZhTW != "" || s.PrivacyPolicyJa != ""
}
