package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// Version 37 orders log lists by created_at through a (filter, created_at)
	// index each, adds the project one, and gives owners' project-less logs
	// their project, so a project's logs are exactly logs.project_id.
	// Upgrade with migrate -command schema37-prefill online first.
	// Version 36 adds models.video_capabilities and video_pricing on
	// model_sell_prices and provider_cost_prices (text JSON, added by
	// AutoMigrate), for the video gateway's per-model capability rows and
	// resolution / token / provider-cost video prices.
	// Version 35 adds project_auto_reloads (one auto-reload setting per
	// project: threshold, amount, monthly cap, saved card, pause) and
	// top_ups.auto_reload, the typed mark of an off-session reload.
	// Version 34 adds dashboard_usage_hourly_slices: hourly usage per project,
	// member, key, model, client, channel and group, written by the batched
	// consumer, for the overview's clients, the filtered user tab and project
	// usage. Upgrade with migrate -command schema34-prefill online first.
	// Version 33 turns JIT off for both databases, copies decision users onto
	// legacy routing outcomes, adds a TTFT histogram to hourly dashboard
	// totals, logs.effort_clamped (by trigger) and gateway_timing_daily.
	// Upgrade with migrate -command schema33-prefill online first.
	// Version 32 copies gateway_pre_ms/gateway_post_ms out of logs.other into
	// typed columns, kept current by a trigger, for the daily timing check.
	// Version 31 adds hourly route health rows (route_health_hourly) and their
	// rollup state, backfills thirty days and drops the materialized health
	// snapshots they replace.
	// Version 30 stores routing facts as typed log columns (ro_*, rd_*),
	// backfills them from the JSON copies and points the routing_outcome and
	// routing_decision views at them, so no reader decodes JSON per row.
	// Version 29 retires the file storage fee: drops users.storage_quota_microusd
	// and rebuilds users.storage_bytes_used from the files table.
	// Version 28 adds per-output image pricing on customer and provider price sheets.
	// Version 27 adds immutable legacy opening balances and audited settlement
	// waivers without rewriting historical charges or customer balances.
	// Version 26 separates Core/Telemetry, partitions telemetry history and
	// adds durable ledger recovery and financial continuity records.
	// Version 25 adds the self-reported source on signup_attributions.
	// Version 24 lets a sell price follow the official price: follow_official,
	// price_ratio and pinned_fields on model_sell_prices.
	// Versions 23 and 22 changed extension tables only.
	// Version 21 adds the first-answer-token time to routing outcomes and
	// probes, and reasoning tokens to probes, for answer-only decode speed.
	// Version 20 changed extension tables only.
	// Version 19 adds tokens.decision_question_generation and durable decision refunds.
	// Version 18 changed extension tables only.
	// Version 17 adds users.home_surface.
	// Version 16 adds official-price, lane and last-failure fields to agent
	// sessions and their models.
	// Version 15 drops the retired routing-research and inherited fork columns,
	// adds user lifecycle timestamps, sessions and signup attribution.
	// Version 14 adds blog posts, blog media and launch kits.
	// Version 13 adds announcement publication audiences, API changes, and release versions.
	// Version 12 records the disputed and original charge amounts for proportional reversals.
	// Version 11 adds payment reversal and dispute hold fields. Version 10
	// retired startup seeders, so rollback across that floor remains unsafe.
	SchemaVersionCurrent = 37

	schemaVersionPrimary = "primary"
	schemaVersionLog     = "log"
)

// Migration support window (AGENTS.md, Deployment Notes): a deployment
// upgrades in one step from any schema version released in the last six
// months. A version's migration code may be deleted six months after it
// shipped; schemaUpgradeFloor then rises to the oldest version still handled,
// and a deployment below it upgrades through schemaUpgradeGateRelease first.
// The oldest version, 1, shipped on 2026-07-13, so nothing is refused yet;
// v15 and earlier (the new-api era cleanup) may go from 2027-03-30.
var (
	schemaUpgradeFloor       = 0
	schemaUpgradeGateRelease = ""
)

// checkSchemaUpgradeFloor refuses a stored schema older than this release can
// migrate from, naming the release to upgrade through.
func checkSchemaUpgradeFloor(scope string, stored int) error {
	if stored >= schemaUpgradeFloor {
		return nil
	}
	return fmt.Errorf(
		"%s schema version %d is older than version %d, the oldest this release upgrades from; upgrade to lazu %s first, then to this release",
		scope, stored, schemaUpgradeFloor, schemaUpgradeGateRelease,
	)
}

type SchemaVersion struct {
	Scope     string    `gorm:"primaryKey;size:64"`
	Version   int       `gorm:"not null"`
	AppliedAt time.Time `gorm:"not null"`
}

func migrationMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("LAZU_MIGRATION_MODE")))
	if mode == "" {
		return "auto"
	}
	return mode
}

func applyPrimarySchemaPolicy() error {
	switch migrationMode() {
	case "auto":
		return RunPrimaryMigrations()
	case "validate":
		return ValidateSchemaVersion()
	default:
		return fmt.Errorf("unsupported LAZU_MIGRATION_MODE %q: use auto or validate", migrationMode())
	}
}

