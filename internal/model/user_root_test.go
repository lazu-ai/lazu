package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func resetRootConfig(t *testing.T) {
	t.Helper()

	previousUsername := common.AppConfig.Root.Username
	previousPassword := common.AppConfig.Root.Password
	common.AppConfig.Root.Username = ""
	common.AppConfig.Root.Password = ""
	t.Setenv("ROOT_USERNAME", "")
	t.Setenv("ROOT_PASSWORD", "")
	t.Cleanup(func() {
		common.AppConfig.Root.Username = previousUsername
		common.AppConfig.Root.Password = previousPassword
	})
}

func TestGenerateDefaultSidebarConfigForRoleUsesSaasPreset(t *testing.T) {
	raw := generateDefaultSidebarConfigForRole(common.RoleRootUser)
	var config map[string]map[string]bool
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("failed to parse sidebar config: %v", err)
	}

	if !config["workspace"]["model_market"] || !config["workspace"]["token"] || !config["workspace"]["usage"] {
		t.Fatalf("expected core workspace modules to be enabled: %#v", config["workspace"])
	}
	if config["workspace"]["search"] || config["workspace"]["key_inspector"] {
		t.Fatalf("expected secondary workspace modules hidden by default: %#v", config["workspace"])
	}
	if !config["account"]["personal"] || !config["account"]["enabled"] {
		t.Fatalf("expected core account modules to be enabled: %#v", config["account"])
	}
	if !config["admin"]["search_backend"] || !config["admin"]["models"] {
		t.Fatalf("expected core admin modules enabled: %#v", config["admin"])
	}
}

func TestSyncRootUserFromConfigRequiresCredentialsWhenNoRootExists(t *testing.T) {
	_ = withPricingTestDB(t, "root_bootstrap_requires_credentials")
	resetRootConfig(t)

	err := SyncRootUserFromConfig()
	if err == nil {
		t.Fatal("expected missing root bootstrap credentials to fail when no root exists")
	}
	if !strings.Contains(err.Error(), "root bootstrap credentials are required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncRootUserFromConfigSkipsMissingCredentialsWhenRootExists(t *testing.T) {
	db := withPricingTestDB(t, "root_bootstrap_existing_root")
	resetRootConfig(t)
	if err := db.Create(&User{
		Username: "existing-root",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
	}).Error; err != nil {
		t.Fatalf("failed to seed root user: %v", err)
	}

	if err := SyncRootUserFromConfig(); err != nil {
		t.Fatalf("expected existing root user to allow missing bootstrap credentials, got %v", err)
	}
}

func TestSyncRootUserFromConfigCreatesRootFromCredentials(t *testing.T) {
	db := withPricingTestDB(t, "root_bootstrap_creates_root")
	resetRootConfig(t)
	t.Setenv("ROOT_USERNAME", "root")
	t.Setenv("ROOT_PASSWORD", "change-me")

	if err := SyncRootUserFromConfig(); err != nil {
		t.Fatalf("failed to sync root user: %v", err)
	}

	var user User
	if err := db.Where("username = ?", "root").First(&user).Error; err != nil {
		t.Fatalf("failed to load root user: %v", err)
	}
	if user.Role != common.RoleRootUser || user.Status != common.UserStatusEnabled {
		t.Fatalf("unexpected root user state: role=%d status=%d", user.Role, user.Status)
	}
}

func TestSyncRootUserFromConfigRevokesSessionsForSecurityChanges(t *testing.T) {
	db := withPricingTestDB(t, "root_bootstrap_security_change")
	resetRootConfig(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := User{
		Username:       "configured-root",
		Password:       "old-root-password",
		Role:           common.RoleCommonUser,
		Status:         common.UserStatusDisabled,
		NeedsPassword:  true,
		SessionVersion: 4,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed existing user: %v", err)
	}
	if err := db.Delete(&user).Error; err != nil {
		t.Fatalf("soft delete existing user: %v", err)
	}
	t.Setenv("ROOT_USERNAME", user.Username)
	t.Setenv("ROOT_PASSWORD", "new-root-password")

	if err := SyncRootUserFromConfig(); err != nil {
		t.Fatalf("sync existing root: %v", err)
	}
	var stored User
	if err := db.Unscoped().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload synced root: %v", err)
	}
	if !common.ValidatePasswordAndHash("new-root-password", stored.Password) {
		t.Fatal("configured password was not applied")
	}
	if stored.Role != common.RoleRootUser || stored.Status != common.UserStatusEnabled || stored.DeletedAt.Valid {
		t.Fatalf("root security state not restored: role=%d status=%d deleted=%t", stored.Role, stored.Status, stored.DeletedAt.Valid)
	}
	if stored.NeedsPassword {
		t.Fatal("configured password left the first-password bypass enabled")
	}
	if stored.SessionVersion != 5 {
		t.Fatalf("security sync did not revoke sessions: version=%d", stored.SessionVersion)
	}

	if err := SyncRootUserFromConfig(); err != nil {
		t.Fatalf("idempotent root sync: %v", err)
	}
	if err := db.Unscoped().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload idempotent root: %v", err)
	}
	if stored.SessionVersion != 5 {
		t.Fatalf("unchanged root config revoked sessions again: version=%d", stored.SessionVersion)
	}
}
