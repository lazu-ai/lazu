package api

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/worker"
	"gorm.io/gorm"
)

func TestAllInOneTaskRefreshesChannelAndAbilityChanges(t *testing.T) {
	t.Setenv("DASHBOARD_ROLLUP_WORKER_ENABLED", "true")
	var refresh worker.Task
	for _, task := range apiBackgroundWorkerTasks() {
		if task.Name() == "dashboard_usage_rollup" {
			refresh = task
		}
	}
	if refresh == nil {
		t.Fatal("all-in-one runtime has no telemetry refresh")
	}
	open := func(name string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
		return db
	}
	core, telemetry := open("core.db"), open("telemetry.db")
	oldCore, oldTelemetry := model.CoreDB(), model.ConfiguredTelemetryDB()
	model.SetCoreDB(core)
	model.SetTelemetryDB(telemetry)
	t.Cleanup(func() { model.SetCoreDB(oldCore); model.SetTelemetryDB(oldTelemetry) })
	model.EnsureColumnNamesInitialized()
	if err := core.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.TelemetryCleanupJob{}); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.AutoMigrate(&model.TelemetryChannel{}, &model.TelemetryAbility{}, &model.DashboardUsageHourlyTotal{}); err != nil {
		t.Fatal(err)
	}
	row := model.Channel{Id: 701, Name: "before", Status: 1}
	ability := model.Ability{ChannelId: 701, Model: "fixture", Group: "default", Enabled: true}
	if err := core.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&ability).Error; err != nil {
		t.Fatal(err)
	}
	if err := refresh.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := core.Model(&row).Updates(map[string]interface{}{"name": "after", "status": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Model(&ability).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := refresh.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	var projected model.TelemetryChannel
	if err := telemetry.First(&projected, 701).Error; err != nil || projected.Name != "after" || projected.Status != 2 {
		t.Fatalf("stale channel: %+v %v", projected, err)
	}
	var projectedAbility model.TelemetryAbility
	if err := telemetry.First(&projectedAbility).Error; err != nil || projectedAbility.Enabled {
		t.Fatalf("stale ability: %+v %v", projectedAbility, err)
	}
	if err := core.Unscoped().Delete(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Delete(&ability).Error; err != nil {
		t.Fatal(err)
	}
	if err := refresh.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, row := range []interface{}{&model.TelemetryChannel{}, &model.TelemetryAbility{}} {
		var count int64
		if err := telemetry.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("deleted metadata remains: %T count=%d err=%v", row, count, err)
		}
	}
}
