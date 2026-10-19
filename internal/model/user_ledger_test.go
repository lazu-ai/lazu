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

// newLedgerTestDBs stands up two isolated in-memory databases (primary +
// log) mirroring production's coreDB/telemetryDB split, migrates the tables the
// customer-ledger aggregations touch, and restores the globals on cleanup.
func newLedgerTestDBs(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	previousDB := coreDB
	previousLogDB := telemetryDB
	previousGroupCol := commonGroupCol

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open("file:"+name+"_primary?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open primary sqlite: %v", err)
	}
	logDB, err := gorm.Open(gormsqlite.Open("file:"+name+"_logs?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open log sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = logDB
	commonGroupCol = `"group"`
	t.Cleanup(func() {
		coreDB = previousDB
		telemetryDB = previousLogDB
		commonGroupCol = previousGroupCol
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		if sqlDB, err := logDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if err := db.AutoMigrate(&User{}, &Token{}, &TopUp{}, &UserIdentity{}, &MediaJob{}, &SettlementFailure{}, &WalletLedger{}, &AdminAuditLog{}, &LedgerLegacySettlementWaiver{}); err != nil {
		t.Fatalf("migrate primary tables: %v", err)
	}
	if err := logDB.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("migrate log tables: %v", err)
	}
	return db, logDB
}

func TestGetUserIdentityProvidersDeduplicates(t *testing.T) {
	db, _ := newLedgerTestDBs(t)
	rows := []UserIdentity{
		{UserID: 1, Provider: "google", Subject: "g-1"},
		{UserID: 1, Provider: "github", Subject: "gh-1"},
		{UserID: 2, Provider: "google", Subject: "g-2"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed identities: %v", err)
	}

	providers, err := GetUserIdentityProviders([]int{1, 2, 3})
	if err != nil {
		t.Fatalf("GetUserIdentityProviders: %v", err)
	}
	if len(providers[1]) != 2 {
		t.Fatalf("expected 2 providers for user 1, got %#v", providers[1])
	}
	if len(providers[2]) != 1 || providers[2][0] != "google" {
		t.Fatalf("unexpected providers for user 2: %#v", providers[2])
	}
	if _, ok := providers[3]; ok {
		t.Fatalf("expected no entry for user 3, got %#v", providers[3])
	}
}

func TestGetUserSummaryStatsCounts(t *testing.T) {
	db, logDB := newLedgerTestDBs(t)
	now := time.Now().Unix()
	users := []User{
		// active + low balance (quota < $5, enabled, has a recent consume)
		{Username: "u1", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1_000_000, HasToppedUp: true},
		// topped up, healthy balance
		{Username: "u2", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 50_000_000, HasToppedUp: true},
		// zero balance but topped up
		{Username: "u3", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 0, HasToppedUp: true},
		// disabled customer
		{Username: "u4", Role: common.RoleCommonUser, Status: common.UserStatusDisabled, Quota: 0, HasToppedUp: false},
		// platform member
		{Username: "admin", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 0},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// u1 and u2 have recent consume; u1 is low-balance active.
	logs := []Log{
		{UserId: users[0].Id, Type: LogTypeConsume, CreatedAt: now},
		{UserId: users[1].Id, Type: LogTypeConsume, CreatedAt: now},
	}
	if err := logDB.Create(&logs).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	stats, err := GetUserSummaryStats(now - 3600)
	if err != nil {
		t.Fatalf("GetUserSummaryStats: %v", err)
	}
	if stats.TotalCustomers != 4 {
		t.Fatalf("total_customers=%d, want 4", stats.TotalCustomers)
	}
	if stats.PlatformMembers != 1 {
		t.Fatalf("platform_members=%d, want 1", stats.PlatformMembers)
	}
	if stats.Active30d != 2 {
		t.Fatalf("active_30d=%d, want 2", stats.Active30d)
	}
	if stats.ToppedUp != 3 {
		t.Fatalf("topped_up=%d, want 3", stats.ToppedUp)
	}
	if stats.LowBalanceActive != 1 {
		t.Fatalf("low_balance_active=%d, want 1", stats.LowBalanceActive)
	}
	if stats.ZeroToppedUp != 1 {
		t.Fatalf("zero_topped_up=%d, want 1", stats.ZeroToppedUp)
	}
	if stats.Disabled != 1 {
		t.Fatalf("disabled=%d, want 1", stats.Disabled)
	}
}

func TestActiveCohortsUseBoundedBatches(t *testing.T) {
	db, logDB := newLedgerTestDBs(t)
	now := time.Now().Unix()
	users := make([]User, 1205)
	for i := range users {
		users[i] = User{Username: fmt.Sprintf("active-%04d", i), Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1_000_000}
	}
	if err := db.CreateInBatches(&users, 200).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	logs := make([]Log, len(users))
	for i := range users {
		logs[i] = Log{UserId: users[i].Id, Type: LogTypeConsume, CreatedAt: now}
	}
	if err := logDB.CreateInBatches(&logs, 200).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}
	stats, err := GetUserSummaryStats(now - 60)
	if err != nil || stats.Active30d != int64(len(users)) || stats.LowBalanceActive != int64(len(users)) {
		t.Fatalf("batched summary=%#v err=%v", stats, err)
	}
	page := &common.PageInfo{Page: 2, PageSize: 20}
	got, total, err := GetAllUsersFiltered(page, UserListOptions{Filter: "active_30d"})
	if err != nil || total != int64(len(users)) || len(got) != 20 {
		t.Fatalf("batched page total=%d len=%d err=%v", total, len(got), err)
	}
}

func TestGetUserAccountUsageAggregate(t *testing.T) {
	db, logDB := newLedgerTestDBs(t)
	now := time.Now().Unix()
	internalScope := "extension-runner"
	tokens := []Token{
		{UserId: 7, Name: "visible", Key: "vis-key", LastUsedAt: 5000},
		{UserId: 7, Name: "extension-runner", Key: "internal-key", InternalScope: &internalScope, LastUsedAt: 9000},
	}
	if err := db.Create(&tokens).Error; err != nil {
		t.Fatalf("seed tokens: %v", err)
	}
	logs := []Log{
		{UserId: 7, Type: LogTypeConsume, CreatedAt: now, AmountMicrousd: 100, SessionID: "s1", ClientFramework: "codex", ClientCountry: "US"},
		{UserId: 7, Type: LogTypeConsume, CreatedAt: now, AmountMicrousd: 200, SessionID: "s1", ClientFramework: "codex", ClientCountry: "US"},
		{UserId: 7, Type: LogTypeConsume, CreatedAt: now, AmountMicrousd: 50, SessionID: "s2", ClientFramework: "curl", ClientCountry: "DE"},
		// Synthetic legacy fallback groups requests by API key; it is not an
		// operator-visible agent session.
		{UserId: 7, TokenId: 77, Type: LogTypeConsume, CreatedAt: now, AmountMicrousd: 25, SessionID: "token-77"},
		// An explicit caller session may legitimately start with token-. It is
		// real unless it exactly equals the generated token-<token_id> fallback.
		{UserId: 7, TokenId: 77, Type: LogTypeConsume, CreatedAt: now, AmountMicrousd: 10, SessionID: "token-team"},
		// outside window; must be ignored
		{UserId: 7, Type: LogTypeConsume, CreatedAt: now - 999999, AmountMicrousd: 999, SessionID: "old"},
	}
	if err := logDB.Create(&logs).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	agg, err := GetUserAccountUsageAggregate(7, now-3600)
	if err != nil {
		t.Fatalf("GetUserAccountUsageAggregate: %v", err)
	}
	if agg.RequestCount != 5 {
		t.Fatalf("request_count=%d, want 5", agg.RequestCount)
	}
	if agg.RecentSpend != 385 {
		t.Fatalf("recent_spend=%d, want 385", agg.RecentSpend)
	}
	// last_used_at is the newest consume-log timestamp (all-time), not the
	// token accessed_time (5000/9000). The in-window logs are the newest.
	if agg.LastUsedAt != now {
		t.Fatalf("last_used_at=%d, want %d (newest consume log)", agg.LastUsedAt, now)
	}
	if agg.AgentSessions != 3 {
		t.Fatalf("agent_sessions=%d, want 3", agg.AgentSessions)
	}
	if len(agg.ClientBreakdown) != 2 || agg.ClientBreakdown[0].Framework != "codex" || agg.ClientBreakdown[0].Count != 2 {
		t.Fatalf("unexpected client breakdown: %#v", agg.ClientBreakdown)
	}
	if len(agg.Countries) != 2 || agg.Countries[0].Country != "US" || agg.Countries[0].Count != 2 {
		t.Fatalf("unexpected country breakdown: %#v", agg.Countries)
	}
}

// TestUsageSummaryClientTopRanksByPair proves the surfaced client_top is a
// (framework,country) pair that actually co-occurred, not a phantom built
// from two independent modes. Here the framework mode is "codex" (5) and
// the country mode is "JP" (4), but that pair never occurred — the real top
// pair is (curl, JP) at 4.
func TestUsageSummaryClientTopRanksByPair(t *testing.T) {
	_, logDB := newLedgerTestDBs(t)
	now := time.Now().Unix()
	logs := []Log{
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "codex", ClientCountry: "US"},
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "codex", ClientCountry: "US"},
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "codex", ClientCountry: "US"},
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "codex", ClientCountry: "DE"},
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "codex", ClientCountry: "DE"},
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "curl", ClientCountry: "JP"},
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "curl", ClientCountry: "JP"},
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "curl", ClientCountry: "JP"},
		{UserId: 12, Type: LogTypeConsume, CreatedAt: now, ClientFramework: "curl", ClientCountry: "JP"},
	}
	if err := logDB.Create(&logs).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	summaries, err := GetUserUsageSummaries([]int{12}, now-3600)
	if err != nil {
		t.Fatalf("GetUserUsageSummaries: %v", err)
	}
	got := summaries[12]
	if got.TopFramework != "curl" || got.TopCountry != "JP" {
		t.Fatalf("expected top pair curl/JP, got %q/%q", got.TopFramework, got.TopCountry)
	}
}

