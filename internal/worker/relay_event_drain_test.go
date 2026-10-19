package worker

import (
	"context"
	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
)

// Even an empty/disabled drain must persist process-local exports before the
// run-once process exits. This protects the existing task-level deferred flush.
func TestRelayEventRunOnceFlushesUsageExportsOnFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "usage.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.UsageData{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldExport, oldRedis := model.ConfiguredTelemetryDB(), common.DataExportEnabled, common.RedisEnabled
	model.SetTelemetryDB(db)
	common.DataExportEnabled = true
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.SetTelemetryDB(oldDB)
		common.DataExportEnabled = oldExport
		common.RedisEnabled = oldRedis
		raw, _ := db.DB()
		raw.Close()
	})
	model.LogUsageData(1, "fixture", "test-model", 123, 3600, 45)
	if err := RunOnce(context.Background(), NewRelayEventDrainTask()); err == nil {
		t.Fatal("expected unavailable consumer")
	}
	var row model.UsageData
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Count != 1 || row.AmountMicrousd != 123 || row.TokenUsed != 45 {
		t.Fatalf("lost usage export: %+v", row)
	}
}
