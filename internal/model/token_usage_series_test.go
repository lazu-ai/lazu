package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupTokenUsageSeriesTestDB(t *testing.T) func() {
	t.Helper()

	previousDB := coreDB
	previousLogDB := telemetryDB

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = db

	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatalf("failed to migrate logs: %v", err)
	}

	return func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		coreDB = previousDB
		telemetryDB = previousLogDB
	}
}

func TestGetTokenUsageSeriesByTokenIDAggregatesByDay(t *testing.T) {
	cleanup := setupTokenUsageSeriesTestDB(t)
	defer cleanup()

	dayOne := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix()
	dayTwo := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC).Unix()
	seedLogs := []Log{
		{TokenId: 7, Type: LogTypeConsume, CreatedAt: dayOne + 3600, AmountMicrousd: billing.MicroUSD(100), PromptTokens: 10, CompletionTokens: 5},
		{TokenId: 7, Type: LogTypeConsume, CreatedAt: dayOne + 7200, AmountMicrousd: billing.MicroUSD(200), PromptTokens: 20, CompletionTokens: 10},
		{TokenId: 7, Type: LogTypeConsume, CreatedAt: dayTwo + 1800, AmountMicrousd: billing.MicroUSD(300), PromptTokens: 30, CompletionTokens: 15},
		{TokenId: 8, Type: LogTypeConsume, CreatedAt: dayOne + 3600, AmountMicrousd: billing.MicroUSD(999), PromptTokens: 99, CompletionTokens: 99},
		{TokenId: 7, Type: LogTypeManage, CreatedAt: dayOne + 3600, AmountMicrousd: billing.MicroUSD(999), PromptTokens: 99, CompletionTokens: 99},
	}
	if err := telemetryDB.Create(&seedLogs).Error; err != nil {
		t.Fatalf("failed to seed logs: %v", err)
	}

	points, err := GetTokenUsageSeriesByTokenID(7, dayOne, dayTwo+3600, "day")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(points) != 2 {
		t.Fatalf("expected 2 buckets, got %d: %#v", len(points), points)
	}
	if points[0].Bucket != dayOne || points[0].RequestCount != 2 || points[0].AmountMicrousd != 300 || points[0].TotalTokens() != 45 {
		t.Fatalf("unexpected first bucket: %#v", points[0])
	}
	if points[1].Bucket != dayTwo || points[1].RequestCount != 1 || points[1].AmountMicrousd != 300 || points[1].TotalTokens() != 45 {
		t.Fatalf("unexpected second bucket: %#v", points[1])
	}
}

func TestGetTokenUsageSeriesByTokenIDAggregatesByMonth(t *testing.T) {
	cleanup := setupTokenUsageSeriesTestDB(t)
	defer cleanup()

	january := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	february := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix()
	seedLogs := []Log{
		{TokenId: 12, Type: LogTypeConsume, CreatedAt: time.Date(2026, 1, 31, 23, 0, 0, 0, time.UTC).Unix(), AmountMicrousd: billing.MicroUSD(100), PromptTokens: 10, CompletionTokens: 2},
		{TokenId: 12, Type: LogTypeConsume, CreatedAt: time.Date(2026, 2, 1, 1, 0, 0, 0, time.UTC).Unix(), AmountMicrousd: billing.MicroUSD(200), PromptTokens: 20, CompletionTokens: 3},
	}
	if err := telemetryDB.Create(&seedLogs).Error; err != nil {
		t.Fatalf("failed to seed logs: %v", err)
	}

	points, err := GetTokenUsageSeriesByTokenID(12, january, february+3600, "month")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	if len(points) != 2 {
		t.Fatalf("expected 2 buckets, got %d: %#v", len(points), points)
	}
	if points[0].Bucket != january || points[0].AmountMicrousd != 100 || points[0].TotalTokens() != 12 {
		t.Fatalf("unexpected january bucket: %#v", points[0])
	}
	if points[1].Bucket != february || points[1].AmountMicrousd != 200 || points[1].TotalTokens() != 23 {
		t.Fatalf("unexpected february bucket: %#v", points[1])
	}
}

func TestUserUsageSummaryAggregatesSiblingTokens(t *testing.T) {
	cleanup := setupTokenUsageSeriesTestDB(t)
	defer cleanup()

	dayOne := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Unix()
	seedLogs := []Log{
		{UserId: 31, TokenId: 101, Type: LogTypeConsume, CreatedAt: dayOne + 3600, AmountMicrousd: billing.MicroUSD(100), PromptTokens: 10, CompletionTokens: 1},
		{UserId: 31, TokenId: 102, Type: LogTypeConsume, CreatedAt: dayOne + 7200, AmountMicrousd: billing.MicroUSD(200), PromptTokens: 20, CompletionTokens: 2},
		{UserId: 32, TokenId: 201, Type: LogTypeConsume, CreatedAt: dayOne + 7200, AmountMicrousd: billing.MicroUSD(999), PromptTokens: 99, CompletionTokens: 9},
	}
	if err := telemetryDB.Create(&seedLogs).Error; err != nil {
		t.Fatalf("failed to seed logs: %v", err)
	}

	summary, err := GetUserConsumeSummaryByUserIDInWindow(31, dayOne, dayOne+86400)
	if err != nil {
		t.Fatalf("summary query failed: %v", err)
	}
	if summary.RequestCount != 2 || summary.AmountMicrousd != 300 || summary.TotalTokens() != 33 {
		t.Fatalf("unexpected user summary: %#v", summary)
	}

	points, err := GetUserUsageSeriesByUserIDInWindow(31, dayOne, dayOne+86400, "day")
	if err != nil {
		t.Fatalf("series query failed: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 bucket, got %d: %#v", len(points), points)
	}
	if points[0].Bucket != dayOne || points[0].RequestCount != 2 || points[0].AmountMicrousd != 300 || points[0].TotalTokens() != 33 {
		t.Fatalf("unexpected user series bucket: %#v", points[0])
	}
}

func TestUserUsageSummaryWindowHonorsExplicitZeroUntil(t *testing.T) {
	cleanup := setupTokenUsageSeriesTestDB(t)
	defer cleanup()

	if err := telemetryDB.Exec(
		"INSERT INTO logs (user_id, token_id, type, created_at, amount_microusd, prompt_tokens, completion_tokens) VALUES (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?, ?)",
		41, 21, LogTypeConsume, 0, billing.MicroUSD(100), 10, 1,
		41, 21, LogTypeConsume, 1, billing.MicroUSD(200), 20, 2,
		42, 22, LogTypeConsume, 0, billing.MicroUSD(999), 99, 9,
	).Error; err != nil {
		t.Fatalf("failed to seed logs: %v", err)
	}

	summary, err := GetUserConsumeSummaryByUserIDInWindow(41, 0, 0)
	if err != nil {
		t.Fatalf("summary query failed: %v", err)
	}
	if summary.RequestCount != 1 || summary.AmountMicrousd != 100 || summary.TotalTokens() != 11 {
		t.Fatalf("unexpected summary for zero window: %#v", summary)
	}

	points, err := GetUserUsageSeriesByUserIDInWindow(41, 0, 0, "hour")
	if err != nil {
		t.Fatalf("series query failed: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected 1 bucket, got %d: %#v", len(points), points)
	}
	if points[0].Bucket != 0 || points[0].RequestCount != 1 || points[0].AmountMicrousd != 100 || points[0].TotalTokens() != 11 {
		t.Fatalf("unexpected series bucket for zero window: %#v", points[0])
	}
}
