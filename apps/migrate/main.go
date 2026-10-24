package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"gorm.io/gorm"
)

func main() {
	command := flag.String("command", "schema", "schema, routing-columns-prefill, gateway-timing-prefill, schema33-prefill, schema34-prefill, schema37-prefill, schema-version, schema-check, tables, inventory, verify, copy-telemetry, partition-audit, partitions, precharge-report, precharge-classify, settlement-waiver-report, settlement-waive, prepare-legacy-rollback, sequences, backup-audit, ledger-report, ledger-bootstrap, ledger-recover, ledger-drain-telemetry, or ledger-resume")
	expectedVersion := flag.Int("expected-version", 0, "compiled target image schema version for the read-only deployment check")
	legacyVersion := flag.Int("legacy-schema-version", model.LegacySchemaRollbackDefault, "actual previous image schema for prepare-legacy-rollback (supported: 23, 24, 25; default 23)")
	scope := flag.String("scope", "core", "core or telemetry")
	retention := flag.Int("retention-days", 30, "request log detail retention days (default LOG_RETENTION_DAYS; 0 disables); routing/probe policies remain independent")
	spoolDirs := flag.String("spool-dirs", "", "comma-separated spool directories for every API and jobs instance")
	writersStopped := flag.Bool("writers-stopped", false, "attest every API, callback and jobs writer has been stopped for financial backup, recovery or rollback")
	ledgerProof := flag.String("ledger-proof", "", "JSON evidence classifying every accepted request and listing stopped writers with their spool directories")
	maxBatches := flag.Int("max-batches", 1000, "maximum telemetry batches to persist during a stopped-writer recovery drain")
	classificationBatch := flag.Int("classification-batch-size", 500, "keyset batch size for historical precharge classification (1-1000)")
	failureIDs := flag.String("failure-ids", "", "exact comma-separated manual legacy failure IDs to inventory for an approved waiver")
	waiverProof := flag.String("waiver-proof", "", "immutable JSON waiver batch with exact source hashes, amount cap and approval evidence")
	flag.Parse()
	if *command == "prepare-legacy-rollback" {
		if !*writersStopped {
			fail(fmt.Errorf("stop every API, callback and jobs writer and provide -writers-stopped before preparing rollback"))
		}
		if err := model.ValidateLegacySchemaRollbackVersion(*legacyVersion); err != nil {
			fail(err)
		}
	}
	if *command == "schema-version" {
		fmt.Println(model.SchemaVersionCurrent)
		return
	}
	_ = godotenv.Load(".env")
	common.InitEnv()
	retentionSpecified := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "retention-days" {
			retentionSpecified = true
		}
	})
	if !retentionSpecified {
		*retention = common.LogRetentionDays
	}
	logger.SetupLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()
	switch *command {
	case "routing-columns-prefill":
		// Online step before the schema-30 upgrade; see model.PrefillRoutingFactColumns.
		telemetry := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL"))
		if telemetry == "" {
			telemetry = strings.TrimSpace(os.Getenv("LOG_SQL_DSN"))
		}
		db, err := model.OpenMigrationDatabase(telemetry)
		if err != nil {
			fail(fmt.Errorf("open Telemetry database: %w", err))
		}
		started := time.Now()
		remaining, err := model.PrefillRoutingFactColumns(ctx, db, time.Now().Add(-24*time.Hour))
		if err != nil {
			fail(err)
		}
		fmt.Printf("Routing columns prefilled in %s; %d recent rows are left to the schema migration.\n", time.Since(started).Round(time.Second), remaining)
	case "gateway-timing-prefill":
		// Online step before the schema-32 upgrade; see model.PrefillGatewayTimingColumns.
		telemetry := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL"))
		if telemetry == "" {
			telemetry = strings.TrimSpace(os.Getenv("LOG_SQL_DSN"))
		}
		db, err := model.OpenMigrationDatabase(telemetry)
		if err != nil {
			fail(fmt.Errorf("open Telemetry database: %w", err))
		}
		started := time.Now()
		if err := model.PrefillGatewayTimingColumns(ctx, db); err != nil {
			fail(err)
		}
		fmt.Printf("Gateway timing columns prefilled in %s; the trigger keeps new rows current.\n", time.Since(started).Round(time.Second))
	case "schema33-prefill":
		// Online step before the schema-33 upgrade; see model.PrefillSchema33.
		telemetry := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL"))
		if telemetry == "" {
			telemetry = strings.TrimSpace(os.Getenv("LOG_SQL_DSN"))
		}
		db, err := model.OpenMigrationDatabase(telemetry)
		if err != nil {
			fail(fmt.Errorf("open Telemetry database: %w", err))
		}
		started := time.Now()
		if err := model.PrefillSchema33(ctx, db); err != nil {
			fail(err)
		}
		fmt.Printf("Schema 33 prefilled in %s; the schema step rebuilds only the most recent hours.\n", time.Since(started).Round(time.Second))
	case "schema34-prefill":
		// Online step before the schema-34 upgrade; see model.PrefillSchema34.
		telemetry := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL"))
		if telemetry == "" {
			telemetry = strings.TrimSpace(os.Getenv("LOG_SQL_DSN"))
		}
		db, err := model.OpenMigrationDatabase(telemetry)
		if err != nil {
			fail(fmt.Errorf("open Telemetry database: %w", err))
		}
		started := time.Now()
		if err := model.PrefillSchema34(ctx, db); err != nil {
			fail(err)
		}
		fmt.Printf("Schema 34 prefilled in %s; the schema step builds only the most recent hours.\n", time.Since(started).Round(time.Second))
	case "schema37-prefill":
		// Online step before the schema-37 upgrade; see model.PrefillSchema37.
		// It reads project owners from Core and writes Telemetry.
		core, err := model.OpenMigrationDatabase(os.Getenv("DATABASE_URL"))
		if err != nil {
			fail(fmt.Errorf("open Core database: %w", err))
		}
		telemetry := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL"))
		if telemetry == "" {
			telemetry = strings.TrimSpace(os.Getenv("LOG_SQL_DSN"))
		}
		if telemetry == "" {
			telemetry = os.Getenv("DATABASE_URL")
		}
		db, err := model.OpenMigrationDatabase(telemetry)
		if err != nil {
			fail(fmt.Errorf("open Telemetry database: %w", err))
		}
		started := time.Now()
		if err := model.PrefillSchema37(ctx, core, db); err != nil {
			fail(err)
		}
		fmt.Printf("Schema 37 prefilled in %s; the schema step backfills only logs written since.\n", time.Since(started).Round(time.Second))
	case "schema-check":
		if err := checkConfiguredSchemas(ctx, *expectedVersion); err != nil {
			fail(err)
		}
		fmt.Printf("Configured Core and Telemetry schemas match target version %d.\n", *expectedVersion)
	case "schema":
		common.IsMasterNode = true
		if err := os.Setenv("LAZU_MIGRATION_MODE", "auto"); err != nil {
			fail(err)
		}
		service.InitTokenEncoders()
		if err := model.InitDBForMigration(); err != nil {
			fail(err)
		}
		defer func() { _ = model.CloseDB() }()
		if err := model.InitMigrationOptionMap(); err != nil {
			fail(err)
		}
		if err := model.InitLogDB(); err != nil {
			fail(err)
		}
		fmt.Printf("Lazu schema migration complete (version %d).\n", model.SchemaVersionCurrent)
	case "tables", "inventory", "verify", "copy-telemetry", "partition-audit", "partitions", "precharge-report", "precharge-classify", "settlement-waiver-report", "settlement-waive", "prepare-legacy-rollback", "sequences", "backup-audit", "ledger-report", "ledger-bootstrap", "ledger-recover", "ledger-drain-telemetry", "ledger-resume":
		source, err := model.OpenMigrationDatabase(os.Getenv("LAZU_MIGRATION_SOURCE_URL"))
		if err != nil {
			fail(err)
		}
		defer func() { db, _ := source.DB(); _ = db.Close() }()
		switch *command {
		case "settlement-waiver-report":
			ids, err := parseFailureIDs(*failureIDs)
			if err != nil {
				fail(err)
			}
			items, err := model.InventoryLegacySettlementWaiverItems(ctx, source, ids)
			if err != nil {
				fail(err)
			}
			emit(items)
		case "settlement-waive":
			if *waiverProof == "" {
				fail(fmt.Errorf("provide -waiver-proof with the approved exact batch"))
			}
			payload, err := os.ReadFile(*waiverProof)
			if err != nil {
				fail(err)
			}
			var proof model.LedgerLegacySettlementWaiverBatch
			if err := json.Unmarshal(payload, &proof); err != nil {
				fail(err)
			}
			if err := common.InitRedisClient(); err != nil {
				fail(err)
			}
			if err := verifyInitialCutoverQuiet(ctx, source, splitDirectories(*spoolDirs), *writersStopped); err != nil {
				fail(err)
			}
			report, err := model.ApplyLegacySettlementWaivers(ctx, source, proof)
			if err != nil {
				fail(err)
			}
			emit(report)
		case "precharge-classify":
			if !*writersStopped {
				fail(fmt.Errorf("stop all previous-release financial writers and provide -writers-stopped before classifying historical precharges"))
			}
			if *ledgerProof != "" {
				payload, err := os.ReadFile(*ledgerProof)
				if err != nil {
					fail(err)
				}
				var proof model.LedgerRecoveryProof
				if err := json.Unmarshal(payload, &proof); err != nil {
					fail(err)
				}
				if err := model.ImportLegacyPrechargeClassifications(ctx, source, proof.LegacyPrecharges); err != nil {
					fail(err)
				}
			}
			report, err := model.ClassifyLegacyPrecharges(ctx, source, *classificationBatch)
			if err != nil {
				fail(err)
			}
			emitPrechargeClassificationReport(report)
		case "ledger-drain-telemetry":
			if !*writersStopped {
				fail(fmt.Errorf("stop every financial writer and provide -writers-stopped before draining recovery telemetry"))
			}
			if err := common.InitRedisClient(); err != nil {
				fail(err)
			}
			model.SetCoreDB(source)
			report, err := model.ReportLedgerContinuity(ctx, source, nil)
			if err != nil {
				fail(err)
			}
			if !report.Maintenance || len(report.LiveWriters) > 0 {
				fail(fmt.Errorf("recovery telemetry drain requires maintenance and no live writer leases"))
			}
			if err := model.BindLedgerContinuity(ctx, source, ledger.New(common.RDB, nil)); err != nil {
				fail(err)
			}
			telemetry, err := model.OpenMigrationDatabase(os.Getenv("LAZU_MIGRATION_TARGET_URL"))
			if err != nil {
				fail(err)
			}
			defer func() { raw, _ := telemetry.DB(); _ = raw.Close() }()
			model.SetTelemetryDB(telemetry)
			processed, err := model.DrainRelayEvents(ctx, "ledger-recovery-cli", *maxBatches)
			if err != nil {
				fail(err)
			}
			emit(map[string]int{"telemetry_events_persisted": processed})
			if err := model.VerifyRelayEventsDrained(ctx); err != nil {
				fail(err)
			}
		case "ledger-report", "ledger-bootstrap", "ledger-recover", "ledger-resume":
			if err := common.InitRedisClient(); err != nil {
				fail(err)
			}
			model.SetCoreDB(source)
			directories := []string{}
			for _, directory := range strings.Split(*spoolDirs, ",") {
				if directory = strings.TrimSpace(directory); directory != "" {
					directories = append(directories, directory)
				}
			}
			switch *command {
			case "ledger-report":
				report, err := model.ReportLedgerContinuity(ctx, source, directories)
				emit(report)
				if err != nil {
					fail(err)
				}
			case "ledger-bootstrap":
				var proof *model.LedgerRecoveryProof
				if *ledgerProof != "" {
					payload, err := os.ReadFile(*ledgerProof)
					if err != nil {
						fail(err)
					}
					proof = &model.LedgerRecoveryProof{}
					if err := json.Unmarshal(payload, proof); err != nil {
						fail(err)
					}
				}
				state, err := model.BootstrapLedgerContinuityWithProof(ctx, source, directories, *writersStopped, proof)
				if err != nil {
					fail(err)
				}
				emit(state)
			case "ledger-recover":
				if *ledgerProof == "" {
					fail(fmt.Errorf("ledger recovery requires -ledger-proof with retained evidence for every accepted request"))
				}
				payload, err := os.ReadFile(*ledgerProof)
				if err != nil {
					fail(err)
				}
				var proof model.LedgerRecoveryProof
				if err := json.Unmarshal(payload, &proof); err != nil {
					fail(err)
				}
				state, err := model.RecoverLedgerContinuity(ctx, source, directories, *writersStopped, proof)
				if err != nil {
					fail(err)
				}
				emit(state)
				fmt.Println("Recovery complete; maintenance remains enabled. Run ledger-resume after final drain verification.")
			case "ledger-resume":
				if err := model.ResumeLedgerContinuity(ctx, source, directories, *writersStopped); err != nil {
					fail(err)
				}
				fmt.Println("Financial continuity verified; admission maintenance cleared. Start API and jobs writers.")
			}
		case "backup-audit":
			if err := common.InitRedisClient(); err != nil {
				fail(err)
			}
			directories := []string{}
			for _, directory := range strings.Split(*spoolDirs, ",") {
				if directory = strings.TrimSpace(directory); directory != "" {
					directories = append(directories, directory)
				}
			}
			report, err := model.AuditGatewayBackup(ctx, source, directories, *writersStopped)
			if err != nil {
				fail(err)
			}
			emit(report)
		case "tables":
			tables, err := model.MigrationTableInventory(source, *scope)
			if err != nil {
				fail(err)
			}
			emit(tables)
		case "sequences":
			if err := model.ResetMigrationSequences(ctx, source); err != nil {
				fail(err)
			}
			fmt.Println("Migration sequences corrected.")
		case "prepare-legacy-rollback":
			if err := model.ValidateLegacySchemaRollbackSource(ctx, source, *legacyVersion); err != nil {
				fail(err)
			}
			if err := common.InitRedisClient(); err != nil {
				fail(err)
			}
			report, err := prepareLegacyRollbackSource(ctx, source, splitDirectories(*spoolDirs), *writersStopped)
			if err != nil {
				fail(err)
			}
			emit(report)
			telemetry, err := model.OpenMigrationDatabase(os.Getenv("LAZU_MIGRATION_TARGET_URL"))
			if err != nil {
				fail(err)
			}
			defer func() { raw, _ := telemetry.DB(); _ = raw.Close() }()
			if err := model.PrepareLegacyTelemetryRollback(ctx, telemetry); err != nil {
				fail(err)
			}
			if err := source.WithContext(ctx).AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
				fail(err)
			}
			if err := model.CopyTelemetryHistoryForRollback(ctx, telemetry, source); err != nil {
				fail(err)
			}
			if err := model.ResetMigrationSequences(ctx, source); err != nil {
				fail(err)
			}
			if err := model.PrepareLegacyTelemetryWrites(ctx, source); err != nil {
				fail(err)
			}
			if err := model.MarkLegacySchemaRollbackToVersion(ctx, source, *legacyVersion); err != nil {
				fail(err)
			}
			fmt.Printf("Legacy writable tables restored in core at schema %d; point the previous release's SQL and log URLs at this core store.\n", *legacyVersion)
		case "inventory":
			proof, err := model.BuildMigrationProof(ctx, source, *scope)
			if err != nil {
				fail(err)
			}
			emit(proof)
		case "verify", "copy-telemetry":
			target, err := model.OpenMigrationDatabase(os.Getenv("LAZU_MIGRATION_TARGET_URL"))
			if err != nil {
				fail(err)
			}
			defer func() { db, _ := target.DB(); _ = db.Close() }()
			if *command == "copy-telemetry" {
				if err := model.CopyTelemetryHistory(ctx, source, target); err != nil {
					fail(err)
				}
				fmt.Println("Telemetry copy complete; run verify before removing source tables.")
				break
			}
			sourceProof, err := model.BuildMigrationProof(ctx, source, *scope)
			if err != nil {
				fail(err)
			}
			targetProof, err := model.BuildMigrationProof(ctx, target, *scope)
			if err != nil {
				fail(err)
			}
			emit(map[string]interface{}{"source": sourceProof, "target": targetProof})
			if err := model.CompareMigrationProofs(sourceProof, targetProof); err != nil {
				fail(err)
			}
			fmt.Println("Migration verification passed.")
		case "partition-audit":
			if err := model.AuditTelemetryPartitions(ctx, source); err != nil {
				fail(err)
			}
			fmt.Println("Historical partition identities and complete row contents verified.")
		case "partitions":
			model.SetTelemetryDB(source)
			if err := model.MaintainTelemetryPartitions(ctx, time.Now().UTC(), *retention); err != nil {
				fail(err)
			}
			fmt.Println("Telemetry partition maintenance complete.")
		case "precharge-report":
			report, err := model.ReportLegacyPrecharges(ctx, source)
			if err != nil {
				fail(err)
			}
			cutover, err := model.ReportLedgerCutoverReadiness(ctx, source)
			if err != nil {
				fail(err)
			}
			emit(map[string]interface{}{"legacy_precharges": report, "ledger_cutover": cutover})
		}
	default:
		fail(fmt.Errorf("unknown migration command %q", *command))
	}
}

