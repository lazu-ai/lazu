package common

import (
	"strings"
	"testing"
)

func TestDeprecatedEnvNoticesOnlyWhenEnabled(t *testing.T) {
	previous := SettlementOutboxEnabled
	t.Cleanup(func() { SettlementOutboxEnabled = previous })

	SettlementOutboxEnabled = false
	if notices := deprecatedEnvNotices(); len(notices) != 0 {
		t.Fatalf("notices = %v, want none at the default", notices)
	}
	SettlementOutboxEnabled = true
	notices := deprecatedEnvNotices()
	if len(notices) != 1 || !strings.Contains(notices[0], "SETTLEMENT_OUTBOX_ENABLED") || !strings.Contains(notices[0], "2027-01-24") {
		t.Fatalf("notices = %v", notices)
	}
}
