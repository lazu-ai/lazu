package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestUpdateUserRejectsInvalidAuthorizationFields(t *testing.T) {
	setupOIDCTestDB(t)
	gin.SetMode(gin.TestMode)

	user := model.User{
		Username:       "managed-validation-user",
		Password:       "placeholder-hashed-password-value",
		Role:           common.RoleRootUser,
		Status:         common.UserStatusEnabled,
		SessionVersion: 7,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "zero role", field: "role", value: "0"},
		{name: "unknown role", field: "role", value: "9"},
		{name: "zero status", field: "status", value: "0"},
		{name: "unknown status", field: "status", value: "3"},
		{name: "unknown price plan", field: "price_plan_id", value: "999999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"id":%d,%q:%s}`, user.Id, tt.field, tt.value)
			request := httptest.NewRequest(http.MethodPut, "/api/user/", strings.NewReader(body))
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = request
			context.Set("id", 1)
			context.Set("role", common.RoleRootUser)
			context.Set("username", "root")

			UpdateUser(context)

			failure := requireUserAPIError(t, recorder, http.StatusBadRequest, types.ErrorCodeInvalidRequest)
			if failure.Error.Details["field"] != tt.field {
				t.Fatalf("error names field %v, want %q", failure.Error.Details["field"], tt.field)
			}
			var stored model.User
			if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
				t.Fatalf("reload user: %v", err)
			}
			if stored.Role != user.Role || stored.Status != user.Status || stored.PricePlanID != user.PricePlanID || stored.SessionVersion != user.SessionVersion {
				t.Fatalf("invalid update changed security state: role=%d status=%d price_plan_id=%d version=%d", stored.Role, stored.Status, stored.PricePlanID, stored.SessionVersion)
			}
		})
	}
}

func TestEditAuthorizedRejectsInvalidAuthorizationFields(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username:       "model-validation-user",
		Password:       "placeholder-hashed-password-value",
		Role:           common.RoleRootUser,
		Status:         common.UserStatusEnabled,
		SessionVersion: 11,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*model.User)
	}{
		{name: "zero role", mutate: func(candidate *model.User) { candidate.Role = 0 }},
		{name: "zero status", mutate: func(candidate *model.User) { candidate.Status = 0 }},
		{name: "negative price plan", mutate: func(candidate *model.User) { candidate.PricePlanID = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := user
			tt.mutate(&candidate)
			if err := candidate.EditAuthorized(false, common.RoleRootUser, user.Role); err == nil {
				t.Fatal("invalid authorization field was accepted by the model")
			}
			var stored model.User
			if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
				t.Fatalf("reload user: %v", err)
			}
			if stored.Role != user.Role || stored.Status != user.Status || stored.PricePlanID != user.PricePlanID || stored.SessionVersion != user.SessionVersion {
				t.Fatalf("invalid model edit changed security state: role=%d status=%d price_plan_id=%d version=%d", stored.Role, stored.Status, stored.PricePlanID, stored.SessionVersion)
			}
		})
	}
}

func TestRetireOnboardingPreservesLatestStoredPreferences(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username: "onboarding-setting-race",
		Password: "placeholder-hashed-password-value",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Setting:  `{"onboarding_active":true,"notify_type":"email","future_preference":{"revision":1}}`,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	staleSnapshot := user

	latestSetting := `{"onboarding_active":true,"notify_type":"webhook","webhook_url":"https://latest.example/hook","future_preference":{"revision":2}}`
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("setting", latestSetting).Error; err != nil {
		t.Fatalf("write concurrent preference: %v", err)
	}
	if err := retireOnboarding(&staleSnapshot); err != nil {
		t.Fatalf("retire onboarding: %v", err)
	}

	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored.Setting), &settings); err != nil {
		t.Fatalf("decode stored setting: %v", err)
	}
	if _, active := settings["onboarding_active"]; active {
		t.Fatalf("onboarding remained active: %s", stored.Setting)
	}
	if string(settings["notify_type"]) != `"webhook"` || string(settings["webhook_url"]) != `"https://latest.example/hook"` || string(settings["future_preference"]) != `{"revision":2}` {
		t.Fatalf("retirement lost a concurrently committed preference: %s", stored.Setting)
	}
}

