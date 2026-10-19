package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupSpendBudgetTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB := coreDB
	originalLogDB := telemetryDB
	originalUsingSQLite := common.UsingSQLite
	originalUsingPostgreSQL := common.UsingPostgreSQL
	originalLogSQLType := common.LogSqlType
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	telemetryDB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.LogSqlType = common.DatabaseTypeSQLite
	initCol()
	if err := db.AutoMigrate(&Log{}, &SpendBudget{}, &SpendBudgetAlert{}, &SpendBudgetReservation{}, &SpendBudgetCharge{}); err != nil {
		t.Fatalf("failed to migrate spend budget tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = originalDB
		telemetryDB = originalLogDB
		common.UsingSQLite = originalUsingSQLite
		common.UsingPostgreSQL = originalUsingPostgreSQL
		common.LogSqlType = originalLogSQLType
	})
	return db
}

func TestSpendBudgetThresholdAndBlockedAlertsCarryAttribution(t *testing.T) {
	db := setupSpendBudgetTestDB(t)
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	budget := SpendBudget{
		Scope:               SpendBudgetScopeToken,
		TokenID:             42,
		Period:              SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1_000),
		WarningThresholdPct: 80,
		Enforcement:         SpendBudgetEnforcementBlock,
		Enabled:             true,
	}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}
	if err := RecordSpendBudgetCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       42,
		SessionID:     "sess-existing",
		RequestID:     "req-current",
		DeltaMicrousd: billing.MicroUSD(700),
		OccurredAt:    now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("failed to seed current spend: %v", err)
	}

	if err := RecordSpendBudgetCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       42,
		SessionID:     "sess-cross-80",
		AgentID:       "codex",
		RequestID:     "req-cross-80",
		DeltaMicrousd: billing.MicroUSD(150),
		OccurredAt:    now,
	}); err != nil {
		t.Fatalf("RecordSpendBudgetCharge error: %v", err)
	}

	var threshold SpendBudgetAlert
	if err := db.Where("alert_type = ? AND threshold_pct = ?", SpendBudgetAlertThreshold, 80).First(&threshold).Error; err != nil {
		t.Fatalf("failed to load threshold alert: %v", err)
	}
	if threshold.SessionID != "sess-cross-80" || threshold.AgentID != "codex" || threshold.RequestID != "req-cross-80" || threshold.TokenID != 42 {
		t.Fatalf("threshold alert lost attribution: %+v", threshold)
	}
	if threshold.SpentMicrousd != billing.MicroUSD(850) || threshold.LimitMicrousd != billing.MicroUSD(1_000) {
		t.Fatalf("unexpected threshold spend values: %+v", threshold)
	}

	err := CheckSpendBudgetBeforeCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       42,
		SessionID:     "sess-blocked",
		AgentID:       "claude-code",
		RequestID:     "req-blocked",
		DeltaMicrousd: billing.MicroUSD(400),
		OccurredAt:    now,
	})
	if !errors.Is(err, ErrSpendBudgetExceeded) {
		t.Fatalf("expected ErrSpendBudgetExceeded, got %v", err)
	}
	var blocked SpendBudgetAlert
	if err := db.Where("alert_type = ?", SpendBudgetAlertBlocked).First(&blocked).Error; err != nil {
		t.Fatalf("failed to load blocked alert: %v", err)
	}
	if blocked.SessionID != "sess-blocked" || blocked.RequestID != "req-blocked" || blocked.TokenID != 42 {
		t.Fatalf("blocked alert lost request/session/key attribution: %+v", blocked)
	}
	if blocked.SpentMicrousd != billing.MicroUSD(1_250) {
		t.Fatalf("expected projected spend 1250, got %d", blocked.SpentMicrousd)
	}
}

