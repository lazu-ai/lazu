package controller

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestMergeUserSettingJSONPreservesUnownedAndFutureFields(t *testing.T) {
	existing := `{
		"onboarding_active": true,
		"needs_password": true,
		"sidebar_modules": "custom-sidebar",
		"future_setting": {"mode":"on"},
		"webhook_url": "https://old.example.com/hook",
		"webhook_secret": "old-secret",
		"accept_unpriced_model": true,
		"record_ip_log": true
	}`
	merged, err := mergeUserSettingJSON(existing, UpdateUserSettingRequest{
		QuotaWarningType:      dto.NotifyTypeEmail,
		QuotaWarningThreshold: 12.5,
		NotificationEmail:     "alerts@example.com",
	})
	if err != nil {
		t.Fatalf("merge settings: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(merged), &got); err != nil {
		t.Fatalf("decode merged settings: %v", err)
	}
	for _, key := range []string{"onboarding_active", "needs_password", "sidebar_modules", "future_setting"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("unowned setting %q was erased: %s", key, merged)
		}
	}
	if _, ok := got["webhook_url"]; ok {
		t.Fatalf("stale webhook URL survived notification type change: %s", merged)
	}
	if _, ok := got["record_ip_log"]; ok {
		t.Fatalf("retired record_ip_log survived a save: %s", merged)
	}
	if _, ok := got["accept_unpriced_model"]; ok {
		t.Fatalf("retired accept_unpriced_model survived a save: %s", merged)
	}
	if _, ok := got["webhook_secret"]; ok {
		t.Fatalf("stale webhook secret survived notification type change: %s", merged)
	}

	var typed dto.UserSetting
	if err := json.Unmarshal([]byte(merged), &typed); err != nil {
		t.Fatalf("decode typed settings: %v", err)
	}
	if !typed.OnboardingActive || !typed.NeedsPassword || typed.SidebarModules != "custom-sidebar" {
		t.Fatalf("existing typed state changed: %+v", typed)
	}
	if typed.NotifyType != dto.NotifyTypeEmail || typed.NotificationEmail != "alerts@example.com" || typed.QuotaWarningThreshold != 12.5 {
		t.Fatalf("requested settings were not applied: %+v", typed)
	}
}
