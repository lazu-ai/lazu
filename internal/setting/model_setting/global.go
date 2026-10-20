package model_setting

import (
	"github.com/lazu-ai/lazu/internal/setting/config"
)

type GlobalSettings struct {
	PassThroughRequestEnabled bool `json:"pass_through_request_enabled"`
	// LegacyClaudeClientConversion sends Claude clients (/v1/messages)
	// through the legacy pairwise conversion instead of the protocol hub:
	// no protocol parse, no plan. It exists for the release the hub ships in,
	// as the switch an operator throws without a deploy if the hub refuses
	// traffic it should not; the legacy path is deleted the release after.
	LegacyClaudeClientConversion bool `json:"legacy_claude_client_conversion"`
}

// 默认配置
var defaultOpenaiSettings = GlobalSettings{
	PassThroughRequestEnabled: false,
}

// 全局实例
var globalSettings = defaultOpenaiSettings

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("global", &globalSettings)
}

func GetGlobalSettings() *GlobalSettings {
	return &globalSettings
}
