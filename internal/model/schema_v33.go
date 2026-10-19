package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Schema 33 removes the reads behind the slow-SQL mails of 2026-10-14 to
// 2026-10-16 (.local-results/slow-sql-mails-2026-10-16/report.md):
//
//   - JIT is off for both databases. The console's analytic reads are
//     misestimated CTE/UNION plans, and compiling one took 6.5 s of a 13.6 s
//     thirty-day TTFT read.
//   - Legacy routing outcomes carry their decision's user. 98.7% had none and
//     were attributed through a decision lookup per row, so a member's window
//     matched every unattributed outcome: 61 s for ninety days. Decisions are
//     kept ninety days, outcomes a year; copying the user now also keeps the
//     attribution after the decision expires.
//   - Hourly dashboard totals carry a TTFT histogram, so the overview reads
//     hours instead of raw outcomes.
//   - logs.effort_clamped and gateway_timing_daily (their own files).
//
// The heavy parts run online with the previous release serving
// (PrefillSchema33); the stopped-writer step only finishes what is left.

const (
	legacyOutcomeUsersMigration = "telemetry_legacy_outcome_users_v33"
	// dashboardTTFTPrefillMarker records the hour (Unix hours, in Version)
	// before which the prefill rebuilt every hour.
	dashboardTTFTPrefillMarker = "telemetry_dashboard_ttft_prefill_v33"
	// schema33ClosedHourLag is how long after its end an hour may still
	// receive outcomes; the prefill leaves younger hours to the schema step.
	schema33ClosedHourLag = 2 * time.Hour
)

// disableDatabaseJIT turns JIT off for every later session of the current
// database. It needs the database owner or a superuser; without either it
// only warns, as the reads stay correct, just slower.
func disableDatabaseJIT(db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return nil
	}
	if err := db.Exec(`DO $$ BEGIN EXECUTE format('ALTER DATABASE %I SET jit = off', current_database()); END $$`).Error; err != nil {
		common.SysWarn("[schema 33] could not turn JIT off for this database: " + err.Error())
	}
	return nil
}

// withoutJIT runs fn in a transaction whose session has JIT off, whatever the
// database default was when the connection opened, and no parallel workers:
// the production PostgreSQL container has Docker's 64 MB /dev/shm, which a
// parallel plan's or maintenance command's shared memory can exceed (the
// first schema-33 prefill failed so on its VACUUM).
func withoutJIT(db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, setting := range []string{"SET LOCAL jit = off", "SET LOCAL max_parallel_workers_per_gather = 0", "SET LOCAL max_parallel_maintenance_workers = 0"} {
			if err := tx.Exec(setting).Error; err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func schema33Session(ctx context.Context, db *gorm.DB) *gorm.DB {
	return db.WithContext(WithSlowQueryExpected(ctx))
}

// addSchema33Columns adds the new columns without scanning any table. Each
// statement takes a short exclusive lock; it waits at most three seconds and
// is retried, so a busy writer delays the prefill rather than the writer.
func addSchema33Columns(ctx context.Context, db *gorm.DB) error {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		lastErr = db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL lock_timeout = '3s'").Error; err != nil {
				return err
			}
			if err := tx.Exec("ALTER TABLE dashboard_usage_hourly_totals ADD COLUMN IF NOT EXISTS ttft_samples bigint DEFAULT 0, ADD COLUMN IF NOT EXISTS ttft_buckets text").Error; err != nil {
				return fmt.Errorf("schema 33: hourly TTFT columns: %w", err)
			}
			return installEffortClampedColumn(tx)
		})
		if lastErr == nil || ctx.Err() != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return lastErr
}

