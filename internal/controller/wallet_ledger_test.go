package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func setupWalletLedgerControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.CoreDB()
	db, err := gorm.Open(sqlite.Open("file:wallet_ledger_controller?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open wallet ledger test database: %v", err)
	}
	if err := db.AutoMigrate(&model.WalletLedger{}); err != nil {
		t.Fatalf("migrate wallet ledger: %v", err)
	}
	model.SetCoreDB(db)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	})
	return db
}

func TestGetUserWalletLedgerScopesAggregatesAndRedactsMovements(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupWalletLedgerControllerTestDB(t)
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	rows := []model.WalletLedger{
		{UserID: 3, DeltaMicrousd: 2_000_000, Kind: model.WalletLedgerKindBonus, RefType: "affiliate_transfer", RefID: "private-ref", PricingVersion: 1, Metadata: `{"private":"value"}`, CreatedAt: start.Add(time.Hour)},
		{UserID: 4, DeltaMicrousd: 9_000_000, Kind: model.WalletLedgerKindTopup, RefType: "topup", RefID: "other-user", PricingVersion: 1, CreatedAt: start.Add(time.Hour)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create wallet ledger rows: %v", err)
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/?start_timestamp=%d&end_timestamp=%d&timezone=Asia%%2FTaipei", start.Unix(), end.Unix()),
		nil,
	)
	context.Set("id", 3)

	GetUserWalletLedger(context)

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode wallet ledger response: %v", err)
	}
	if !payload.Success || len(payload.Data.Items) != 1 {
		t.Fatalf("expected one current-user movement, got %s", recorder.Body.String())
	}
	item := payload.Data.Items[0]
	if item["date"] != "2026-08-01" || item["credit_microusd"] != float64(2_000_000) || item["spend_microusd"] != float64(0) || item["closing_balance_microusd"] != float64(2_000_000) {
		t.Fatalf("unexpected movement: %#v", item)
	}
	for _, privateField := range []string{"id", "user_id", "delta_microusd", "kind", "created_at", "ref_type", "ref_id", "metadata", "pricing_version"} {
		if _, exists := item[privateField]; exists {
			t.Fatalf("response leaked %s: %#v", privateField, item)
		}
	}
}

func TestGetUserWalletLedgerRejectsUnknownTimezone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/?start_timestamp=%d&end_timestamp=%d&timezone=Mars%%2FOlympus", start.Unix(), start.Add(24*time.Hour).Unix()),
		nil,
	)
	context.Set("id", 3)

	GetUserWalletLedger(context)

	assertConsoleError(t, recorder, http.StatusBadRequest, "wallet_ledger_range_invalid")
}

func TestGetUserWalletLedgerRejectsUnboundedRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodGet,
		"/?start_timestamp=1&end_timestamp=9999999999",
		nil,
	)
	context.Set("id", 3)

	GetUserWalletLedger(context)

	assertConsoleError(t, recorder, http.StatusBadRequest, "wallet_ledger_range_invalid")
}