func TestGetAllUsersFilteredCohorts(t *testing.T) {
	db, logDB := newLedgerTestDBs(t)
	now := time.Now().Unix()
	users := []User{
		{Username: "low-active", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1_000_000, FirstRequestAt: 1_700_000_000},
		{Username: "low-idle", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1_000_000},
		{Username: "zero-topped", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 0, HasToppedUp: true, FirstRequestAt: 1_700_000_000},
		{Username: "admin", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 0},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// Only low-active has a recent consume.
	if err := logDB.Create(&Log{UserId: users[0].Id, Type: LogTypeConsume, CreatedAt: now}).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	page := &common.PageInfo{Page: 1, PageSize: 50}

	got, total, err := GetAllUsersFiltered(page, UserListOptions{Filter: "low_balance_active"})
	if err != nil {
		t.Fatalf("low_balance_active: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].Username != "low-active" {
		t.Fatalf("low_balance_active returned total=%d users=%#v", total, got)
	}

	got, total, err = GetAllUsersFiltered(page, UserListOptions{Filter: "active_30d"})
	if err != nil {
		t.Fatalf("active_30d: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].Username != "low-active" {
		t.Fatalf("active_30d returned total=%d users=%#v", total, got)
	}

	got, total, err = GetAllUsersFiltered(page, UserListOptions{Filter: "topped_up"})
	if err != nil {
		t.Fatalf("topped_up: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].Username != "zero-topped" {
		t.Fatalf("topped_up returned total=%d users=%#v", total, got)
	}

	got, total, err = GetAllUsersFiltered(page, UserListOptions{Filter: "zero_topped_up"})
	if err != nil {
		t.Fatalf("zero_topped_up: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].Username != "zero-topped" {
		t.Fatalf("zero_topped_up returned total=%d users=%#v", total, got)
	}

	got, total, err = GetAllUsersFiltered(page, UserListOptions{Filter: "never_called"})
	if err != nil {
		t.Fatalf("never_called: %v", err)
	}
	// Cohort filters are customers-only, so the admin (also
	// never called) is excluded; only low-idle qualifies.
	if total != 1 || len(got) != 1 || got[0].Username != "low-idle" {
		t.Fatalf("never_called total=%d, want 1 low-idle (users=%#v)", total, got)
	}

	got, total, err = GetAllUsersFiltered(page, UserListOptions{Role: "100"})
	if err != nil {
		t.Fatalf("role filter: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].Username != "admin" {
		t.Fatalf("role=100 returned total=%d users=%#v", total, got)
	}

	deleted := User{Username: "deleted-customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if err := db.Create(&deleted).Error; err != nil {
		t.Fatalf("seed deleted customer: %v", err)
	}
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("soft-delete customer: %v", err)
	}
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Role: "1", ExcludeDeleted: true})
	if err != nil || total != 3 || len(got) != 3 {
		t.Fatalf("exclude_deleted total=%d users=%#v err=%v, want 3 live customers", total, got, err)
	}
	_, total, err = GetAllUsersFiltered(page, UserListOptions{Role: "1"})
	if err != nil || total != 4 {
		t.Fatalf("legacy unscoped total=%d err=%v, want 4 customers", total, err)
	}
}

func TestGetAllUsersFilteredIdentityProvider(t *testing.T) {
	db, _ := newLedgerTestDBs(t)
	previousEnabled := common.EmailCodeAuthEnabled
	previousPassword := common.PasswordLoginEnabled
	previousResend := common.AppConfig.Resend.APIKey
	previousGoogleID, previousGoogleSecret := common.GoogleOAuthClientID, common.GoogleOAuthClientSecret
	previousGitHubID, previousGitHubSecret := common.GitHubOAuthClientID, common.GitHubOAuthClientSecret
	previousOIDCEnabled := common.OIDCEnabled
	previousOIDCIssuer, previousOIDCID, previousOIDCSecret := common.OIDCIssuerURL, common.OIDCClientID, common.OIDCClientSecret
	common.EmailCodeAuthEnabled = true
	common.PasswordLoginEnabled = true
	common.AppConfig.Resend.APIKey = "test-resend-key"
	common.GoogleOAuthClientID, common.GoogleOAuthClientSecret = "google-client", "google-secret"
	common.GitHubOAuthClientID, common.GitHubOAuthClientSecret = "", ""
	common.OIDCEnabled = true
	common.OIDCIssuerURL, common.OIDCClientID, common.OIDCClientSecret = "https://id.example.com", "oidc-client", "oidc-secret"
	t.Cleanup(func() {
		common.EmailCodeAuthEnabled = previousEnabled
		common.PasswordLoginEnabled = previousPassword
		common.AppConfig.Resend.APIKey = previousResend
		common.GoogleOAuthClientID, common.GoogleOAuthClientSecret = previousGoogleID, previousGoogleSecret
		common.GitHubOAuthClientID, common.GitHubOAuthClientSecret = previousGitHubID, previousGitHubSecret
		common.OIDCEnabled = previousOIDCEnabled
		common.OIDCIssuerURL, common.OIDCClientID, common.OIDCClientSecret = previousOIDCIssuer, previousOIDCID, previousOIDCSecret
	})
	users := []User{
		{Username: "google-user", Role: common.RoleCommonUser, Email: "google@example.com", NeedsPassword: true},
		{Username: "github-user", Role: common.RoleCommonUser, NeedsPassword: true},
		{Username: "oidc-user", Role: common.RoleCommonUser, NeedsPassword: true},
		{Username: "password-user", Role: common.RoleCommonUser},
		{Username: "email-code-user", Role: common.RoleCommonUser, Email: "code@example.com", NeedsPassword: false},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if err := db.Create(&[]UserIdentity{
		{UserID: users[0].Id, Provider: "google", Subject: "g-1"},
		{UserID: users[1].Id, Provider: "github", Subject: "gh-1"},
		{UserID: users[2].Id, Provider: "oidc", Subject: "oidc-1"},
	}).Error; err != nil {
		t.Fatalf("seed identities: %v", err)
	}
	page := &common.PageInfo{Page: 1, PageSize: 20}

	got, total, err := GetAllUsersFiltered(page, UserListOptions{Provider: "google"})
	if err != nil || total != 1 || len(got) != 1 || got[0].Username != "google-user" {
		t.Fatalf("provider=google total=%d users=%#v err=%v", total, got, err)
	}
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Provider: "oidc"})
	if err != nil || total != 1 || len(got) != 1 || got[0].Username != "oidc-user" {
		t.Fatalf("provider=oidc total=%d users=%#v err=%v", total, got, err)
	}
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Provider: "email"})
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("provider=email total=%d users=%#v err=%v", total, got, err)
	}
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Provider: "password"})
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("provider=password total=%d users=%#v err=%v", total, got, err)
	}
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Provider: "unbound"})
	if err != nil || total != 1 || len(got) != 1 || got[0].Username != "github-user" {
		t.Fatalf("provider=unbound total=%d users=%#v err=%v", total, got, err)
	}
	common.EmailCodeAuthEnabled = false
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Provider: "email"})
	if err != nil || total != 0 || len(got) != 0 {
		t.Fatalf("disabled email capability total=%d users=%#v err=%v", total, got, err)
	}
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Provider: "unbound"})
	if err != nil || total != 1 || len(got) != 1 || got[0].Username != "github-user" {
		t.Fatalf("disabled email capability unbound total=%d users=%#v err=%v", total, got, err)
	}
	common.PasswordLoginEnabled = false
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Provider: "unbound"})
	if err != nil || total != 3 || len(got) != 3 {
		t.Fatalf("disabled email/password capability unbound total=%d users=%#v err=%v", total, got, err)
	}
	common.GitHubOAuthClientID, common.GitHubOAuthClientSecret = "github-client", "github-secret"
	got, total, err = GetAllUsersFiltered(page, UserListOptions{Provider: "unbound"})
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("re-enabled historical OAuth capability unbound total=%d users=%#v err=%v", total, got, err)
	}
}