func checkConfiguredSchemas(ctx context.Context, expected int) error {
	if expected <= 0 {
		return fmt.Errorf("schema-check requires a positive -expected-version")
	}
	core := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if core == "" {
		core = strings.TrimSpace(os.Getenv("SQL_DSN"))
	}
	telemetry := strings.TrimSpace(os.Getenv("TELEMETRY_DATABASE_URL"))
	if telemetry == "" {
		telemetry = strings.TrimSpace(os.Getenv("LOG_SQL_DSN"))
	}
	if telemetry == "" {
		telemetry = core
	}
	stores := []struct {
		name, dsn, scope string
		db               *gorm.DB
	}{{name: "Core", dsn: core, scope: "primary"}, {name: "Telemetry", dsn: telemetry, scope: "log"}}
	for i := range stores {
		store := &stores[i]
		db, err := model.OpenMigrationDatabase(store.dsn)
		if err != nil {
			return fmt.Errorf("cannot open configured %s database for schema check", store.name)
		}
		raw, err := db.DB()
		if err != nil {
			return fmt.Errorf("cannot access configured %s database for schema check", store.name)
		}
		defer func() { _ = raw.Close() }()
		store.db = db
		scope := store.scope
		if store.name == "Telemetry" && telemetry == core {
			scope = "primary" // the legacy single-store schema owns both scopes
		}
		var version model.SchemaVersion
		err = db.WithContext(ctx).Where("scope = ?", scope).First(&version).Error
		if err != nil {
			return fmt.Errorf("configured %s schema version is unavailable", store.name)
		}
		if version.Version != expected {
			return fmt.Errorf("configured %s schema version %d is incompatible with target version %d", store.name, version.Version, expected)
		}
	}
	// Schema 26 PostgreSQL gateways require separate actual stores. Versions
	// alone do not prove that two URLs are independent; aliases can share the
	// same database. Keep embedded SQLite and earlier administrative checks.
	if expected >= 26 && stores[0].db.Dialector.Name() == "postgres" {
		if err := model.ValidateGatewayStoreSeparation(ctx, stores[0].db, stores[1].db); err != nil {
			return err
		}
	}
	return nil
}

