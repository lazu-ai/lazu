package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func putOption(t *testing.T, key string, value any) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, _ := json.Marshal(map[string]any{"key": key, "value": value})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option/", bytes.NewReader(body))
	UpdateOption(ctx)
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return recorder.Code, payload
}

// expectSettingInvalid asserts a 400 setting_invalid naming the key and rule.
func expectSettingInvalid(t *testing.T, status int, payload map[string]any, key string, rule string) {
	t.Helper()
	if status != http.StatusBadRequest || payload["success"] != false {
		t.Fatalf("expected a 400 failure, got %d %v", status, payload)
	}
	errorBody, _ := payload["error"].(map[string]any)
	details, _ := errorBody["details"].(map[string]any)
	if errorBody["code"] != "setting_invalid" || details["field"] != key || details["rule"] != rule {
		t.Fatalf("expected setting_invalid on %s/%s, got %v", key, rule, errorBody)
	}
}

func TestUpdateOptionRefusesEmptyingAnActiveDomainAllowlist(t *testing.T) {
	previous := common.EmailDomainRestrictionEnabled
	common.EmailDomainRestrictionEnabled = true
	t.Cleanup(func() { common.EmailDomainRestrictionEnabled = previous })

	// Blank entries count as empty: " , " is the same lockout as "".
	for _, value := range []string{"", " , "} {
		status, payload := putOption(t, "EmailDomainWhitelist", value)
		expectSettingInvalid(t, status, payload, "EmailDomainWhitelist", "locked_while_enabled")
	}
}

func TestUpdateOptionRefusesANonPositiveProbeInterval(t *testing.T) {
	for _, value := range []any{"0", "-3", "1.5", 0} {
		status, payload := putOption(t, "monitor_setting.auto_test_channel_minutes", value)
		expectSettingInvalid(t, status, payload, "monitor_setting.auto_test_channel_minutes", "min_integer")
	}
}