func TestGetUserTokensAdminExcludesInternalAndPaginates(t *testing.T) {
	db, _ := newLedgerTestDBs(t)
	internalScope := "extension-runner"
	tokens := []Token{
		{UserId: 3, Name: "a", Key: "key-a"},
		{UserId: 3, Name: "b", Key: "key-b"},
		{UserId: 3, Name: "extension-runner", Key: "key-internal", InternalScope: &internalScope},
		{UserId: 4, Name: "other", Key: "key-other"},
	}
	if err := db.Create(&tokens).Error; err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	got, total, err := GetUserTokensAdmin(3, 0, 10)
	if err != nil {
		t.Fatalf("GetUserTokensAdmin: %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Fatalf("expected 2 visible tokens for user 3, got total=%d len=%d", total, len(got))
	}
	for _, tok := range got {
		if tok.IsInternal() {
			t.Fatalf("internal token leaked into admin listing: %#v", tok)
		}
	}
}

func TestUserAccountOperationalAggregations(t *testing.T) {
	db, logDB := newLedgerTestDBs(t)
	if err := db.Create(&[]User{
		{Id: 5, Username: "owner-five", Role: common.RoleCommonUser},
		{Id: 6, Username: "owner-six", Role: common.RoleCommonUser},
	}).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if err := db.Create(&[]TopUp{
		{UserId: 5, TradeNo: "t-ok-1", Status: common.TopUpStatusSuccess, CreditedMicrousd: 1000, CompleteTime: 150},
		{UserId: 5, TradeNo: "t-ok-2", Status: common.TopUpStatusSuccess, CreditedMicrousd: 2500, CompleteTime: 150},
		{UserId: 5, TradeNo: "t-legacy", Status: common.TopUpStatusSuccess, Amount: 1, CompleteTime: 150},
		{UserId: 5, TradeNo: "t-pending", Status: "pending", CreditedMicrousd: 9999},
		{UserId: 6, TradeNo: "t-other-owner", Status: common.TopUpStatusSuccess, CreditedMicrousd: 777},
	}).Error; err != nil {
		t.Fatalf("seed topups: %v", err)
	}
	if err := db.Create(&[]MediaJob{
		{PublicID: "mj-1", UserID: 5, Source: testExtensionMediaSource, ModelName: "veo", Kind: "video", Status: MediaJobStatusSucceeded},
		{PublicID: "mj-2", UserID: 5, Source: testExtensionMediaSource, ModelName: "veo", Kind: "video", Status: MediaJobStatusFailed},
		{PublicID: "mj-api", UserID: 5, Source: MediaJobSourceAPI, ModelName: "veo", Kind: "video", Status: MediaJobStatusFailed},
		{PublicID: "mj-3", UserID: 6, Source: testExtensionMediaSource, ModelName: "veo", Kind: "video", Status: MediaJobStatusFailed},
	}).Error; err != nil {
		t.Fatalf("seed media jobs: %v", err)
	}
	if err := db.Create(&[]SettlementFailure{
		{UserID: 5, PricingVersion: 1, QuotaDelta: 10, Status: SettlementFailureStatusOpen},
		{UserID: 5, PricingVersion: 1, QuotaDelta: 10, Status: SettlementFailureStatusRetrying},
		{UserID: 5, PricingVersion: 1, QuotaDelta: 10, Status: SettlementFailureStatusManualReview},
		{UserID: 5, PricingVersion: 1, QuotaDelta: 10, Status: SettlementFailureStatusResolved},
	}).Error; err != nil {
		t.Fatalf("seed settlement failures: %v", err)
	}

	topup, err := GetUserTopupTotalMicrousd(5)
	if err != nil || topup != 1_003_500 {
		t.Fatalf("GetUserTopupTotalMicrousd=%d err=%v, want 1003500", topup, err)
	}
	summaries, err := GetUserUsageSummaries([]int{5}, time.Now().Add(-30*24*time.Hour).Unix())
	if err != nil || summaries[5].TopupTotalMicrousd != 1_003_500 {
		t.Fatalf("GetUserUsageSummaries topup=%d err=%v, want 1003500", summaries[5].TopupTotalMicrousd, err)
	}
	total, failed, err := GetUserMediaJobCounts(5)
	if err != nil || total != 3 || failed != 2 {
		t.Fatalf("GetUserMediaJobCounts total=%d failed=%d err=%v, want 3/2", total, failed, err)
	}
	unresolved, err := GetUserUnresolvedSettlementFailureCount(5)
	if err != nil || unresolved != 3 {
		t.Fatalf("GetUserUnresolvedSettlementFailureCount=%d err=%v, want 3", unresolved, err)
	}

	page := &common.PageInfo{Page: 1, PageSize: 20}
	topups, total, err := SearchAllTopUps("", "owner-five", page)
	if err != nil || total != 4 || len(topups) != 4 {
		t.Fatalf("SearchAllTopUps owner total=%d rows=%#v err=%v, want 4", total, topups, err)
	}
	for _, row := range topups {
		if row.UserId != 5 {
			t.Fatalf("owner filter leaked user %d: %#v", row.UserId, row)
		}
	}

	if err := logDB.Create(&[]Log{
		{UserId: 5, Username: "owner-five", Type: LogTypeConsume, CreatedAt: 100, AmountMicrousd: 100},
		{UserId: 5, Username: "renamed-owner", Type: LogTypeConsume, CreatedAt: 200, AmountMicrousd: 200},
		{UserId: 6, Username: "owner-five", Type: LogTypeConsume, CreatedAt: 300, AmountMicrousd: 900},
	}).Error; err != nil {
		t.Fatalf("seed immutable-id logs: %v", err)
	}
	logs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "owner-five", 5, "", 0, 20, 0, "")
	if err != nil || total != 2 || len(logs) != 2 {
		t.Fatalf("GetAllLogs user_id total=%d rows=%#v err=%v, want 2", total, logs, err)
	}
	stat := SumUsedQuota(LogTypeUnknown, 0, 0, "", "owner-five", 5, "", 0, "")
	if stat.AmountMicrousd != 300 {
		t.Fatalf("SumUsedQuota immutable user id=%d, want 300", stat.AmountMicrousd)
	}

	if err := db.Create(&[]WalletLedger{
		{UserID: 5, DeltaMicrousd: 1_000, Kind: WalletLedgerKindTopup, RefType: "topup", RefID: "topup-1", PricingVersion: 1, CreatedAt: time.Unix(100, 0)},
		{UserID: 5, DeltaMicrousd: 250, Kind: WalletLedgerKindRefund, RefType: "media", RefID: "refund-1", PricingVersion: 1, CreatedAt: time.Unix(200, 0)},
		{UserID: 5, DeltaMicrousd: -100, Kind: WalletLedgerKindSpend, RefType: "relay", RefID: "spend-1", PricingVersion: 1, CreatedAt: time.Unix(300, 0)},
		{UserID: 5, DeltaMicrousd: 9_999, Kind: WalletLedgerKindAdjustment, RefType: openingBalanceRefType, RefID: "user:5", PricingVersion: 1, CreatedAt: time.Unix(350, 0)},
		{UserID: 6, DeltaMicrousd: 999, Kind: WalletLedgerKindRefund, RefType: "media", RefID: "other-user-refund", PricingVersion: 1, CreatedAt: time.Unix(400, 0)},
	}).Error; err != nil {
		t.Fatalf("seed wallet ledger: %v", err)
	}
	events, err := GetUserWalletLedgerEvents(5, 8)
	if err != nil || len(events) != 2 || events[0].Kind != WalletLedgerKindRefund || events[1].Kind != WalletLedgerKindTopup {
		t.Fatalf("GetUserWalletLedgerEvents rows=%#v err=%v, want refund then topup", events, err)
	}
}

