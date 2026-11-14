package service

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestErrorRateMonitorCustomerBudgets(t *testing.T) {
	old := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = old })
	tests := []struct {
		name      string
		code      types.ErrorCode
		attempted bool
		want      int64
	}{
		{"wallet", types.ErrorCodeInsufficientQuota, false, 0},
		{"key", types.ErrorCodeKeyBudgetExceeded, false, 0},
		{"legacy key allowance", types.ErrorCodePreConsumeTokenQuotaFailed, false, 0},
		{"member", types.ErrorCodeMemberBudgetExceeded, false, 0},
		{"project", types.ErrorCodeProjectBudgetExceeded, false, 0},
		{"provider quota", types.ErrorCodeInsufficientQuota, true, 1},
		{"provider budget code", types.ErrorCodeKeyBudgetExceeded, true, 1},
		{"platform budget", types.ErrorCodePlatformBudgetExceeded, false, 1},
		{"internal failure", types.ErrorCodeInternalError, false, 1},
		{"unknown", types.ErrorCode("unknown"), false, 1},
		{"missing classification", "", false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &ErrorRateMonitor{windowDuration: time.Hour, windowStart: time.Now(), threshold: 100, maxRecentErrors: 10}
			// A selected channel does not prove an upstream request happened.
			m.RecordError(ErrorLogEntry{ChannelId: 7, ErrorCode: tt.code, ProviderAttempted: tt.attempted, Content: "quota exceeded"})
			if m.errorCount != tt.want || int64(len(m.getRecentErrorsCopy())) != tt.want {
				t.Fatalf("count=%d samples=%d, want %d", m.errorCount, len(m.getRecentErrorsCopy()), tt.want)
			}
		})
	}
}

func TestErrorRateMonitorBudgetFloodPreservesOperationalSamples(t *testing.T) {
	old := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = old })
	m := &ErrorRateMonitor{windowDuration: time.Hour, windowStart: time.Now(), threshold: 100, maxRecentErrors: 10}
	m.RecordError(ErrorLogEntry{ErrorCode: types.ErrorCodeInternalError, Content: "operational failure"})
	for i := 0; i < 1000; i++ {
		m.RecordError(ErrorLogEntry{ErrorCode: types.ErrorCodeKeyBudgetExceeded, ChannelId: 7})
	}
	samples := m.getRecentErrorsCopy()
	if m.errorCount != 1 || len(samples) != 1 || samples[0].Content != "operational failure" || !m.lastAlertTime.IsZero() {
		t.Fatalf("budget flood affected alert state: count=%d samples=%v lastAlert=%v", m.errorCount, samples, m.lastAlertTime)
	}
}
