package model

import (
	"fmt"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestSearchModelsDoesNotMatchDescriptionOnly(t *testing.T) {
	previousDB := coreDB
	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	EnsureColumnNamesInitialized()
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = previousDB
	}()

	if err := db.AutoMigrate(&Model{}, &Vendor{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	if err := db.Create(&Vendor{Id: 1, PublicID: "deepseek", Name: "DeepSeek"}).Error; err != nil {
		t.Fatalf("failed to create deepseek vendor: %v", err)
	}
	if err := db.Create(&Vendor{Id: 2, PublicID: "perplexity", Name: "Perplexity"}).Error; err != nil {
		t.Fatalf("failed to create perplexity vendor: %v", err)
	}
	if err := (&Model{ModelName: "deepseek-v3.2", VendorID: 1, Status: 1}).Insert(); err != nil {
		t.Fatalf("failed to create deepseek model: %v", err)
	}
	if err := (&Model{
		ModelName:   "sonar-reasoning-pro",
		Description: "Powered by DeepSeek R1 with search.",
		VendorID:    2,
		Status:      1,
	}).Insert(); err != nil {
		t.Fatalf("failed to create sonar model: %v", err)
	}

	got, total, err := SearchModelsWithFilters("deepseek", "", "", "", 0, 20, "name_asc")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].ModelName != "deepseek-v3.2" {
		t.Fatalf("expected only the DeepSeek model, total=%d got=%#v", total, got)
	}
}

func TestSearchModelsAppliesStatusAndTierFiltersBeforePagination(t *testing.T) {
	previousDB := coreDB
	previousUsingPostgreSQL := common.UsingPostgreSQL
	previousUsingSQLite := common.UsingSQLite
	previousCommonGroupCol := commonGroupCol
	db, err := gorm.Open(gormsqlite.Open("file:model_search_filters?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	common.UsingPostgreSQL = false
	common.UsingSQLite = true
	initCol()
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = previousDB
		common.UsingPostgreSQL = previousUsingPostgreSQL
		common.UsingSQLite = previousUsingSQLite
		commonGroupCol = previousCommonGroupCol
	}()

	if err := db.AutoMigrate(&Model{}, &Vendor{}, &ModelSellPrice{}, &Ability{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	if err := db.Create(&Vendor{Id: 1, PublicID: "openai", Name: "OpenAI"}).Error; err != nil {
		t.Fatalf("failed to create vendor: %v", err)
	}
	for index, status := range []int{1, 1, 1, 0} {
		name := fmt.Sprintf("filter-model-%d", index+1)
		if err := (&Model{ModelName: name, VendorID: 1, Status: status}).Insert(); err != nil {
			t.Fatalf("failed to create %s: %v", name, err)
		}
	}
	if err := (&Model{ModelName: "gpt-4o-gizmo-custom", VendorID: 1, Status: 1}).Insert(); err != nil {
		t.Fatalf("failed to create normalized-price model: %v", err)
	}
	if err := db.Model(&Model{}).Where("model_name = ?", "filter-model-4").Update("status", 0).Error; err != nil {
		t.Fatalf("failed to mark model unlisted: %v", err)
	}
	inputPrice := 1.25
	prices := []ModelSellPrice{
		{ModelName: "filter-model-1", ChannelGroup: ChannelLaneDirect, InputPerMtok: &inputPrice, Active: true, PricingVersion: 1},
		{ModelName: "filter-model-2", ChannelGroup: ChannelLaneAgent, InputPerMtok: &inputPrice, Active: true, PricingVersion: 1},
		{ModelName: "filter-model-3", ChannelGroup: ChannelLaneDirect, InputPerMtok: &inputPrice, Active: false, PricingVersion: 1},
		{ModelName: "gpt-4o-gizmo-*", ChannelGroup: ChannelLaneDirect, InputPerMtok: &inputPrice, Active: true, PricingVersion: 1},
	}
	if err := db.Create(&prices).Error; err != nil {
		t.Fatalf("failed to create prices: %v", err)
	}
	if err := db.Model(&ModelSellPrice{}).
		Where("model_name = ? AND channel_group = ?", "filter-model-3", ChannelLaneDirect).
		Update("active", false).Error; err != nil {
		t.Fatalf("failed to mark price inactive: %v", err)
	}

	listed, total, err := SearchModelsWithFilters("filter-model", "", "listed", "", 0, 2, "name_asc")
	if err != nil {
		t.Fatalf("listed search failed: %v", err)
	}
	if total != 3 || len(listed) != 2 {
		t.Fatalf("expected server-side listed pagination, total=%d rows=%#v", total, listed)
	}

	atRisk, total, err := SearchModelsWithFilters("filter-model", "", "", "atRisk", 0, 10, "name_asc")
	if err != nil {
		t.Fatalf("at-risk search failed: %v", err)
	}
	if total != 3 || len(atRisk) != 3 {
		t.Fatalf("expected models without an active direct price, total=%d rows=%#v", total, atRisk)
	}

	unpriced, total, err := SearchModelsWithFilters("filter-model", "", "", "unpriced", 0, 10, "name_asc")
	if err != nil {
		t.Fatalf("unpriced search failed: %v", err)
	}
	if total != 2 || len(unpriced) != 2 {
		t.Fatalf("expected models without any active price, total=%d rows=%#v", total, unpriced)
	}

	normalized, total, err := SearchModelsWithFilters("gpt-4o-gizmo", "", "", "atRisk", 0, 10, "name_asc")
	if err != nil {
		t.Fatalf("normalized-price search failed: %v", err)
	}
	if total != 0 || len(normalized) != 0 {
		t.Fatalf("expected wildcard-priced model not to be at risk, total=%d rows=%#v", total, normalized)
	}
}

func TestOfferedModelSearchUsesCatalogOwnersBeforePagination(t *testing.T) {
	previousDB, previousPricing, previousTime := coreDB, pricingMap, lastGetPricingTime
	db, err := gorm.Open(gormsqlite.Open("file:offered_search?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	coreDB = db
	t.Cleanup(func() {
		coreDB, pricingMap, lastGetPricingTime = previousDB, previousPricing, previousTime
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&Model{}, &Vendor{}); err != nil {
		t.Fatal(err)
	}
	rows := []Model{
		{ModelName: "active-model", Status: 1, LifecycleStatus: "active"},
		{ModelName: "imported-model", Status: 1, LifecycleStatus: "candidate"},
		{ModelName: "offline-model", Status: 1, LifecycleStatus: "active"},
		{ModelName: "family-", Status: 1, LifecycleStatus: "active", NameRule: NameRulePrefix},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	pricingMap = []Pricing{{ModelName: "active-model"}, {ModelName: "family-one"}, {ModelName: "family-two"}, {ModelName: "imported-model"}}
	lastGetPricingTime = time.Now()
	got, total, err := SearchModelsWithFilters("", "", "offered", "", 0, 1, "name_asc")
	if err != nil || total != 2 || len(got) != 1 || got[0].ModelName != "active-model" {
		t.Fatalf("catalog filter: total=%d rows=%v err=%v", total, got, err)
	}
	got, total, err = SearchModelsWithFilters("family-one", "", "offered", "", 0, 10, "name_asc")
	if err != nil || total != 1 || len(got) != 1 || got[0].ModelName != "family-" {
		t.Fatalf("wildcard owner: total=%d rows=%v err=%v", total, got, err)
	}
	_, total, err = SearchModelsWithFilters("", "", "", "", 0, 10, "name_asc")
	if err != nil || total != 4 {
		t.Fatalf("all metadata must remain accessible: total=%d err=%v", total, err)
	}
}
