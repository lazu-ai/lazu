package controller

import (
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
	"testing"
)

func setupModelCatalogTestDB(t *testing.T) func() {
	t.Helper()

	previousDB := model.CoreDB()
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)

	if err := db.AutoMigrate(
		&model.User{},
		&model.Channel{},
		&model.Ability{},
		&model.Model{},
		&model.ModelMetadata{},
		&model.ModelSellPrice{},
		&model.Vendor{},
	); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		common.RedisEnabled = previousRedisEnabled
		model.SetCoreDB(previousDB)
		if previousDB != nil {
			model.RefreshPricing()
		}
	}
}
