package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func gatewayReviewOpenPostgres(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
	return db
}

func TestPR380GatewayRejectsSameActualDatabaseThroughDifferentHandles(t *testing.T) {
	dsn := os.Getenv("TEST_GATEWAY_CORE_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL fixture required")
	}
	open := func(value string) *gorm.DB {
		db, err := gorm.Open(postgres.Open(value), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
		return db
	}
	core, alias := open(dsn), open(dsn)
	if err := validateGatewayStoreSeparation(context.Background(), core, alias); err == nil {
		t.Fatal("different handles to the same actual PostgreSQL database were admitted")
	}
	if target := os.Getenv("TEST_GATEWAY_TELEMETRY_DSN"); target != "" {
		if err := validateGatewayStoreSeparation(context.Background(), core, open(target)); err != nil {
			t.Fatal(err)
		}
	}
	if socket := os.Getenv("TEST_GATEWAY_CORE_UNIX_DSN"); socket != "" {
		t.Run("tcp_and_unix_socket", func(t *testing.T) {
			gatewayReviewAssertTCPUnixAlias(t, dsn, socket)
		})
	}
}

func gatewayReviewAssertTCPUnixAlias(t *testing.T, tcp, socket string) {
	t.Helper()
	core, alias := gatewayReviewOpenPostgres(t, tcp), gatewayReviewOpenPostgres(t, socket)
	var addressTCP, addressUnix string
	if err := core.Raw("SELECT COALESCE(inet_server_addr()::text,'unix')").Scan(&addressTCP).Error; err != nil {
		t.Fatal(err)
	}
	if err := alias.Raw("SELECT COALESCE(inet_server_addr()::text,'unix')").Scan(&addressUnix).Error; err != nil {
		t.Fatal(err)
	}
	if addressTCP == addressUnix {
		t.Fatal("fixture did not create genuinely distinct native transport addresses")
	}
	if err := validateGatewayStoreSeparation(context.Background(), core, alias); err == nil {
		t.Fatal("TCP and Unix-socket handles to one actual PostgreSQL database were admitted")
	}
}

func TestPR380GatewayRestrictedRoleIdentityFallback(t *testing.T) {
	source, target := os.Getenv("TEST_GATEWAY_CORE_DSN"), os.Getenv("TEST_GATEWAY_TELEMETRY_DSN")
	if source == "" || target == "" {
		t.Skip("two isolated PostgreSQL databases required")
	}
	admin := gatewayReviewOpenPostgres(t, source)
	role := fmt.Sprintf("migration_identity_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE ROLE " + role + " NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT").Error; err != nil {
		t.Fatal(err)
	}
	// This fixture owns only its unique role. Permission changes below remain
	// uncommitted and are always rolled back, invisible to other connections.
	defer func() {
		if err := admin.Exec("DROP ROLE " + role).Error; err != nil {
			t.Errorf("clean isolated role: %v", err)
		}
	}()
	telemetry := gatewayReviewOpenPostgres(t, target)
	rollback := errors.New("rollback isolated permission fixture")
	err := admin.Transaction(func(tx *gorm.DB) error {
		for _, statement := range []string{"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_control_system() FROM PUBLIC", "SET LOCAL ROLE " + role} {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		var permitted bool
		if err := tx.Raw("SELECT has_function_privilege('pg_catalog.pg_control_system()','EXECUTE')").Scan(&permitted).Error; err != nil || permitted {
			return fmt.Errorf("role fixture must lack cluster-control privilege: permitted=%v err=%v", permitted, err)
		}
		if err := validateGatewayStoreSeparation(context.Background(), tx, telemetry); err != nil {
			t.Errorf("distinct actual database names should remain usable by restricted roles: %v", err)
		}
		if err := validateGatewayStoreSeparation(context.Background(), admin, tx); err == nil || !strings.Contains(err.Error(), "cannot prove") {
			t.Errorf("same names without either cluster identity must fail closed: %v", err)
		}
		for _, statement := range []string{"SET LOCAL ROLE NONE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_control_system() TO " + role, "SET LOCAL ROLE " + role} {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		if err := tx.Raw("SELECT has_function_privilege('pg_catalog.pg_control_system()','EXECUTE')").Scan(&permitted).Error; err != nil || !permitted {
			return fmt.Errorf("explicit function grant unavailable: permitted=%v err=%v", permitted, err)
		}
		if err := validateGatewayStoreSeparation(context.Background(), admin, tx); err == nil || !strings.Contains(err.Error(), "same actual") {
			t.Errorf("explicitly granted stable cluster identity did not reject the same actual database: %v", err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
}

func TestPR380GatewayRejectsInvalidStoresBeforePricing(t *testing.T) {
	dsn := os.Getenv("TEST_GATEWAY_CORE_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL fixture required")
	}
	core, alias := gatewayReviewOpenPostgres(t, dsn), gatewayReviewOpenPostgres(t, dsn)
	oldCore, oldTelemetry := CoreDB(), ConfiguredTelemetryDB()
	SetCoreDB(core)
	SetTelemetryDB(alias)
	t.Cleanup(func() { SetCoreDB(oldCore); SetTelemetryDB(oldTelemetry) })
	if err := InitGatewayLedger(); err == nil || !strings.Contains(err.Error(), "same actual") {
		t.Fatalf("startup did not validate actual store separation before pricing: %v", err)
	}
}

func TestPR380GatewayStableClusterIdentityComparison(t *testing.T) {
	for _, item := range []struct {
		name       string
		core, logs gatewayDatabaseIdentity
		allowed    bool
	}{
		{"same_cluster_and_database", gatewayDatabaseIdentity{Database: "gateway", ClusterID: "1"}, gatewayDatabaseIdentity{Database: "gateway", ClusterID: "1"}, false},
		{"different_clusters_same_name", gatewayDatabaseIdentity{Database: "gateway", ClusterID: "1"}, gatewayDatabaseIdentity{Database: "gateway", ClusterID: "2"}, true},
		{"restricted_distinct_names", gatewayDatabaseIdentity{Database: "core"}, gatewayDatabaseIdentity{Database: "telemetry"}, true},
		{"missing_core_cluster", gatewayDatabaseIdentity{Database: "gateway"}, gatewayDatabaseIdentity{Database: "gateway", ClusterID: "1"}, false},
		{"missing_telemetry_cluster", gatewayDatabaseIdentity{Database: "gateway", ClusterID: "1"}, gatewayDatabaseIdentity{Database: "gateway"}, false},
		{"missing_database_name", gatewayDatabaseIdentity{ClusterID: "1"}, gatewayDatabaseIdentity{Database: "gateway", ClusterID: "2"}, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			if err := compareGatewayDatabaseIdentities(item.core, item.logs); (err == nil) != item.allowed {
				t.Fatalf("identity decision differs: allowed=%v err=%v", item.allowed, err)
			}
		})
	}
}

func TestPR380GatewayIdentityQueryPreservesCancellation(t *testing.T) {
	dsn := os.Getenv("TEST_GATEWAY_CORE_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL fixture required")
	}
	core, telemetry := gatewayReviewOpenPostgres(t, dsn), gatewayReviewOpenPostgres(t, dsn)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := validateGatewayStoreSeparation(ctx, core, telemetry)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), dsn) {
		t.Fatalf("startup identity error lost cancellation or exposed connection settings: %v", err)
	}
}