func TestGetUserWalletLedgerEventsPreservesPreLedgerHistory(t *testing.T) {
	db, logDB := newLedgerTestDBs(t)
	if err := db.Create(&TopUp{UserId: 5, TradeNo: "legacy-topup", Status: common.TopUpStatusSuccess, CreditedMicrousd: 700, CompleteTime: 50}).Error; err != nil {
		t.Fatalf("seed legacy topup: %v", err)
	}
	if err := logDB.Create(&[]Log{
		{UserId: 5, Type: LogTypeManage, CreatedAt: 60, Content: "管理员调整用户余额 ＄-0.000025 额度（原因：测试）"},
		{UserId: 5, Type: LogTypeRefund, CreatedAt: 120, AmountMicrousd: 999},
	}).Error; err != nil {
		t.Fatalf("seed legacy logs: %v", err)
	}
	if err := db.Create(&WalletLedger{UserID: 5, DeltaMicrousd: 100, Kind: WalletLedgerKindRefund, RefType: "media", RefID: "new-refund", PricingVersion: 1, CreatedAt: time.Unix(100, 0)}).Error; err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	events, err := GetUserWalletLedgerEvents(5, 8)
	if err != nil || len(events) != 3 {
		t.Fatalf("events=%#v err=%v, want current + two pre-ledger events", events, err)
	}
	if events[0].ID[:7] != "wallet:" || events[1].ID[:4] != "log:" || events[2].ID[:6] != "topup:" {
		t.Fatalf("unexpected merged order/ids: %#v", events)
	}
	if events[1].DeltaMicrousd != -25 {
		t.Fatalf("legacy management delta=%d, want -25", events[1].DeltaMicrousd)
	}
}