// backfillLegacyOutcomeUsers copies each unattributed legacy outcome's
// decision user onto it, a day of outcomes per statement. The decision is
// resolved as the dashboard resolves it: a typed decision on a log row, else a
// legacy decision no log has superseded. The legacy table takes no new rows,
// so this runs once.
func backfillLegacyOutcomeUsers(ctx context.Context, db *gorm.DB) error {
	if !db.Migrator().HasTable("routing_outcome_legacy") || !db.Migrator().HasTable("routing_decision_legacy") {
		return nil
	}
	return runOneTimeMigration(db, legacyOutcomeUsersMigration, func() error {
		var span struct{ First, Last *time.Time }
		if err := db.Raw("SELECT MIN(outcome_at) AS first, MAX(outcome_at) AS last FROM routing_outcome_legacy WHERE user_id IS NULL").Scan(&span).Error; err != nil {
			return fmt.Errorf("schema 33: legacy outcome span: %w", err)
		}
		if span.First == nil || span.Last == nil {
			return nil
		}
		// An outcome older than every decision still kept has nothing to
		// copy; retention removed its decision (two months of May to July
		// rows on production). A decision precedes its outcome.
		var decisions struct{ First *time.Time }
		if err := db.Raw("SELECT LEAST((SELECT MIN(decision_at) FROM routing_decision_legacy), (SELECT MIN(rd_decision_at) FROM logs WHERE rd_id IS NOT NULL)) AS first").Scan(&decisions).Error; err != nil {
			return fmt.Errorf("schema 33: oldest decision: %w", err)
		}
		if decisions.First == nil {
			return nil
		}
		if earliest := decisions.First.Add(-24 * time.Hour); span.First.Before(earliest) {
			span.First = &earliest
		}
		fromLogs := `UPDATE routing_outcome_legacy o SET user_id = snap.rd_user_id
			FROM logs snap
			WHERE o.outcome_at >= ? AND o.outcome_at < ? AND o.user_id IS NULL
				AND snap.rd_id = o.decision_id AND snap.rd_user_id IS NOT NULL`
		fromLegacy := `UPDATE routing_outcome_legacy o SET user_id = d.user_id
			FROM routing_decision_legacy d
			WHERE o.outcome_at >= ? AND o.outcome_at < ? AND o.user_id IS NULL
				AND d.id = o.decision_id AND d.user_id IS NOT NULL
				AND NOT EXISTS (SELECT 1 FROM logs newer WHERE newer.request_id = d.request_id AND newer.rd_id IS NOT NULL)`
		for day := span.First.UTC().Truncate(24 * time.Hour); !day.After(span.Last.UTC()); day = day.Add(24 * time.Hour) {
			if err := ctx.Err(); err != nil {
				return err
			}
			next := day.Add(24 * time.Hour)
			if err := withoutJIT(db, func(tx *gorm.DB) error {
				if err := tx.Exec(fromLogs, day, next).Error; err != nil {
					return err
				}
				return tx.Exec(fromLegacy, day, next).Error
			}); err != nil {
				return fmt.Errorf("schema 33: legacy outcome users on %s: %w", day.Format("2006-01-02"), err)
			}
		}
		// The update wrote a new version of millions of rows; reclaim them now
		// rather than when autovacuum gets to a table nothing else writes.
		return db.Exec("VACUUM (ANALYZE, PARALLEL 0) routing_outcome_legacy").Error
	})
}

// dashboardTTFTLogSQL and dashboardTTFTLegacySQL list every eligible outcome
// of a range with its user (dashboardTTFTEligible). They run after
// backfillLegacyOutcomeUsers, so an outcome's own user is the attribution the
// dashboard would resolve.
const dashboardTTFTLogSQL = `SELECT snap.ro_outcome_at AS outcome_at, snap.ro_user_id AS user_id, snap.ro_ttft_ms AS ttft_ms
	FROM logs snap WHERE snap.ro_request_id IS NOT NULL AND snap.ro_outcome_at >= ? AND snap.ro_outcome_at < ?
		AND snap.ro_was_streaming AND snap.ro_ttft_ms >= 0
		AND COALESCE(snap.ro_audio_output_tokens,0) = 0 AND COALESCE(snap.ro_image_output_tokens,0) = 0`

const dashboardTTFTLegacySQL = `SELECT old.outcome_at, old.user_id, old.ttft_ms
	FROM routing_outcome_legacy old WHERE old.outcome_at >= ? AND old.outcome_at < ?
		AND old.was_streaming AND old.ttft_ms >= 0
		AND COALESCE(old.audio_output_tokens,0) = 0 AND COALESCE(old.image_output_tokens,0) = 0
		AND NOT EXISTS (SELECT 1 FROM logs newer WHERE newer.request_id = old.request_id AND newer.ro_request_id IS NOT NULL OFFSET 0)`

type dashboardTTFTKey struct {
	hour  int64
	scope int
}

// dashboardTTFTRebuildSpan is one rebuild transaction; the busiest legacy
// days hold 600k outcomes.
const dashboardTTFTRebuildSpan = 6 * time.Hour

