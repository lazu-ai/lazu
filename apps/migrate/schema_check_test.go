package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

func schemaCheckFixture(t *testing.T, name, scope string, version int) string {
	t.Helper()
	dsn := "sqlite://" + filepath.Join(t.TempDir(), name+".sqlite")
	db, err := model.OpenMigrationDatabase(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SchemaVersion{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SchemaVersion{Scope: scope, Version: version, AppliedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	raw, _ := db.DB()
	_ = raw.Close()
	return dsn
}

func TestSchemaCheckReadsCoreAndConfiguredLogDatabaseWithoutMigrating(t *testing.T) {
	for _, mismatch := range []string{"none", "Core", "Telemetry"} {
		t.Run(mismatch, func(t *testing.T) {
			coreVersion, telemetryVersion := 26, 26
			if mismatch == "Core" {
				coreVersion = 23
			}
			if mismatch == "Telemetry" {
				telemetryVersion = 23
			}
			core := schemaCheckFixture(t, "core", "primary", coreVersion)
			telemetry := schemaCheckFixture(t, "telemetry", "log", telemetryVersion)
			t.Setenv("DATABASE_URL", core)
			t.Setenv("TELEMETRY_DATABASE_URL", "")
			t.Setenv("LOG_SQL_DSN", telemetry)
			err := checkConfiguredSchemas(context.Background(), 26)
			if mismatch == "none" && err != nil {
				t.Fatal(err)
			}
			if mismatch != "none" && (err == nil || !strings.Contains(err.Error(), mismatch)) {
				t.Fatalf("did not reject %s schema: %v", mismatch, err)
			}
			for _, fixture := range []struct {
				dsn, scope string
				version    int
			}{{core, "primary", coreVersion}, {telemetry, "log", telemetryVersion}} {
				db, err := model.OpenMigrationDatabase(fixture.dsn)
				if err != nil {
					t.Fatal(err)
				}
				var actual model.SchemaVersion
				if err := db.First(&actual, "scope = ?", fixture.scope).Error; err != nil || actual.Version != fixture.version {
					t.Fatalf("read-only schema check changed schema: %+v %v", actual, err)
				}
				raw, _ := db.DB()
				_ = raw.Close()
			}
		})
	}
}

func TestSchemaCheckRejectsUnknownExpectedVersion(t *testing.T) {
	if err := checkConfiguredSchemas(context.Background(), 0); err == nil {
		t.Fatal("schema check accepted unknown target version")
	}
}

func TestSchemaCheckPreservesEmbeddedSingleStoreReadOnly(t *testing.T) {
	for _, version := range []int{23, 24, 25, 26} {
		t.Run(fmt.Sprintf("schema_%d", version), func(t *testing.T) {
			core := schemaCheckFixture(t, "embedded", "primary", version)
			t.Setenv("DATABASE_URL", core)
			t.Setenv("TELEMETRY_DATABASE_URL", "")
			t.Setenv("LOG_SQL_DSN", "")
			if err := checkConfiguredSchemas(context.Background(), version); err != nil {
				t.Fatal(err)
			}
			db, err := model.OpenMigrationDatabase(core)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { raw, _ := db.DB(); _ = raw.Close() }()
			var actual []model.SchemaVersion
			if err := db.Find(&actual).Error; err != nil || len(actual) != 1 || actual[0].Version != version {
				t.Fatalf("embedded check changed persisted schema: %+v err=%v", actual, err)
			}
		})
	}
}

func TestSchemaCheckAcceptsPreparedSchema25AndRejectsHistoricalDefault(t *testing.T) {
	core := schemaCheckFixture(t, "legacy-core", "primary", 26)
	db, err := model.OpenMigrationDatabase(core)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { raw, _ := db.DB(); _ = raw.Close() }()
	t.Setenv("DATABASE_URL", core)
	t.Setenv("TELEMETRY_DATABASE_URL", "")
	t.Setenv("LOG_SQL_DSN", core)
	if err := model.MarkLegacySchemaRollbackToVersion(context.Background(), db, 25); err != nil {
		t.Fatal(err)
	}
	if err := checkConfiguredSchemas(context.Background(), 25); err != nil {
		t.Fatalf("prepared Core does not pass the actual schema25 image's version check: %v", err)
	}
	if err := model.MarkLegacySchemaRollback(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := checkConfiguredSchemas(context.Background(), 25); err == nil {
		t.Fatal("schema25 deployment accepted the historical schema23 rollback default")
	}
}

func schemaCheckPostgresFixture(t *testing.T, key string, scopes []string) string {
	t.Helper()
	dsn := os.Getenv(key)
	if dsn == "" {
		t.Skip("isolated PostgreSQL fixture required")
	}
	admin, err := model.OpenMigrationDatabase(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("schema_check_review_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + name).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + name + " CASCADE").Error; err != nil {
			t.Errorf("remove schema-check fixture: %v", err)
		}
		raw, _ := admin.DB()
		_ = raw.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", name)
	u.RawQuery = query.Encode()
	db, err := model.OpenMigrationDatabase(u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
	if err := db.AutoMigrate(&model.SchemaVersion{}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		if err := db.Create(&model.SchemaVersion{Scope: scope, Version: 26, AppliedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return u.String()
}

func TestSchemaCheckPostgresRejectsSameActualStoreBeforeDeployment(t *testing.T) {
	for _, transport := range []string{"same_url", "different_handle_alias"} {
		t.Run(transport, func(t *testing.T) {
			core := schemaCheckPostgresFixture(t, "TEST_GATEWAY_CORE_DSN", []string{"primary", "log"})
			telemetry := core
			if transport == "different_handle_alias" {
				u, _ := url.Parse(core)
				query := u.Query()
				query.Set("application_name", "schema-check-distinct-handle")
				u.RawQuery = query.Encode()
				telemetry = u.String()
			}
			t.Setenv("DATABASE_URL", core)
			t.Setenv("TELEMETRY_DATABASE_URL", "")
			t.Setenv("LOG_SQL_DSN", telemetry)
			if err := checkConfiguredSchemas(context.Background(), 26); err == nil || !strings.Contains(err.Error(), "same actual") {
				t.Fatalf("deployment schema gate accepted aliases of one actual PostgreSQL store: %v", err)
			}
		})
	}
}

func TestSchemaCheckPostgresAllowsDistinctActualStores(t *testing.T) {
	core := schemaCheckPostgresFixture(t, "TEST_GATEWAY_CORE_DSN", []string{"primary"})
	telemetry := schemaCheckPostgresFixture(t, "TEST_GATEWAY_TELEMETRY_DSN", []string{"log"})
	t.Setenv("DATABASE_URL", core)
	t.Setenv("TELEMETRY_DATABASE_URL", "")
	t.Setenv("LOG_SQL_DSN", telemetry)
	if err := checkConfiguredSchemas(context.Background(), 26); err != nil {
		t.Fatal(err)
	}
}
