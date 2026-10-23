package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Schema 37 makes every log list read only its own page.
//
// A list is ordered newest first by created_at, then id (orderLogsNewestFirst),
// and each filter it narrows by has a (column, created_at) index. On the
// partitioned table each partition's index then yields rows already in page
// order, and LIMIT stops after one page however wide the window and however
// sparse the matching rows. Ordered by id alone, PostgreSQL had to choose
// between walking the id index (throwing away every row of other projects,
// 40 000 rows for a five-row page on 2026-10-23) and reading every matching
// row before sorting.
//
// A project's logs were also found through a fallback, rows without a
// project written by the project's owner, which no index can serve in time
// order. Schema 37 gives those rows their owner's project, the one the
// console already showed them in, and new account logs are written with it
// (accountLog), so a project's logs are exactly logs.project_id.

const logProjectPrefillMarker = "telemetry_log_project_prefill_v37"

type logListIndex struct{ name, columns string }

// logListIndexes are the indexes the log lists are ordered through. The
// user, token and channel ones are also declared as GORM tags, but on
// PostgreSQL those were built on the table that partitioning renamed away, so
// the partitioned table may lack them; each is built only when no valid index
// with the same key exists.
var logListIndexes = []logListIndex{
	{"logs_project_created", "project_id, created_at"},
	{"logs_user_created", "user_id, created_at"},
	{"logs_token_created", "token_id, created_at"},
	{"logs_channel_created", "channel_id, created_at"},
}

// ensureLogListIndexes builds the missing list indexes. Online, each
// partition's index is built concurrently and attached, so writers are never
// blocked; offline (writers stopped) it builds them directly. SQLite has the
// GORM-tagged indexes on its single table and needs only the project one.
func ensureLogListIndexes(db *gorm.DB, online bool) error {
	if db.Dialector.Name() != "postgres" {
		return db.Exec("CREATE INDEX IF NOT EXISTS " + logListIndexes[0].name + " ON logs (" + logListIndexes[0].columns + ")").Error
	}
	for _, index := range logListIndexes {
		if err := ensurePartitionedLogIndex(db, index, online); err != nil {
			return fmt.Errorf("schema 37: index %s: %w", index.name, err)
		}
	}
	return nil
}

