package operation_setting

import "github.com/lazu-ai/lazu/internal/setting/config"

// 额度展示类型
const (
	QuotaDisplayTypeUSD = "USD"
	// Deprecated: delete after 2027-01-24 (log text always in USD; see
	// model/option_deprecations.go and docs/DEPLOYMENT.md, Deprecated settings).
	QuotaDisplayTypeTokens = "TOKENS"
)

type GeneralSetting struct {
	DocsLink            string `json:"docs_link"`
	PingIntervalEnabled bool   `json:"ping_interval_enabled"`
	PingIntervalSeconds int    `json:"ping_interval_seconds"`
	// 当前站点额度展示类型：USD / TOKENS
	QuotaDisplayType string `json:"quota_display_type"`
}

// 默认配置
var generalSetting = GeneralSetting{
	DocsLink:            "https://lazu.ai/docs",
	PingIntervalEnabled: true,
	PingIntervalSeconds: 15,
	QuotaDisplayType:    QuotaDisplayTypeUSD,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("general_setting", &generalSetting)
}

func GetGeneralSetting() *GeneralSetting {
	return &generalSetting
}

func NormalizeQuotaDisplayType(value string) string {
	if value == QuotaDisplayTypeTokens {
		return QuotaDisplayTypeTokens
	}
	return QuotaDisplayTypeUSD
}

// GetQuotaDisplayType 返回额度展示类型
func GetQuotaDisplayType() string {
	return NormalizeQuotaDisplayType(generalSetting.QuotaDisplayType)
}

// IsCurrencyDisplay 是否以货币形式展示（USD；TOKENS 为非货币展示）
func IsCurrencyDisplay() bool {
	return GetQuotaDisplayType() != QuotaDisplayTypeTokens
}