func prepareLegacyRollbackSource(ctx context.Context, source *gorm.DB, directories []string, writersStopped bool) (model.LegacyLedgerRollbackReport, error) {
	if _, err := model.AuditGatewayBackup(ctx, source, directories, writersStopped); err != nil {
		return model.LegacyLedgerRollbackReport{}, fmt.Errorf("rollback backup audit: %w", err)
	}
	// Writers are stopped and the joint SQL/Redis/spool proof has passed.
	// Keep this offline handle free of the runtime identity lease deadline:
	// rollback rewrites derived projections, never wallets or Key balances.
	model.SetCoreDB(source)
	return model.PrepareLegacyLedgerRollback(ctx, writersStopped)
}

func splitDirectories(value string) []string {
	directories := []string{}
	for _, directory := range strings.Split(value, ",") {
		if directory = strings.TrimSpace(directory); directory != "" {
			directories = append(directories, directory)
		}
	}
	return directories
}

func emitPrechargeClassificationReport(report model.LegacyPrechargeClassificationReport) {
	emit(report)
	if report.ManualReview > 0 {
		fmt.Fprintln(os.Stderr, "Ambiguous rows remain for explicit operator evidence via -ledger-proof; no balances or refunds were changed.")
	}
}

func emit(value interface{}) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, "migration failed:", err); os.Exit(1) }