func TestSpendBudgetReservationsPreventConcurrentAdmission(t *testing.T) {
	db := setupSpendBudgetTestDB(t)
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	budget := SpendBudget{
		Scope:         SpendBudgetScopeToken,
		TokenID:       42,
		Period:        SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(1_000),
		Enforcement:   SpendBudgetEnforcementBlock,
		Enabled:       true,
	}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}

	first := SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       42,
		SessionID:     "sess-reserve",
		RequestID:     "req-reserve-1",
		DeltaMicrousd: billing.MicroUSD(600),
		OccurredAt:    now,
	}
	if err := CheckSpendBudgetBeforeCharge(first); err != nil {
		t.Fatalf("first budget check should reserve, got %v", err)
	}
	var reserved billing.MicroUSD
	if err := db.Model(&SpendBudgetReservation{}).Select("COALESCE(SUM(reserved_microusd), 0)").Scan(&reserved).Error; err != nil {
		t.Fatalf("failed to sum reservations: %v", err)
	}
	if reserved != billing.MicroUSD(600) {
		t.Fatalf("expected 600 reserved, got %d", reserved)
	}

	err := CheckSpendBudgetBeforeCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       42,
		SessionID:     "sess-reserve",
		RequestID:     "req-reserve-2",
		DeltaMicrousd: billing.MicroUSD(500),
		OccurredAt:    now,
	})
	if !errors.Is(err, ErrSpendBudgetExceeded) {
		t.Fatalf("expected reservation-aware block, got %v", err)
	}
	var blocked SpendBudgetAlert
	if err := db.Where("alert_type = ? AND request_id = ?", SpendBudgetAlertBlocked, "req-reserve-2").First(&blocked).Error; err != nil {
		t.Fatalf("failed to load blocked alert: %v", err)
	}
	if blocked.SpentMicrousd != billing.MicroUSD(1_100) {
		t.Fatalf("expected projected spend to include reservation, got %d", blocked.SpentMicrousd)
	}

	if err := telemetryDB.Create(&Log{
		PublicID:       "log-reserve-1",
		RequestID:      first.RequestID,
		UserId:         first.UserID,
		TokenId:        first.TokenID,
		Type:           LogTypeConsume,
		SessionID:      first.SessionID,
		AmountMicrousd: first.DeltaMicrousd,
		CreatedAt:      now.Unix(),
	}).Error; err != nil {
		t.Fatalf("failed to seed settled log: %v", err)
	}
	if err := ReleaseSpendBudgetReservation(first); err != nil {
		t.Fatalf("failed to release reservation: %v", err)
	}
	var reservationCount int64
	if err := db.Model(&SpendBudgetReservation{}).Count(&reservationCount).Error; err != nil {
		t.Fatalf("failed to count reservations: %v", err)
	}
	if reservationCount != 0 {
		t.Fatalf("expected reservation to release after settlement, got %d", reservationCount)
	}
}

func TestSpendBudgetCountsRecordedChargesWithoutConsumeLogs(t *testing.T) {
	db := setupSpendBudgetTestDB(t)
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	budget := SpendBudget{
		Scope:         SpendBudgetScopeToken,
		TokenID:       42,
		Period:        SpendBudgetPeriodMonth,
		LimitMicrousd: billing.MicroUSD(1_000),
		Enforcement:   SpendBudgetEnforcementBlock,
		Enabled:       true,
	}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}
	if err := RecordSpendBudgetCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       42,
		RequestID:     "req-settled-without-log",
		DeltaMicrousd: billing.MicroUSD(700),
		OccurredAt:    now,
	}); err != nil {
		t.Fatalf("RecordSpendBudgetCharge error: %v", err)
	}
	var logCount int64
	if err := telemetryDB.Model(&Log{}).Where("type = ?", LogTypeConsume).Count(&logCount).Error; err != nil {
		t.Fatalf("failed to count consume logs: %v", err)
	}
	if logCount != 0 {
		t.Fatalf("expected no consume logs, got %d", logCount)
	}
	telemetryDB = nil

	err := CheckSpendBudgetBeforeCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       42,
		RequestID:     "req-next",
		DeltaMicrousd: billing.MicroUSD(400),
		OccurredAt:    now,
	})
	if !errors.Is(err, ErrSpendBudgetExceeded) {
		t.Fatalf("expected recorded charge to block next request without consume logs, got %v", err)
	}
}