// rebuildDashboardTTFTHours recomputes the TTFT histogram of every hourly
// total in [from, to), six hours per transaction. Hours with no eligible
// outcome get an empty histogram; an hour or user with outcomes but no total
// yet gets a total holding only its TTFT.
func rebuildDashboardTTFTHours(ctx context.Context, db *gorm.DB, from, to time.Time) error {
	legacy := db.Migrator().HasTable("routing_outcome_legacy")
	for day := from.UTC().Truncate(dashboardTTFTRebuildSpan); day.Before(to); day = day.Add(dashboardTTFTRebuildSpan) {
		if err := ctx.Err(); err != nil {
			return err
		}
		start, end := day, day.Add(dashboardTTFTRebuildSpan)
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if err := withoutJIT(db, func(tx *gorm.DB) error {
			return rebuildDashboardTTFTRange(tx, start, end, legacy)
		}); err != nil {
			return fmt.Errorf("schema 33: TTFT hours from %s: %w", day.Format(time.RFC3339), err)
		}
	}
	return nil
}

func rebuildDashboardTTFTRange(tx *gorm.DB, start, end time.Time, legacy bool) error {
	var rows []struct {
		OutcomeAt time.Time
		UserID    *int
		TTFTMs    int64 `gorm:"column:ttft_ms"`
	}
	query, args := dashboardTTFTLogSQL, []interface{}{start, end}
	if legacy {
		query, args = query+" UNION ALL "+dashboardTTFTLegacySQL, append(args, start, end)
	}
	if err := tx.Raw(query, args...).Scan(&rows).Error; err != nil {
		return err
	}
	histograms := map[dashboardTTFTKey][]int64{}
	add := func(key dashboardTTFTKey, ms int64) {
		histograms[key] = addDashboardTTFTBucket(histograms[key], ms)
	}
	for _, row := range rows {
		hour := dashboardRollupHour(row.OutcomeAt.Unix())
		add(dashboardTTFTKey{hour, dashboardRollupScopeGlobal}, row.TTFTMs)
		if row.UserID != nil && *row.UserID > 0 {
			add(dashboardTTFTKey{hour, *row.UserID}, row.TTFTMs)
		}
	}
	empty := encodeDashboardTTFTBuckets(nil)
	if err := tx.Model(&DashboardUsageHourlyTotal{}).
		Where("bucket_hour >= ? AND bucket_hour < ?", dashboardRollupHour(start.Unix()), end.Unix()).
		UpdateColumns(map[string]interface{}{"ttft_samples": 0, "ttft_buckets": *empty}).Error; err != nil {
		return err
	}
	if len(histograms) == 0 {
		return nil
	}
	now := time.Now().UTC()
	totals := make([]DashboardUsageHourlyTotal, 0, len(histograms))
	for key, buckets := range histograms {
		var samples int64
		for _, count := range buckets {
			samples += count
		}
		totals = append(totals, DashboardUsageHourlyTotal{BucketHour: key.hour, ScopeUserID: key.scope, TTFTSamples: samples, TTFTBuckets: encodeDashboardTTFTBuckets(buckets), LatencyBuckets: encodeDashboardLatencyBuckets(nil), UpdatedAt: now})
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "bucket_hour"}, {Name: "scope_user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"ttft_samples", "ttft_buckets"}),
	}).CreateInBatches(totals, 200).Error
}

// PrefillSchema33 is the online half of the schema-33 upgrade. With the
// previous release still serving it adds the columns and the trigger, which
// the previous release's writes do not touch, then fills them for everything
// already written: effort marks, gateway timing days, legacy outcome users
// and the TTFT of every hour that has stopped receiving outcomes. It records
// no schema version; running it twice is harmless.
func PrefillSchema33(ctx context.Context, db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return errors.New("schema 33 prefill needs the PostgreSQL Telemetry database")
	}
	session := schema33Session(ctx, db)
	if err := disableDatabaseJIT(session); err != nil {
		return err
	}
	if err := addSchema33Columns(ctx, session); err != nil {
		return err
	}
	if err := session.AutoMigrate(&GatewayTimingDaily{}); err != nil {
		return fmt.Errorf("schema 33: gateway timing days: %w", err)
	}
	if err := withoutJIT(session, backfillEffortClampedColumn); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := postgresGatewayPerformanceDays(session, now.AddDate(0, 0, -31), now, now); err != nil {
		return fmt.Errorf("schema 33: gateway timing days: %w", err)
	}
	if err := backfillLegacyOutcomeUsers(ctx, session); err != nil {
		return err
	}
	through := now.Add(-schema33ClosedHourLag).Truncate(time.Hour)
	from, ok, err := dashboardTTFTOldestOutcome(session)
	if err != nil {
		return err
	}
	if ok {
		if err := rebuildDashboardTTFTHours(ctx, session, from, through); err != nil {
			return err
		}
	}
	return session.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}}, DoUpdates: clause.AssignmentColumns([]string{"version", "applied_at"})}).
		Create(&SchemaVersion{Scope: dashboardTTFTPrefillMarker, Version: int(through.Unix() / 3600), AppliedAt: now}).Error
}

