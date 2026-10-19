package model

import (
	"strconv"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestSearchUsersMatchesPublicID(t *testing.T) {
	previousDB := coreDB
	previousLogDB := telemetryDB
	previousCommonGroupCol := commonGroupCol
	defer func() {
		coreDB = previousDB
		telemetryDB = previousLogDB
		commonGroupCol = previousCommonGroupCol
	}()

	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(
		gormsqlite.Open("file:"+dbName+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = db
	commonGroupCol = `"group"`
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	}()

	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatalf("failed to migrate users: %v", err)
	}
	user := User{
		PublicID: "usr_public_member",
		Username: "member",
		Password: "password",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	users, total, err := SearchUsers(user.PublicID, 0, 10)
	if err != nil {
		t.Fatalf("SearchUsers returned error: %v", err)
	}
	if total != 1 || len(users) != 1 || users[0].PublicID != user.PublicID {
		t.Fatalf("expected public ID match, got total=%d users=%#v", total, users)
	}

	users, total, err = SearchUsers("#"+strconv.Itoa(user.Id), 0, 10)
	if err != nil {
		t.Fatalf("SearchUsers by displayed ID returned error: %v", err)
	}
	if total != 1 || len(users) != 1 || users[0].Id != user.Id {
		t.Fatalf("expected #ID match for user %d, got total=%d users=%#v", user.Id, total, users)
	}
}

func TestGetUserUsageSummariesUsesLogDBAndHidesInternalTokens(t *testing.T) {
	previousDB := coreDB
	previousLogDB := telemetryDB
	previousCommonGroupCol := commonGroupCol
	defer func() {
		coreDB = previousDB
		telemetryDB = previousLogDB
		commonGroupCol = previousCommonGroupCol
	}()

	db, err := gorm.Open(gormsqlite.Open("file:user_usage_primary?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open primary sqlite: %v", err)
	}
	logDB, err := gorm.Open(gormsqlite.Open("file:user_usage_logs?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open log sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = logDB
	commonGroupCol = `"group"`
	defer func() {
		primarySQL, _ := db.DB()
		_ = primarySQL.Close()
		logSQL, _ := logDB.DB()
		_ = logSQL.Close()
	}()

	if err := db.AutoMigrate(&Token{}, &TopUp{}, &UserIdentity{}); err != nil {
		t.Fatalf("failed to migrate primary tables: %v", err)
	}
	if err := logDB.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("failed to migrate logs: %v", err)
	}
	internalScope := "extension-runner"
	tokens := []Token{
		{UserId: 9, Name: "visible", Key: "visible-token", LastUsedAt: 1700},
		{UserId: 9, Name: "extension-runner", Key: "extension-token", InternalScope: &internalScope, LastUsedAt: 9999},
	}
	if err := db.Create(&tokens).Error; err != nil {
		t.Fatalf("failed to create tokens: %v", err)
	}
	if err := db.Create(&TopUp{UserId: 9, TradeNo: "topup-success", Status: common.TopUpStatusSuccess, CreditedMicrousd: 5000}).Error; err != nil {
		t.Fatalf("failed to create topup: %v", err)
	}
	if err := db.Create(&TopUp{UserId: 9, TradeNo: "topup-pending", Status: "pending", CreditedMicrousd: 111}).Error; err != nil {
		t.Fatalf("failed to create pending topup: %v", err)
	}
	now := time.Now().Unix()
	logs := []Log{
		{UserId: 9, Type: LogTypeConsume, CreatedAt: now - 20, AmountMicrousd: 321, ClientFramework: "codex", ClientCountry: "US"},
		{UserId: 9, Type: LogTypeConsume, CreatedAt: now - 10, AmountMicrousd: 100, ClientFramework: "codex", ClientCountry: "US"},
		{UserId: 9, Type: LogTypeConsume, CreatedAt: now, AmountMicrousd: 50, ClientFramework: "openai-python", ClientCountry: "DE"},
	}
	if err := logDB.Create(&logs).Error; err != nil {
		t.Fatalf("failed to create logs: %v", err)
	}

	summaries, err := GetUserUsageSummaries([]int{9}, time.Now().Add(-time.Hour).Unix())
	if err != nil {
		t.Fatalf("GetUserUsageSummaries returned error: %v", err)
	}
	got := summaries[9]
	if got.TokenCount != 1 || got.SpendMicrousd != 471 {
		t.Fatalf("unexpected token/spend summary: %#v", got)
	}
	if got.RequestCount != 3 {
		t.Fatalf("expected 3 consume requests, got %d", got.RequestCount)
	}
	// last_used_at is the newest consume-log timestamp, never a token's
	// accessed_time (which is seeded to creation time: 1700/9999 here).
	if got.LastUsedAt != now {
		t.Fatalf("expected last_used_at %d from newest consume log, got %d", now, got.LastUsedAt)
	}
	if got.TopupTotalMicrousd != 5000 {
		t.Fatalf("expected only successful top-ups summed, got %d", got.TopupTotalMicrousd)
	}
	if got.TopFramework != "codex" || got.TopCountry != "US" {
		t.Fatalf("unexpected mode framework/country: %q/%q", got.TopFramework, got.TopCountry)
	}
}

func TestHardDeleteUserRemovesCredentialRows(t *testing.T) {
	previousDB := coreDB
	previousRedisEnabled := common.RedisEnabled
	previousCommonKeyCol := commonKeyCol
	defer func() {
		coreDB = previousDB
		common.RedisEnabled = previousRedisEnabled
		commonKeyCol = previousCommonKeyCol
	}()

	db, err := gorm.Open(gormsqlite.Open("file:hard_delete_user?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	common.RedisEnabled = false
	commonKeyCol = "`key`"
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	}()
	if err := db.AutoMigrate(&User{}, &Token{}, &UserIdentity{}); err != nil {
		t.Fatalf("failed to migrate user tables: %v", err)
	}
	user := User{Username: "delete-me", Password: "password", Role: common.RoleCommonUser}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if err := db.Create(&Token{UserId: user.Id, Name: "credential", Key: "delete-token"}).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if err := db.Create(&UserIdentity{
		UserID:   user.Id,
		Provider: "test-provider",
		Subject:  "hard-delete-subject",
		Email:    "delete-me@example.com",
	}).Error; err != nil {
		t.Fatalf("failed to create identity: %v", err)
	}

	deleted, err := HardDeleteUserAuthorized(user.Id, common.RoleRootUser, common.RoleCommonUser)
	if err != nil {
		t.Fatalf("HardDeleteUserAuthorized returned error: %v", err)
	}
	if deleted.Id != user.Id || deleted.Role != common.RoleCommonUser || deleted.Password != "" {
		t.Fatalf("unexpected deleted user snapshot: %#v", deleted)
	}
	var userCount int64
	var tokenCount int64
	var identityCount int64
	if err := db.Unscoped().Model(&User{}).Where("id = ?", user.Id).Count(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := db.Unscoped().Model(&Token{}).Where("user_id = ?", user.Id).Count(&tokenCount).Error; err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if err := db.Unscoped().Model(&UserIdentity{}).Where("user_id = ?", user.Id).Count(&identityCount).Error; err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if userCount != 0 || tokenCount != 0 || identityCount != 0 {
		t.Fatalf("expected user and credentials removed, users=%d tokens=%d identities=%d", userCount, tokenCount, identityCount)
	}
}
