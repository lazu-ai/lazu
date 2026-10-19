package model

import (
	"fmt"
	"strings"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTokenConsumeLogFilterTestDB(t *testing.T) func() {
	t.Helper()

	previousDB := coreDB
	previousLogDB := telemetryDB

	db, err := gorm.Open(
		gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = db

	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("failed to migrate logs: %v", err)
	}

	return func() {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
		coreDB = previousDB
		telemetryDB = previousLogDB
	}
}

func TestGetTokenConsumeLogsByTokenIDWithFilters_KeywordRespectsTokenBoundary(t *testing.T) {
	cleanup := setupTokenConsumeLogFilterTestDB(t)
	defer cleanup()

	seedLogs := []*Log{
		{
			Id:        1,
			TokenId:   1,
			Type:      LogTypeConsume,
			CreatedAt: 100,
			ModelName: "sora-2",
			Content:   "same token sora",
			RequestID: "req-a",
		},
		{
			Id:        2,
			TokenId:   1,
			Type:      LogTypeConsume,
			CreatedAt: 101,
			ModelName: "gpt-image-1.5",
			Content:   "same token non-match",
			RequestID: "req-b",
		},
		{
			Id:        3,
			TokenId:   2,
			Type:      LogTypeConsume,
			CreatedAt: 102,
			ModelName: "gpt-image-1.5",
			Content:   "sora keyword in other token",
			RequestID: "req-c",
		},
	}
	if err := telemetryDB.Create(&seedLogs).Error; err != nil {
		t.Fatalf("failed to seed logs: %v", err)
	}

	logs, total, err := GetTokenConsumeLogsByTokenIDWithFilters(
		1,
		0,
		0,
		0,
		20,
		TokenConsumeLogFilter{
			Keyword: "sora",
		},
	)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if total != 1 {
		t.Fatalf("expected total=1, got total=%d", total)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].TokenId != 1 || logs[0].ModelName != "sora-2" {
		t.Fatalf("unexpected log returned: token_id=%d, model=%s", logs[0].TokenId, logs[0].ModelName)
	}
}

func TestSearchLogsFindsRequestOutsideCurrentPageWithoutCrossingUserBoundary(t *testing.T) {
	cleanup := setupTokenConsumeLogFilterTestDB(t)
	defer cleanup()

	seedLogs := []*Log{
		{
			Id:        11,
			RequestID: "req-health-deep-link",
			UserId:    1,
			Type:      LogTypeConsume,
			Content:   "older receipt",
		},
		{
			Id:        12,
			RequestID: "req-other-user",
			UserId:    2,
			Type:      LogTypeConsume,
			Content:   "other user receipt",
		},
	}
	if err := telemetryDB.Create(&seedLogs).Error; err != nil {
		t.Fatalf("failed to seed logs: %v", err)
	}

	allLogs, err := SearchAllLogs("req-health-deep-link")
	if err != nil {
		t.Fatalf("admin request search failed: %v", err)
	}
	if len(allLogs) != 1 || allLogs[0].RequestID != "req-health-deep-link" {
		t.Fatalf("expected exact request receipt, got %+v", allLogs)
	}

	userLogs, err := SearchUserLogs(1, "req-health-deep-link")
	if err != nil {
		t.Fatalf("user request search failed: %v", err)
	}
	if len(userLogs) != 1 || userLogs[0].UserId != 1 {
		t.Fatalf("expected user-scoped request receipt, got %+v", userLogs)
	}

	otherUserLogs, err := SearchUserLogs(1, "req-other-user")
	if err != nil {
		t.Fatalf("cross-user request search failed: %v", err)
	}
	if len(otherUserLogs) != 0 {
		t.Fatalf("expected request receipt to remain user-scoped, got %+v", otherUserLogs)
	}

	typeLogs, err := SearchAllLogs("2")
	if err != nil {
		t.Fatalf("numeric log type search failed: %v", err)
	}
	if len(typeLogs) != 2 {
		t.Fatalf("expected both consume logs from numeric type search, got %+v", typeLogs)
	}
}

func TestGetConsumeLogByRequestIDForTokenScopesByUserAndToken(t *testing.T) {
	cleanup := setupTokenConsumeLogFilterTestDB(t)
	defer cleanup()

	seedLogs := []*Log{
		{
			Id:        21,
			PublicID:  "log_scope_21",
			RequestID: "req-shared",
			UserId:    1,
			TokenId:   10,
			Type:      LogTypeConsume,
			ModelName: "deepseek-chat",
		},
		{
			Id:        22,
			PublicID:  "log_scope_22",
			RequestID: "req-shared",
			UserId:    1,
			TokenId:   11,
			Type:      LogTypeConsume,
			ModelName: "qwen-plus",
		},
	}
	if err := telemetryDB.Create(&seedLogs).Error; err != nil {
		t.Fatalf("failed to seed logs: %v", err)
	}

	got, err := GetConsumeLogByRequestIDForToken(1, 10, "req-shared")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if got.TokenId != 10 || got.ModelName != "deepseek-chat" {
		t.Fatalf("expected token 10 deepseek log, got token=%d model=%s", got.TokenId, got.ModelName)
	}
	if _, err := GetConsumeLogByRequestIDForToken(2, 10, "req-shared"); err == nil {
		t.Fatalf("expected wrong user to be denied")
	}
	if _, err := GetConsumeLogByRequestIDForToken(1, 12, "req-shared"); err == nil {
		t.Fatalf("expected wrong token to be denied")
	}
}

func TestGetTokenConsumeLogsByTokenIDWithFilters_ModelAndKeyword(t *testing.T) {
	cleanup := setupTokenConsumeLogFilterTestDB(t)
	defer cleanup()

	seedLogs := []*Log{
		{
			Id:        11,
			TokenId:   9,
			Type:      LogTypeConsume,
			CreatedAt: 200,
			ModelName: "sora-2",
			Content:   "text generate",
			RequestID: "req-sora",
		},
		{
			Id:        12,
			TokenId:   9,
			Type:      LogTypeConsume,
			CreatedAt: 201,
			ModelName: "gpt-image-1.5",
			Content:   "image generate",
			RequestID: "req-image",
		},
	}
	if err := telemetryDB.Create(&seedLogs).Error; err != nil {
		t.Fatalf("failed to seed logs: %v", err)
	}

	logs, total, err := GetTokenConsumeLogsByTokenIDWithFilters(
		9,
		0,
		0,
		0,
		20,
		TokenConsumeLogFilter{
			ModelName: "sora-2",
			Keyword:   "sora",
		},
	)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if total != 1 {
		t.Fatalf("expected total=1, got total=%d", total)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].ModelName != "sora-2" {
		t.Fatalf("unexpected model returned: %s", logs[0].ModelName)
	}
}

func TestPublicTokenLogsRedactAccounting(t *testing.T) {
	defer setupTokenConsumeLogFilterTestDB(t)()
	source := Log{TokenId: 7, UserId: 0, ProjectID: 9, Type: LogTypeConsume, ChannelId: 42, UsageFact: `{"provider_family":"private","provider_cost_usd":1}`, Other: `{"provider_cost_usd":1,"gross_margin_usd":2,"customer_cost_usd":3,"nested":{"channel_id":42}}`}
	if err := telemetryDB.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	logs, _, err := GetTokenConsumeLogsByTokenIDWithFilters(7, 0, 0, 0, 10, TokenConsumeLogFilter{})
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs: %v %v", logs, err)
	}
	if logs[0].UsageFact != "" || logs[0].ChannelId != 0 || strings.Contains(logs[0].Other, "provider_cost") || strings.Contains(logs[0].Other, "gross_margin") || strings.Contains(logs[0].Other, "channel_id") || !strings.Contains(logs[0].Other, "customer_cost_usd") {
		t.Fatalf("leaked economics: %+v", logs[0])
	}
	var raw Log
	if err := telemetryDB.First(&raw, source.Id).Error; err != nil {
		t.Fatal(err)
	}
	if raw.UsageFact == "" || raw.ChannelId != 42 {
		t.Fatal("platform accounting changed")
	}
}
