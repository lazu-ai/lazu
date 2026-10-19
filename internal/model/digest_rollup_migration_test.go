package model

import (
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestPostgresDigestMigrationWithoutRedisKeepsRuntimePublisherRequired(t *testing.T) {
	core, telemetry := postgresTelemetryFixture(t)
	if err := core.AutoMigrate(&Option{}); err != nil {
		t.Fatal(err)
	}
	oldRedis := common.RDB
	common.RDB = nil
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.RDB = oldRedis
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
	})
	t.Setenv("PROCESS_ROLE", "jobs")
	t.Setenv("LAZU_MIGRATION_MODE", "auto")
	if err := InitMigrationOptionMap(); err != nil {
		t.Fatalf("offline migration settings: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	for _, withHistory := range []bool{false, true} {
		name := "empty"
		if withHistory {
			name = "historical"
		}
		t.Run(name, func(t *testing.T) {
			if err := updateOptionForMigration(DigestRollupSinceOption, "0"); err != nil {
				t.Fatal(err)
			}
			if withHistory {
				logs := []Log{
					{PublicID: "offline-first-partial-hour", UserId: 1, Type: LogTypeConsume, CreatedAt: now.Add(-5*time.Hour + time.Minute).Unix(), AmountMicrousd: 7},
					{PublicID: "offline-complete-hour", UserId: 1, Type: LogTypeConsume, CreatedAt: now.Add(-3*time.Hour + time.Minute).Unix(), AmountMicrousd: 123, ModelName: "offline-digest-model"},
				}
				if err := telemetry.Create(&logs).Error; err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err := backfillDigestRollupForMigration(now); err != nil {
					t.Fatalf("offline backfill attempt %d: %v", attempt, err)
				}
			}
			var marker Option
			if err := core.Where("key = ?", DigestRollupSinceOption).First(&marker).Error; err != nil {
				t.Fatal(err)
			}
			if marker.Value == "0" || DigestRollupSince() == 0 {
				t.Fatal("migration did not persist and apply its completion marker")
			}
			if withHistory {
				var result DigestUsageHour
				if err := telemetry.Where("model_name = ?", "offline-digest-model").First(&result).Error; err != nil {
					t.Fatal(err)
				}
				if result.Spend != 123 || result.Requests != 1 {
					t.Fatalf("digest backfill was lost or repeated: %+v", result)
				}
			}
		})
	}
	if err := UpdateOption("OfflineMigrationRuntimeRegression", "1"); err == nil || !strings.Contains(err.Error(), "control-plane Redis unavailable") {
		t.Fatalf("runtime option update bypassed its required Redis publisher: %v", err)
	}
	if err := publishCommittedOptions(); err == nil || !strings.Contains(err.Error(), "control-plane Redis unavailable") {
		t.Fatalf("runtime publisher did not fail closed: %v", err)
	}
}
