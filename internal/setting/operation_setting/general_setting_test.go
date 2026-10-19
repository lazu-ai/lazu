package operation_setting

import "testing"

func TestGeneralSettingDefaultsKeepStreamingConnectionsAlive(t *testing.T) {
	setting := GetGeneralSetting()
	if !setting.PingIntervalEnabled {
		t.Fatal("expected SSE ping interval to be enabled by default")
	}
	if setting.PingIntervalSeconds != 15 {
		t.Fatalf("expected 15 second SSE ping interval, got %d", setting.PingIntervalSeconds)
	}
}