func TestOnboardingIgnoresInternalCredentialsAndRequests(t *testing.T) {
	setupOIDCTestDB(t)
	previousLogDB := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(model.CoreDB())

	t.Cleanup(func() { model.SetTelemetryDB(previousLogDB) })
	if err := model.CoreDB().AutoMigrate(&model.Token{}, &model.Log{}); err != nil {
		t.Fatalf("migrate onboarding evidence tables: %v", err)
	}

	user := model.User{
		Username: "onboarding-public-credential",
		Password: "placeholder-hashed-password-value",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Setting:  `{"onboarding_active":true}`,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed onboarding user: %v", err)
	}
	internalScope := "extension-runner"
	internalToken := model.Token{
		UserId: user.Id, Key: "internal-extension-key", Name: "extension-runner",
		Product: "extension", InternalScope: &internalScope,
	}
	if err := model.CoreDB().Create(&internalToken).Error; err != nil {
		t.Fatalf("seed internal token: %v", err)
	}
	if err := model.TelemetryDB().Create(&model.Log{
		UserId: user.Id, Type: model.LogTypeConsume, TokenId: internalToken.Id, TokenName: internalToken.Name,
	}).Error; err != nil {
		t.Fatalf("seed internal request: %v", err)
	}

	state, err := buildOnboardingState(&user)
	if err != nil {
		t.Fatalf("build onboarding state with internal evidence: %v", err)
	}
	if state.HasKey || state.HasRequest {
		t.Fatalf("internal credential completed public onboarding: %+v", state)
	}

	publicToken := model.Token{
		UserId: user.Id, Key: "public-api-key", Name: "Public API key",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true,
	}
	if err := model.CoreDB().Create(&publicToken).Error; err != nil {
		t.Fatalf("seed public token: %v", err)
	}
	if err := model.TelemetryDB().Create(&model.Log{
		UserId: user.Id, Type: model.LogTypeConsume, TokenId: publicToken.Id, TokenName: publicToken.Name,
	}).Error; err != nil {
		t.Fatalf("seed public request: %v", err)
	}
	state, err = buildOnboardingState(&user)
	if err != nil {
		t.Fatalf("build onboarding state with public evidence: %v", err)
	}
	if !state.HasKey || !state.HasRequest {
		t.Fatalf("public API credential/request did not complete onboarding: %+v", state)
	}
}

func TestPreferenceUpdatePreservesRetiredOnboardingAndUnknownFields(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username: "preference-onboarding-race",
		Password: "placeholder-hashed-password-value",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Setting:  `{"onboarding_active":true,"future_preference":{"revision":3}}`,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := retireOnboarding(&user); err != nil {
		t.Fatalf("retire onboarding: %v", err)
	}
	if _, err := updateUserSettingLocked(user.Id, UpdateUserSettingRequest{
		QuotaWarningType:      "email",
		QuotaWarningThreshold: 25,
		NotificationEmail:     "alerts@example.com",
	}); err != nil {
		t.Fatalf("update preferences: %v", err)
	}

	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored.Setting), &settings); err != nil {
		t.Fatalf("decode stored setting: %v", err)
	}
	if _, active := settings["onboarding_active"]; active {
		t.Fatalf("preference update reactivated onboarding: %s", stored.Setting)
	}
	if string(settings["future_preference"]) != `{"revision":3}` || string(settings["notification_email"]) != `"alerts@example.com"` {
		t.Fatalf("preference merge lost unrelated fields: %s", stored.Setting)
	}
}

func TestSidebarUpdatePreservesLatestPreferencesAndRetiredOnboarding(t *testing.T) {
	setupOIDCTestDB(t)

	user := model.User{
		Username: "sidebar-setting-race",
		Password: "placeholder-hashed-password-value",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Setting:  `{"notify_type":"webhook","webhook_url":"https://latest.example/hook","future_preference":{"revision":4}}`,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := updateSidebarModulesLocked(user.Id, `{"workspace":{"enabled":true}}`); err != nil {
		t.Fatalf("update sidebar modules: %v", err)
	}

	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored.Setting), &settings); err != nil {
		t.Fatalf("decode stored setting: %v", err)
	}
	if _, active := settings["onboarding_active"]; active {
		t.Fatalf("sidebar update reactivated onboarding: %s", stored.Setting)
	}
	if string(settings["notify_type"]) != `"webhook"` || string(settings["future_preference"]) != `{"revision":4}` || string(settings["sidebar_modules"]) != `"{\"workspace\":{\"enabled\":true}}"` {
		t.Fatalf("sidebar merge lost unrelated settings: %s", stored.Setting)
	}
}