// logsHasValidIndexOn reports whether logs has a valid, non-partial btree
// index whose key is exactly columns.
func logsHasValidIndexOn(db *gorm.DB, columns string) (bool, error) {
	var found bool
	err := db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_index x
		WHERE x.indrelid = 'logs'::regclass AND x.indisvalid AND x.indpred IS NULL
		AND pg_get_indexdef(x.indexrelid) LIKE ?)`, "% USING btree ("+columns+")%").Scan(&found).Error
	return found, err
}

// ensurePartitionedLogIndex creates the index on the parent only, then builds
// and attaches one index per partition. A partition created afterwards gets
// the index from its parent. Every step is idempotent, so an interrupted run,
// online or not, is finished by the next one.
func ensurePartitionedLogIndex(db *gorm.DB, index logListIndex, online bool) error {
	if found, err := logsHasValidIndexOn(db, index.columns); err != nil || found {
		return err
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS " + index.name + " ON ONLY logs (" + index.columns + ")").Error; err != nil {
		return err
	}
	var partitions []string
	if err := db.Raw(`SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'logs'::regclass ORDER BY c.relname`).Scan(&partitions).Error; err != nil {
		return err
	}
	concurrently := ""
	if online {
		concurrently = "CONCURRENTLY "
	}
	for _, partition := range partitions {
		var attached bool
		if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_inherits i JOIN pg_index x ON x.indexrelid = i.inhrelid
			WHERE i.inhparent = ?::regclass AND x.indrelid = ?::regclass)`, index.name, partition).Scan(&attached).Error; err != nil {
			return err
		}
		if attached {
			continue
		}
		child := partition + "_" + strings.TrimPrefix(index.name, "logs_")
		// A concurrent build that failed leaves an invalid index behind.
		var invalid bool
		if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_index x JOIN pg_class c ON c.oid = x.indexrelid
			WHERE c.relname = ? AND c.relnamespace = (SELECT relnamespace FROM pg_class WHERE oid = 'logs'::regclass) AND NOT x.indisvalid)`, child).Scan(&invalid).Error; err != nil {
			return err
		}
		if invalid {
			if err := db.Exec("DROP INDEX " + concurrently + "IF EXISTS " + child).Error; err != nil {
				return err
			}
		}
		if err := db.Exec("CREATE INDEX " + concurrently + "IF NOT EXISTS " + child + " ON " + partition + " (" + index.columns + ")").Error; err != nil {
			return err
		}
		if err := db.Exec("ALTER INDEX " + index.name + " ATTACH PARTITION " + child).Error; err != nil {
			return err
		}
	}
	var valid bool
	if err := db.Raw("SELECT indisvalid FROM pg_index WHERE indexrelid = ?::regclass", index.name).Scan(&valid).Error; err != nil {
		return err
	}
	if !valid {
		return errors.New("index is not valid after every partition was attached")
	}
	return nil
}

// backfillLogProjectIDs gives each log an owner wrote without a project the
// owner's project. Each user owns at most one project, so the mapping is
// exact. from is a request_ts in milliseconds; on PostgreSQL rows are updated
// a day of request_ts at a time from the next row still waiting.
func backfillLogProjectIDs(core, telemetry *gorm.DB, from int64) error {
	if core == nil || telemetry == nil {
		return errors.New("schema 37: both databases are required")
	}
	// A Core without projects has no owner whose logs could need one.
	if !core.Migrator().HasTable(&Project{}) {
		return nil
	}
	var owners []struct {
		ID          int
		OwnerUserID int
	}
	if err := core.Model(&Project{}).Select("id", "owner_user_id").Where("owner_user_id > 0").Order("id").Scan(&owners).Error; err != nil {
		return fmt.Errorf("schema 37: read project owners: %w", err)
	}
	if len(owners) == 0 {
		return nil
	}
	const waiting = "(logs.project_id = 0 OR logs.project_id IS NULL)"
	if telemetry.Dialector.Name() != "postgres" {
		for _, owner := range owners {
			if err := telemetry.Exec("UPDATE logs SET project_id = ? WHERE user_id = ? AND "+waiting, owner.ID, owner.OwnerUserID).Error; err != nil {
				return fmt.Errorf("schema 37: backfill owner %d: %w", owner.OwnerUserID, err)
			}
		}
		return nil
	}
	projectIDs, userIDs := make([]int, len(owners)), make([]int, len(owners))
	for i, owner := range owners {
		projectIDs[i], userIDs[i] = owner.ID, owner.OwnerUserID
	}
	users, projects := postgresIntArray(userIDs), postgresIntArray(projectIDs)
	pending := "SELECT MIN(logs.request_ts) AS next FROM logs WHERE logs.request_ts >= ? AND " + waiting + " AND logs.user_id = ANY(?::int[])"
	update := `UPDATE logs SET project_id = owner.project_id
		FROM unnest(?::int[], ?::int[]) AS owner(user_id, project_id)
		WHERE logs.request_ts >= ? AND logs.request_ts < ? AND ` + waiting + ` AND logs.user_id = owner.user_id`
	cursor := from
	for {
		var next struct{ Next *int64 }
		if err := telemetry.Raw(pending, cursor, users).Scan(&next).Error; err != nil {
			return fmt.Errorf("schema 37: find pending logs: %w", err)
		}
		if next.Next == nil {
			return nil
		}
		to := *next.Next + routingFactBackfillSpan
		if to < *next.Next {
			to = math.MaxInt64
		}
		if err := telemetry.Exec(update, users, projects, *next.Next, to).Error; err != nil {
			return fmt.Errorf("schema 37: backfill from %d: %w", *next.Next, err)
		}
		if to == math.MaxInt64 {
			return nil
		}
		cursor = to
	}
}

// PrefillSchema37 is the online half of the schema-37 upgrade. With the
// previous release still serving it builds the list indexes partition by
// partition without blocking writes, then gives every owner's project-less
// log its project. The previous release reads a project's logs through the
// fallback that already matched those rows, so it sees no change. It records
// when it started, so the stopped step only revisits what was written since;
// running it twice is harmless.
func PrefillSchema37(ctx context.Context, core, telemetry *gorm.DB) error {
	if telemetry == nil || telemetry.Dialector.Name() != "postgres" {
		return errors.New("schema 37 prefill needs the PostgreSQL Telemetry database")
	}
	started := time.Now().UTC()
	session := schema33Session(ctx, telemetry)
	if err := ensureLogListIndexes(session, true); err != nil {
		return err
	}
	if err := backfillLogProjectIDs(core.WithContext(ctx), session, math.MinInt64); err != nil {
		return err
	}
	return session.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}}, DoUpdates: clause.AssignmentColumns([]string{"version", "applied_at"})}).
		Create(&SchemaVersion{Scope: logProjectPrefillMarker, Version: int(started.Unix() / 3600), AppliedAt: started}).Error
}

// migrateSchema37Telemetry is the stopped-writer step. After a prefill it
// finds the indexes built and backfills only rows written since the prefill
// began, less a day for late writes; without one it does all of it.
func migrateSchema37Telemetry(db *gorm.DB) error {
	return runOneTimeMigration(db, "telemetry_log_lists_v37", func() error {
		session := schema33Session(context.Background(), db)
		if err := ensureLogListIndexes(session, false); err != nil {
			return err
		}
		from := int64(math.MinInt64)
		var prefill SchemaVersion
		if err := session.Where("scope = ?", logProjectPrefillMarker).Limit(1).Find(&prefill).Error; err != nil {
			return err
		}
		if prefill.Scope != "" {
			from = (int64(prefill.Version)*3600 - 86400) * 1000
		}
		return backfillLogProjectIDs(coreDB, session, from)
	})
}