func TestSpendBudgetBurstAlertComparesPreviousPeriod(t *testing.T) {
	db := setupSpendBudgetTestDB(t)
	now := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	budget := SpendBudget{
		Scope:               SpendBudgetScopeUser,
		UserID:              7,
		Period:              SpendBudgetPeriodDay,
		LimitMicrousd:       billing.MicroUSD(10_000),
		WarningThresholdPct: 80,
		BurstMultiplier:     2,
		Enforcement:         SpendBudgetEnforcementAlert,
		Enabled:             true,
	}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}
	if err := RecordSpendBudgetCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       99,
		RequestID:     "req-prev",
		DeltaMicrousd: billing.MicroUSD(100),
		OccurredAt:    now.AddDate(0, 0, -1),
	}); err != nil {
		t.Fatalf("failed to seed previous spend: %v", err)
	}
	if err := RecordSpendBudgetCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       99,
		RequestID:     "req-current",
		DeltaMicrousd: billing.MicroUSD(150),
		OccurredAt:    now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("failed to seed current spend: %v", err)
	}

	if err := RecordSpendBudgetCharge(SpendBudgetChargeInput{
		UserID:        7,
		TokenID:       99,
		SessionID:     "sess-burst",
		AgentID:       "codex",
		RequestID:     "req-burst",
		DeltaMicrousd: billing.MicroUSD(60),
		OccurredAt:    now,
	}); err != nil {
		t.Fatalf("RecordSpendBudgetCharge error: %v", err)
	}

	var alert SpendBudgetAlert
	if err := db.Where("alert_type = ?", SpendBudgetAlertBurst).First(&alert).Error; err != nil {
		t.Fatalf("failed to load burst alert: %v", err)
	}
	if alert.UserID != 7 || alert.TokenID != 99 || alert.SessionID != "sess-burst" || alert.RequestID != "req-burst" {
		t.Fatalf("burst alert lost attribution: %+v", alert)
	}
	if alert.SpentMicrousd != billing.MicroUSD(210) {
		t.Fatalf("expected current period projected spend 210, got %d", alert.SpentMicrousd)
	}
}

func TestListSpendBudgetsPageReturnsCompletePaginationMetadata(t *testing.T) {
	db := setupSpendBudgetTestDB(t)
	for index := 1; index <= 3; index++ {
		if err := db.Create(&SpendBudget{
			Scope:               SpendBudgetScopeUser,
			UserID:              index,
			Period:              SpendBudgetPeriodMonth,
			LimitMicrousd:       billing.MicroUSD(index * 1_000),
			WarningThresholdPct: 80,
			Enforcement:         SpendBudgetEnforcementBlock,
			Enabled:             true,
		}).Error; err != nil {
			t.Fatalf("failed to create budget %d: %v", index, err)
		}
	}

	firstPage, total, err := ListSpendBudgetsPage(2, 0)
	if err != nil {
		t.Fatalf("ListSpendBudgetsPage first page error: %v", err)
	}
	if total != 3 || len(firstPage) != 2 {
		t.Fatalf("expected first page 2/3, got %d/%d", len(firstPage), total)
	}

	secondPage, total, err := ListSpendBudgetsPage(2, 2)
	if err != nil {
		t.Fatalf("ListSpendBudgetsPage second page error: %v", err)
	}
	if total != 3 || len(secondPage) != 1 {
		t.Fatalf("expected second page 1/3, got %d/%d", len(secondPage), total)
	}
}

func TestSaveSpendBudgetPreservesCreatedAtOnUpdate(t *testing.T) {
	db := setupSpendBudgetTestDB(t)
	createdAt := time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)
	budget := SpendBudget{
		Scope:               SpendBudgetScopeToken,
		TokenID:             42,
		Period:              SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1_000),
		WarningThresholdPct: 80,
		Enforcement:         SpendBudgetEnforcementBlock,
		Enabled:             true,
		CreatedAt:           createdAt,
		UpdatedAt:           createdAt,
	}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}

	updated, err := SaveSpendBudget(SpendBudget{
		ID:                  budget.ID,
		Scope:               SpendBudgetScopeToken,
		TokenID:             42,
		Period:              SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(2_000),
		WarningThresholdPct: 90,
		Enforcement:         SpendBudgetEnforcementAlert,
		Enabled:             false,
	})
	if err != nil {
		t.Fatalf("SaveSpendBudget update error: %v", err)
	}
	if !updated.CreatedAt.Equal(createdAt) {
		t.Fatalf("expected returned created_at %s, got %s", createdAt, updated.CreatedAt)
	}
	var loaded SpendBudget
	if err := db.First(&loaded, budget.ID).Error; err != nil {
		t.Fatalf("failed to reload budget: %v", err)
	}
	if !loaded.CreatedAt.Equal(createdAt) {
		t.Fatalf("expected stored created_at %s, got %s", createdAt, loaded.CreatedAt)
	}
	if loaded.LimitMicrousd != billing.MicroUSD(2_000) || loaded.WarningThresholdPct != 90 || loaded.Enforcement != SpendBudgetEnforcementAlert || loaded.Enabled {
		t.Fatalf("expected editable fields to update, got %+v", loaded)
	}
}
