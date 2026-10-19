package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

// Run the actual schema command in another process so no initialized Redis,
// cached settings or schema catalogue from other tests can hide a dependency.
func TestOfflineSchemaCommandWithoutRedis(t *testing.T) {
	if os.Getenv("LAZU_TEST_OFFLINE_SCHEMA_CHILD") == "1" {
		if common.RDB != nil {
			t.Fatal("offline child unexpectedly has a Redis client")
		}
		os.Args = []string{os.Args[0], "-command", "schema", "-log-dir", os.Getenv("LAZU_TEST_OFFLINE_LOG_DIR")}
		main()
		if common.RDB != nil {
			t.Fatal("schema command initialized Redis")
		}
		return
	}
	coreDSN, telemetryDSN := os.Getenv("TEST_GATEWAY_CORE_DSN"), os.Getenv("TEST_GATEWAY_TELEMETRY_DSN")
	if coreDSN == "" || telemetryDSN == "" {
		t.Skip("isolated PostgreSQL test DSNs are not configured")
	}
	for _, withHistory := range []bool{false, true} {
		name := "empty"
		if withHistory {
			name = "historical"
		}
		t.Run(name, func(t *testing.T) {
			core, coreURL := offlineSchemaFixture(t, coreDSN, "core")
			telemetry, telemetryURL := offlineSchemaFixture(t, telemetryDSN, "telemetry")
			if withHistory {
				if err := core.AutoMigrate(&model.User{}); err != nil {
					t.Fatal(err)
				}
				if err := core.Create(&model.User{Id: 1, Username: "offline-digest-owner", Status: common.UserStatusEnabled}).Error; err != nil {
					t.Fatal(err)
				}
				if err := telemetry.AutoMigrate(&model.Log{}); err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC().Truncate(time.Hour)
				logs := []model.Log{
					{PublicID: "offline-first-partial-hour", UserId: 1, Type: model.LogTypeConsume, CreatedAt: now.Add(-5*time.Hour + time.Minute).Unix(), AmountMicrousd: 7},
					{PublicID: "offline-complete-hour", UserId: 1, Type: model.LogTypeConsume, CreatedAt: now.Add(-3*time.Hour + time.Minute).Unix(), AmountMicrousd: 123, ModelName: "offline-digest-model"},
				}
				if err := telemetry.Create(&logs).Error; err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOfflineSchemaCommandWithoutRedis$")
				cmd.Dir = t.TempDir()
				cmd.Env = append(os.Environ(),
					"LAZU_TEST_OFFLINE_SCHEMA_CHILD=1", "LAZU_TEST_OFFLINE_LOG_DIR="+t.TempDir(),
					"DATABASE_URL="+coreURL, "TELEMETRY_DATABASE_URL="+telemetryURL,
					"APP_ENV=development", "PROCESS_ROLE=jobs", "GORM_LOG_LEVEL=silent", "LOG_LEVEL=error")
				output, err := cmd.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("offline schema command attempt %d failed: %v\n%s", attempt, err, output)
				}
				if !strings.Contains(string(output), "Lazu schema migration complete (version "+strconv.Itoa(model.SchemaVersionCurrent)+").") {
					t.Fatalf("schema command omitted its success result: %s", output)
				}
			}
			var marker model.Option
			if err := core.Where("key = ?", model.DigestRollupSinceOption).First(&marker).Error; err != nil {
				t.Fatal(err)
			}
			if since, err := strconv.ParseInt(marker.Value, 10, 64); err != nil || since <= 0 {
				t.Fatalf("offline schema did not persist digest completion: %+v", marker)
			}
			for _, check := range []struct {
				db    *gorm.DB
				scope string
			}{{core, "primary"}, {telemetry, "log"}} {
				var version model.SchemaVersion
				if err := check.db.Where("scope = ?", check.scope).First(&version).Error; err != nil {
					t.Fatal(err)
				}
				if version.Version != model.SchemaVersionCurrent {
					t.Fatalf("schema %s version = %d", check.scope, version.Version)
				}
			}
			if withHistory {
				var rollup model.DigestUsageHour
				if err := telemetry.Where("model_name = ?", "offline-digest-model").First(&rollup).Error; err != nil {
					t.Fatal(err)
				}
				if rollup.Spend != 123 || rollup.Requests != 1 {
					t.Fatalf("schema rerun duplicated or lost historical digest: %+v", rollup)
				}
			}
		})
	}
}

func offlineSchemaFixture(t *testing.T, dsn, kind string) (*gorm.DB, string) {
	t.Helper()
	admin, err := model.OpenMigrationDatabase(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("offline_schema_%s_%d", kind, time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + name).Error; err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	// The trigram extension's operator class lives in public; all application
	// tables are created in the first, isolated schema.
	q.Set("search_path", name+",public")
	u.RawQuery = q.Encode()
	db, err := model.OpenMigrationDatabase(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + name + " CASCADE").Error; err != nil {
			t.Errorf("remove offline schema fixture: %v", err)
		}
		raw, _ := db.DB()
		_ = raw.Close()
		raw, _ = admin.DB()
		_ = raw.Close()
	})
	return db, u.String()
}
