package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func setupSpendBudgetControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalDB := model.CoreDB()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	if err := db.AutoMigrate(&model.SpendBudget{}); err != nil {
		t.Fatalf("failed to migrate spend budget table: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(originalDB)
	})
	return db
}

func TestSaveSpendBudgetUsesRouteIDForUpdates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSpendBudgetControllerTestDB(t)
	first := model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             10,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1_000),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}
	second := model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             20,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(2_000),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             true,
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("failed to create first budget: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("failed to create second budget: %v", err)
	}

	engine := gin.New()
	engine.PUT("/api/admin/spend-budgets/:id", SaveSpendBudget)
	enabled := true
	body, err := json.Marshal(spendBudgetPayload{
		ID:                  second.ID,
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             99,
		Period:              model.SpendBudgetPeriodDay,
		LimitMicrousd:       3_000,
		WarningThresholdPct: 90,
		BurstMultiplier:     1.5,
		Enforcement:         model.SpendBudgetEnforcementAlert,
		Enabled:             &enabled,
	})
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/admin/spend-budgets/%d", first.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var reloadedFirst model.SpendBudget
	if err := db.First(&reloadedFirst, first.ID).Error; err != nil {
		t.Fatalf("failed to reload first budget: %v", err)
	}
	if reloadedFirst.TokenID != 99 || reloadedFirst.LimitMicrousd != billing.MicroUSD(3_000) || reloadedFirst.Period != model.SpendBudgetPeriodDay {
		t.Fatalf("expected route budget to be updated, got %+v", reloadedFirst)
	}
	var reloadedSecond model.SpendBudget
	if err := db.First(&reloadedSecond, second.ID).Error; err != nil {
		t.Fatalf("failed to reload second budget: %v", err)
	}
	if reloadedSecond.TokenID != second.TokenID || reloadedSecond.LimitMicrousd != second.LimitMicrousd || reloadedSecond.Period != second.Period {
		t.Fatalf("expected body-id budget to remain unchanged, got %+v", reloadedSecond)
	}
}

func TestSaveSpendBudgetPreservesEnabledWhenUpdateOmitsEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupSpendBudgetControllerTestDB(t)
	budget := model.SpendBudget{
		Scope:               model.SpendBudgetScopeToken,
		TokenID:             30,
		Period:              model.SpendBudgetPeriodMonth,
		LimitMicrousd:       billing.MicroUSD(1_000),
		WarningThresholdPct: 80,
		Enforcement:         model.SpendBudgetEnforcementBlock,
		Enabled:             false,
	}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatalf("failed to create budget: %v", err)
	}
	if err := db.Model(&budget).Update("enabled", false).Error; err != nil {
		t.Fatalf("failed to disable budget seed: %v", err)
	}

	engine := gin.New()
	engine.PUT("/api/admin/spend-budgets/:id", SaveSpendBudget)
	body := []byte(`{"scope":"token","token_id":30,"period":"day","limit_microusd":4000,"warning_threshold_pct":90,"burst_multiplier":1.5,"enforcement":"alert"}`)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/admin/spend-budgets/%d", budget.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var reloaded model.SpendBudget
	if err := db.First(&reloaded, budget.ID).Error; err != nil {
		t.Fatalf("failed to reload budget: %v", err)
	}
	if reloaded.Enabled {
		t.Fatalf("expected omitted enabled to preserve disabled budget, got %+v", reloaded)
	}
	if reloaded.LimitMicrousd != billing.MicroUSD(4_000) || reloaded.Period != model.SpendBudgetPeriodDay || reloaded.Enforcement != model.SpendBudgetEnforcementAlert {
		t.Fatalf("expected editable fields to update, got %+v", reloaded)
	}
}