func applyLogSchemaPolicy() error {
	switch migrationMode() {
	case "auto":
		return RunLogMigrations()
	case "validate":
		return validateSchemaVersion(TelemetryDB(), schemaVersionLog)
	default:
		return fmt.Errorf("unsupported LAZU_MIGRATION_MODE %q: use auto or validate", migrationMode())
	}
}

// RunPrimaryMigrations is the explicit deployment migration entrypoint. It
// serializes schema changes across API instances and records the compatible
// schema version only after every migration succeeds.
func RunPrimaryMigrations() error {
	return runSchemaMigrations(CoreDB(), schemaVersionPrimary, migrateDB)
}

func RunLogMigrations() error {
	return runSchemaMigrations(TelemetryDB(), schemaVersionLog, migrateLOGDB)
}

func runSchemaMigrations(db *gorm.DB, scope string, migrate func() error) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	invalidateSchemaCatalogue(db)
	release, err := acquireMigrationLock(db, scope)
	if err != nil {
		return err
	}
	defer release()

	if db.Migrator().HasTable(&SchemaVersion{}) {
		var existing SchemaVersion
		err := db.Where("scope = ?", scope).First(&existing).Error
		if err == nil && existing.Version == SchemaVersionCurrent {
			return cacheValidatedSchemaVersion(db, scope)
		}
		if err == nil && existing.Version > SchemaVersionCurrent {
			return fmt.Errorf(
				"%s schema version %d is newer than application version %d; refusing to downgrade",
				scope,
				existing.Version,
				SchemaVersionCurrent,
			)
		}
		if err == nil {
			if err := checkSchemaUpgradeFloor(scope, existing.Version); err != nil {
				return err
			}
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read existing %s schema version: %w", scope, err)
		}
	}
	if err := db.AutoMigrate(&SchemaVersion{}); err != nil {
		return fmt.Errorf("prepare schema version table: %w", err)
	}
	if err := migrate(); err != nil {
		return err
	}
	record := SchemaVersion{
		Scope:     scope,
		Version:   SchemaVersionCurrent,
		AppliedAt: time.Now().UTC(),
	}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "scope"}},
		DoUpdates: clause.AssignmentColumns([]string{"version", "applied_at"}),
	}).Create(&record).Error; err != nil {
		return fmt.Errorf("record %s schema version: %w", scope, err)
	}
	return cacheValidatedSchemaVersion(db, scope)
}

func ValidateSchemaVersion() error {
	return validateSchemaVersion(CoreDB(), schemaVersionPrimary)
}

// ValidateLogSchemaVersion verifies a separately configured log database.
// When logs share the primary database, the primary schema check is sufficient.
func ValidateLogSchemaVersion() error {
	if TelemetryDB() == nil {
		return nil
	}
	return validateSchemaVersion(TelemetryDB(), schemaVersionLog)
}

func validateSchemaVersion(db *gorm.DB, scope string) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if schemaVersionAlreadyValidated(db, scope) {
		return nil
	}
	if !db.Migrator().HasTable(&SchemaVersion{}) {
		return fmt.Errorf("%s schema is not initialized; run lazu-migrate before starting the service", scope)
	}
	var record SchemaVersion
	if err := db.Where("scope = ?", scope).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%s schema version is missing; run lazu-migrate", scope)
		}
		return fmt.Errorf("read %s schema version: %w", scope, err)
	}
	if record.Version != SchemaVersionCurrent {
		return fmt.Errorf(
			"%s schema version %d is incompatible with application version %d; run lazu-migrate",
			scope,
			record.Version,
			SchemaVersionCurrent,
		)
	}
	if scope == schemaVersionPrimary && !db.Migrator().HasColumn(&Announcement{}, "delivery") {
		return fmt.Errorf("%s schema is missing announcements.delivery; run lazu-migrate before starting the service", scope)
	}
	if err := validateTeamProjectSchema(db, scope); err != nil {
		return err
	}
	return cacheValidatedSchemaVersion(db, scope)
}

func acquireMigrationLock(db *gorm.DB, scope string) (func(), error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("open migration lock connection: %w", err)
	}
	// Advisory locks must remain bound to one connection while GORM performs
	// the migration through the pool. Leave one transient migration connection
	// available when an operator configured a single-connection pool.
	maxOpenConnections := sqlDB.Stats().MaxOpenConnections
	if maxOpenConnections == 1 {
		sqlDB.SetMaxOpenConns(2)
	}
	restorePoolLimit := func() {
		if maxOpenConnections == 1 {
			sqlDB.SetMaxOpenConns(1)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		cancel()
		restorePoolLimit()
		return nil, fmt.Errorf("reserve migration lock connection: %w", err)
	}
	lockName := "lazu-schema-" + scope
	release := func() {
		defer cancel()
		switch db.Dialector.Name() {
		case "postgres":
			_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtext($1))", lockName)
		}
		_ = conn.Close()
		restorePoolLimit()
	}

	switch db.Dialector.Name() {
	case "postgres":
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtext($1))", lockName); err != nil {
			release()
			return nil, fmt.Errorf("acquire postgres migration lock: %w", err)
		}
	}
	return release, nil
}
