package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupEphemeralCredentialTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB := coreDB
	originalCryptoSecret := common.CryptoSecret
	originalRedisEnabled := common.RedisEnabled
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	common.CryptoSecret = "ephemeral-credential-test-secret" // secret-scan: allow -- test fixture
	common.RedisEnabled = false
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&User{}, &Token{}, &EphemeralCredential{}, &EphemeralCredentialAudit{}); err != nil {
		t.Fatalf("failed to migrate ephemeral credential tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = originalDB
		common.CryptoSecret = originalCryptoSecret
		common.RedisEnabled = originalRedisEnabled
	})
	return db
}

func TestIssueEphemeralCredentialStoresDigestAndValidatesTTL(t *testing.T) {
	db := setupEphemeralCredentialTestDB(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&User{Id: 1, Username: "ephemeral-user", Status: common.UserStatusEnabled, Quota: billing.MicroUSD(1_000)}).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	token := &Token{
		Id:          2,
		UserId:      1,
		Key:         "ephemeralbasetoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: billing.MicroUSD(1_000),
		Group:       ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}

	result, err := IssueEphemeralCredential(IssueEphemeralCredentialInput{
		UserID:     1,
		TokenID:    2,
		SessionID:  "sess-credential",
		AgentID:    "codex",
		Models:     []string{"GPT-4O"},
		Providers:  []string{"OpenAI"},
		TTLSeconds: 60,
		Now:        now,
	})
	if err != nil {
		t.Fatalf("IssueEphemeralCredential error: %v", err)
	}
	if !strings.HasPrefix(result.Credential, EphemeralCredentialPrefix) {
		t.Fatalf("unexpected credential prefix: %q", result.Credential)
	}

	var stored EphemeralCredential
	if err := db.First(&stored, result.Record.ID).Error; err != nil {
		t.Fatalf("failed to load stored credential: %v", err)
	}
	if stored.CredentialDigest == "" || strings.Contains(stored.CredentialDigest, result.Credential) {
		t.Fatalf("stored credential must contain only digest, got %+v", stored)
	}
	if stored.ExpiresAt != now.Unix()+60 || stored.SessionID != "sess-credential" {
		t.Fatalf("unexpected stored credential fields: %+v", stored)
	}
	if _, _, err := ValidateEphemeralCredentialAt(result.Credential, now.Add(30*time.Second)); err != nil {
		t.Fatalf("credential should validate before expiry: %v", err)
	}
	if _, _, err := ValidateEphemeralCredentialAt(result.Credential, now.Add(61*time.Second)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expiry error, got %v", err)
	}

	var expiredAudit EphemeralCredentialAudit
	if err := db.Where("event_type = ?", EphemeralCredentialAuditExpired).First(&expiredAudit).Error; err != nil {
		t.Fatalf("expected expired audit row: %v", err)
	}
}

func TestValidateEphemeralCredentialRequestScopesModelProviderAndSession(t *testing.T) {
	credential := &EphemeralCredential{
		Scope:     EphemeralCredentialScopeRelay,
		SessionID: "sess-bound",
		Models:    StringSlice{"gpt-4o"},
		Providers: StringSlice{"openai"},
	}
	if err := ValidateEphemeralCredentialRequest(credential, "gpt-4o", "OpenAI", "sess-bound"); err != nil {
		t.Fatalf("expected matching request to pass: %v", err)
	}
	cases := []struct {
		name      string
		modelName string
		provider  string
		sessionID string
	}{
		{name: "model", modelName: "claude-sonnet", provider: "OpenAI", sessionID: "sess-bound"},
		{name: "provider", modelName: "gpt-4o", provider: "Anthropic", sessionID: "sess-bound"},
		{name: "session", modelName: "gpt-4o", provider: "OpenAI", sessionID: "other-session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEphemeralCredentialRequest(credential, tc.modelName, tc.provider, tc.sessionID)
			if err == nil {
				t.Fatalf("expected request to be denied")
			}
		})
	}
}