// dashboardTTFTOldestOutcome is the hour of the oldest routing outcome.
func dashboardTTFTOldestOutcome(db *gorm.DB) (time.Time, bool, error) {
	query := "SELECT MIN(ro_outcome_at) AS first FROM logs WHERE ro_request_id IS NOT NULL"
	if db.Migrator().HasTable("routing_outcome_legacy") {
		query = "SELECT LEAST((" + query + "), (SELECT MIN(outcome_at) FROM routing_outcome_legacy)) AS first"
	}
	var oldest struct{ First *time.Time }
	if err := db.Raw(query).Scan(&oldest).Error; err != nil {
		return time.Time{}, false, err
	}
	if oldest.First == nil {
		return time.Time{}, false, nil
	}
	return oldest.First.UTC().Truncate(time.Hour), true, nil
}

// dashboardTTFTFirstUnfilledHour is the oldest hourly total without a TTFT
// histogram yet.
func dashboardTTFTFirstUnfilledHour(db *gorm.DB) (time.Time, bool, error) {
	var first struct{ First *int64 }
	if err := db.Model(&DashboardUsageHourlyTotal{}).Where("ttft_buckets IS NULL").Select("MIN(bucket_hour) AS first").Scan(&first).Error; err != nil {
		return time.Time{}, false, err
	}
	if first.First == nil {
		return time.Time{}, false, nil
	}
	return time.Unix(*first.First, 0).UTC(), true, nil
}

// migrateSchema33Telemetry is the stopped-writer step. After a prefill it
// rebuilds only the hours the prefill left and finds the rest done; without
// one it does all of it.
func migrateSchema33Telemetry(db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return runOneTimeMigration(db, "telemetry_dashboard_v33", func() error {
		ctx := context.Background()
		session := schema33Session(ctx, db)
		if err := disableDatabaseJIT(session); err != nil {
			return err
		}
		if err := session.Transaction(installEffortClampedColumn); err != nil {
			return err
		}
		if err := withoutJIT(session, backfillEffortClampedColumn); err != nil {
			return err
		}
		if err := withoutJIT(session, createSchema33LogIndexes); err != nil {
			return err
		}
		if err := backfillLegacyOutcomeUsers(ctx, session); err != nil {
			return err
		}
		// After a prefill, rebuild the hours from where it stopped, and any
		// older hour a late write left unfilled; without one, every hour
		// that has an outcome.
		from, ok, err := dashboardTTFTFirstUnfilledHour(session)
		if err != nil {
			return err
		}
		var prefill SchemaVersion
		if err := session.Where("scope = ?", dashboardTTFTPrefillMarker).Limit(1).Find(&prefill).Error; err != nil {
			return err
		}
		resume, found := time.Unix(int64(prefill.Version)*3600, 0).UTC(), prefill.Scope != ""
		if !found {
			resume, found, err = dashboardTTFTOldestOutcome(session)
			if err != nil {
				return err
			}
		}
		if found && (!ok || resume.Before(from)) {
			from, ok = resume, true
		}
		if ok {
			if err := rebuildDashboardTTFTHours(ctx, session, from, time.Now().UTC().Add(time.Hour).Truncate(time.Hour)); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		if _, err := postgresGatewayPerformanceDays(session, now.AddDate(0, 0, -31), now, now); err != nil {
			return fmt.Errorf("schema 33: gateway timing days: %w", err)
		}
		return nil
	})
}

func migrateSchema33Core(db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return runOneTimeMigration(db, "core_jit_off_v33", func() error { return disableDatabaseJIT(db) })
}
