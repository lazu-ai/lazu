package model

import (
	"fmt"
	"math"

	"gorm.io/gorm"
)

// A request whose reasoning effort the gateway lowered to a cap is marked
// "effort_clamped": true in its log's Other document (appendEffortCapInfo).
// The console counts these as 被降 for a member, a Key and a log filter, and
// used to find them with other LIKE over every row in range: the team member
// page read 1.7 GB of one heavy user's month for it on 2026-10-15.
//
// A trigger copies the mark into logs.effort_clamped whenever Other is
// written, as gateway_timing_columns.go does for the timings: TRUE when the
// document says so, NULL otherwise, so the partial index holds only the
// clamped rows.

const (
	effortClampedFunction = "lazu_log_effort_clamped"
	effortClampedTrigger  = "lazu_logs_effort_clamped"
	effortClampedIndex    = "logs_effort_clamped"
	// logsUserRequestIndex serves a member's newest log, which the team page
	// reads per member (readProjectMemberLastActive).
	logsUserRequestIndex = "logs_user_request_ts"
)

// effortClampedCondition selects clamped requests. PostgreSQL keeps the
// column by trigger; the SQLite development store has no trigger and still
// matches the marker in Other.
func effortClampedCondition(db *gorm.DB) string {
	if db.Dialector.Name() == "postgres" {
		return "logs.effort_clamped"
	}
	return `logs.other LIKE '%"effort_clamped":true%'`
}

const effortClampedFunctionSQL = `CREATE OR REPLACE FUNCTION ` + effortClampedFunction + `(other text) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
	doc jsonb;
BEGIN
	IF other IS NULL OR strpos(other, 'effort_clamped') = 0 THEN
		RETURN NULL;
	END IF;
	BEGIN
		doc := other::jsonb;
	EXCEPTION WHEN others THEN
		RETURN NULL;
	END;
	IF jsonb_typeof(doc) = 'object' AND doc -> 'effort_clamped' = 'true'::jsonb THEN
		RETURN TRUE;
	END IF;
	RETURN NULL;
END $$`

const effortClampedTriggerFunctionSQL = `CREATE OR REPLACE FUNCTION ` + effortClampedTrigger + `() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	NEW.effort_clamped := ` + effortClampedFunction + `(NEW.other);
	RETURN NEW;
END $$`

// installEffortClampedColumn adds the column, the functions and the trigger.
// Every statement is idempotent and none rewrites or scans the table.
func installEffortClampedColumn(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE logs ADD COLUMN IF NOT EXISTS effort_clamped boolean",
		effortClampedFunctionSQL,
		effortClampedTriggerFunctionSQL,
		"DROP TRIGGER IF EXISTS " + effortClampedTrigger + " ON logs",
		"CREATE TRIGGER " + effortClampedTrigger + " BEFORE INSERT OR UPDATE OF other ON logs FOR EACH ROW EXECUTE FUNCTION " + effortClampedTrigger + "()",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("effort clamped column: %w", err)
		}
	}
	return nil
}

// createSchema33LogIndexes builds the partial index of clamped requests and
// the per-user request index. On the partitioned table each is built on every
// partition while holding off writes, so they belong to the stopped-writer
// schema step.
func createSchema33LogIndexes(tx *gorm.DB) error {
	for _, statement := range []string{
		"CREATE INDEX IF NOT EXISTS " + effortClampedIndex + " ON logs (request_ts) WHERE effort_clamped",
		"CREATE INDEX IF NOT EXISTS " + logsUserRequestIndex + " ON logs (user_id, request_ts)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("schema 33 log index: %w", err)
		}
	}
	return nil
}

// backfillEffortClampedColumn marks rows written before the trigger, a day of
// request_ts per statement from the next row still waiting. A second run
// costs one empty probe.
func backfillEffortClampedColumn(db *gorm.DB) error {
	const waiting = "strpos(logs.other, 'effort_clamped') > 0 AND logs.effort_clamped IS NULL AND " + effortClampedFunction + "(logs.other)"
	update := "UPDATE logs SET effort_clamped = TRUE WHERE logs.request_ts >= ? AND logs.request_ts < ? AND " + waiting
	pending := "SELECT MIN(request_ts) AS next FROM logs WHERE request_ts >= ? AND " + waiting
	cursor := int64(math.MinInt64)
	for {
		var next struct{ Next *int64 }
		if err := db.Raw(pending, cursor).Scan(&next).Error; err != nil {
			return fmt.Errorf("effort clamped column: find pending: %w", err)
		}
		if next.Next == nil {
			return nil
		}
		to := *next.Next + routingFactBackfillSpan
		if to < *next.Next {
			to = math.MaxInt64
		}
		if err := db.Exec(update, *next.Next, to).Error; err != nil {
			return fmt.Errorf("effort clamped column: backfill from %d: %w", *next.Next, err)
		}
		cursor = to
	}
}
