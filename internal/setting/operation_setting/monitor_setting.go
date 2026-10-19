package operation_setting

import (
	"os"
	"strconv"

	"github.com/lazu-ai/lazu/internal/setting/config"
)

type MonitorSetting struct {
	AutoTestChannelEnabled bool `json:"auto_test_channel_enabled"`
	AutoTestChannelMinutes int  `json:"auto_test_channel_minutes"`
}

// 默认配置
var monitorSetting = MonitorSetting{
	AutoTestChannelEnabled: false,
	AutoTestChannelMinutes: 10,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("monitor_setting", &monitorSetting)
}

// ChannelTestFrequencyOverride reports whether CHANNEL_TEST_FREQUENCY takes
// over the probe schedule, and with what interval. It is the one predicate the
// getter applies and the console reports, so a value the getter ignores — "0",
// "abc" — is never shown as owning the controls.
func ChannelTestFrequencyOverride() (int, bool) {
	frequency, err := strconv.Atoi(os.Getenv("CHANNEL_TEST_FREQUENCY"))
	if err != nil || frequency <= 0 {
		return 0, false
	}
	return frequency, true
}

func GetMonitorSetting() *MonitorSetting {
	if frequency, ok := ChannelTestFrequencyOverride(); ok {
		monitorSetting.AutoTestChannelEnabled = true
		monitorSetting.AutoTestChannelMinutes = frequency
	}
	return &monitorSetting
}