// Waivers precede the first continuity bootstrap. They use the same stopped-
// writer/drain boundary without pretending an opening already exists.
func verifyInitialCutoverQuiet(ctx context.Context, db *gorm.DB, dirs []string, stopped bool) error {
	if !stopped || len(dirs) == 0 {
		return fmt.Errorf("stop every API, callback and jobs writer; provide -writers-stopped and every -spool-dirs")
	}
	report, err := model.ReportLedgerContinuity(ctx, db, dirs)
	if err != nil {
		return err
	}
	if report.State != nil || report.HotValue != "" {
		return fmt.Errorf("legacy waivers must be approved before the first continuity bootstrap")
	}
	health, err := model.ReadLedgerHealth(ctx)
	if err != nil {
		return err
	}
	if health.Uncommitted != 0 || health.Reservations != 0 || health.Exposure != 0 || health.Dead != 0 {
		return fmt.Errorf("initial cutover requires all financial evidence drained")
	}
	dead, err := common.RDB.XLen(ctx, ledger.DeadStream).Result()
	if err != nil {
		return err
	}
	if dead != 0 {
		return fmt.Errorf("initial cutover requires empty financial dead-letter evidence")
	}
	for _, row := range []interface{}{&model.RedisLedgerOperation{}, &model.LedgerCreditSync{}, &model.LedgerCounterRefresh{}} {
		var count int64
		if err := db.WithContext(ctx).Model(row).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("initial cutover requires empty permanent operations and administrative outboxes")
		}
	}
	if !report.Maintenance || len(report.LiveWriters) != 0 || len(report.ActiveMediaJobIDs) != 0 || len(report.Reservations) != 0 || report.Uncommitted != 0 || len(report.UnresolvedSettlementOutboxIDs) != 0 {
		return fmt.Errorf("initial cutover requires maintenance and drained writers, media, reservations, journal and old outboxes")
	}
	for _, files := range report.SpoolFiles {
		if files != 0 {
			return fmt.Errorf("initial cutover requires every spool empty")
		}
	}
	return model.VerifyRelayEventsDrained(ctx)
}
func parseFailureIDs(raw string) ([]uint, error) {
	var ids []uint
	seen := map[uint]bool{}
	for _, part := range strings.Split(raw, ",") {
		id, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
		if err != nil || id == 0 || seen[uint(id)] {
			return nil, fmt.Errorf("failure IDs must be distinct positive integers")
		}
		seen[uint(id)] = true
		ids = append(ids, uint(id))
	}
	return ids, nil
}