func TestGetUserWalletLedgerEventsIncludesTargetedAccountAudit(t *testing.T) {
	db, _ := newLedgerTestDBs(t)
	user := User{PublicID: "usr_customer", Username: "customer", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Create(&WalletLedger{UserID: user.Id, DeltaMicrousd: 500, Kind: WalletLedgerKindTopup, RefType: "topup", RefID: "one", PricingVersion: 1, CreatedAt: time.Unix(100, 0)}).Error; err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	if err := db.Create(&[]AdminAuditLog{
		{PublicID: "aud_profile", ActorUsername: "operator", Action: "user.update", TargetType: "user", TargetID: user.PublicID, CreatedAt: time.Unix(200, 0)},
		{PublicID: "aud_disable", ActorUsername: "root", Action: "user.manage.disable", TargetType: "user", TargetID: user.PublicID, CreatedAt: time.Unix(300, 0)},
		{PublicID: "aud_wallet", ActorUsername: "operator", Action: "user.quota.adjust", TargetType: "user", TargetID: user.PublicID, CreatedAt: time.Unix(400, 0)},
		{PublicID: "aud_other", ActorUsername: "operator", Action: "user.update", TargetType: "user", TargetID: "usr_other", CreatedAt: time.Unix(500, 0)},
	}).Error; err != nil {
		t.Fatalf("seed audit rows: %v", err)
	}

	events, err := GetUserWalletLedgerEvents(user.Id, 8)
	if err != nil || len(events) != 3 {
		t.Fatalf("events=%#v err=%v, want two account audits and one wallet row", events, err)
	}
	if events[0].ID != "audit:aud_disable" || events[0].Action != "user.manage.disable" || events[0].ActorUsername != "root" {
		t.Fatalf("latest account audit=%#v", events[0])
	}
	if events[1].ID != "audit:aud_profile" || events[1].Action != "user.update" {
		t.Fatalf("profile audit=%#v", events[1])
	}
	if events[2].Kind != WalletLedgerKindTopup || events[2].DeltaMicrousd != 500 {
		t.Fatalf("wallet row=%#v", events[2])
	}
}

func TestLegacyManagementDeltaMicrousd(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    billing.MicroUSD
		ok      bool
	}{
		{name: "direct usd", content: "管理员调整用户余额 ＄-1.250000 额度（原因：退款）", want: -1_250_000, ok: true},
		{name: "set usd", content: "管理员将用户额度从 ＄2.000000 额度修改为 ＄5.500000 额度", want: 3_500_000, ok: true},
		{name: "direct points", content: "管理员调整用户余额 -125 点额度", want: -125, ok: true},
		{name: "set points", content: "管理员将用户额度从 200 点额度修改为 125 点额度", want: -75, ok: true},
		{name: "unrelated", content: "管理员更新用户资料", want: 0, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := legacyManagementDeltaMicrousd(test.content)
			if got != test.want || ok != test.ok {
				t.Fatalf("legacyManagementDeltaMicrousd(%q)=(%d,%v), want (%d,%v)", test.content, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestGetAllUsersFilteredCreditOrder(t *testing.T) {
	db, logDB := newLedgerTestDBs(t)
	now := time.Now().Unix()
	// Lifetime credit is balance plus spend, whether it came from a top-up,
	// an admin grant or a bonus; a manually funded account ranks by it too.
	users := []User{
		{Username: "small", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 5_000_000, UsedQuota: 15_000_000},
		{Username: "never", Role: common.RoleCommonUser, Status: common.UserStatusEnabled},
		{Username: "manual-whale", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 100_000_000, UsedQuota: 900_000_000},
		{Username: "spent-out", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 0, UsedQuota: 50_000_000},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	want := []string{"manual-whale", "spent-out", "small", "never"}
	page := &common.PageInfo{Page: 1, PageSize: 50}

	got, _, err := GetAllUsersFiltered(page, UserListOptions{Order: "credit_desc"})
	if err != nil {
		t.Fatalf("credit_desc: %v", err)
	}
	assertUsernames(t, "credit_desc", got, want)

	// The active cohort sorts in memory across log batches; it must agree.
	for _, user := range users {
		if err := logDB.Create(&Log{UserId: user.Id, Type: LogTypeConsume, CreatedAt: now}).Error; err != nil {
			t.Fatalf("seed logs: %v", err)
		}
	}
	got, _, err = GetAllUsersFiltered(page, UserListOptions{Order: "credit_desc", Filter: "active_30d"})
	if err != nil {
		t.Fatalf("active credit_desc: %v", err)
	}
	assertUsernames(t, "active credit_desc", got, want)
}

func assertUsernames(t *testing.T, label string, got []*User, want []string) {
	t.Helper()
	names := make([]string, 0, len(got))
	for _, user := range got {
		names = append(names, user.Username)
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("%s order = %v, want %v", label, names, want)
	}
}
