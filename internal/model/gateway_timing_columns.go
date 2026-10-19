package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"
)

// The gateway's preparation and post-response times (gateway_pre_ms and
// gateway_post_ms) reach a log's Other document from several writers: the
// request's own facts, late diagnostics merged with jsonb ||, and a
// provisional carrier merged on conflict. The daily timing check used to
// decode Other for every row of eight days, 8.4 s on 2026-10-14 for 185k rows.
//
// A trigger copies the two numbers into typed columns whenever Other is
// written, so every writer, including the SQL-side merges Go never sees,
// keeps them in step. Only a JSON number counts; anything else is NULL,
// as is a document that is not valid JSON.

const (
	gatewayTimingFunction = "lazu_log_gateway_timing"
	gatewayTimingTrigger  = "lazu_logs_gateway_timing"
)

// gatewayTimingFunctionSQL reads both timings from an Other document in one
// parse. The strpos check skips the parse for rows that carry neither.
const gatewayTimingFunctionSQL = `CREATE OR REPLACE FUNCTION ` + gatewayTimingFunction + `(other text) RETURNS float8[]
LANGUAGE plpgsql IMMUTABLE PARALLEL SAFE AS $$
DECLARE
	doc jsonb;
BEGIN
	IF other IS NULL OR strpos(other, 'gateway_') = 0 THEN
		RETURN ARRAY[NULL, NULL]::float8[];
	END IF;
	BEGIN
		doc := other::jsonb;
	EXCEPTION WHEN others THEN
		RETURN ARRAY[NULL, NULL]::float8[];
	END;
	IF jsonb_typeof(doc) <> 'object' THEN
		RETURN ARRAY[NULL, NULL]::float8[];
	END IF;
	RETURN ARRAY[
		CASE WHEN jsonb_typeof(doc -> 'gateway_pre_ms') = 'number' THEN (doc ->> 'gateway_pre_ms')::float8 END,
		CASE WHEN jsonb_typeof(doc -> 'gateway_post_ms') = 'number' THEN (doc ->> 'gateway_post_ms')::float8 END
	];
END $$`

const gatewayTimingTriggerFunctionSQL = `CREATE OR REPLACE FUNCTION ` + gatewayTimingTrigger + `() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
	timing float8[];
BEGIN
	timing := ` + gatewayTimingFunction + `(NEW.other);
	NEW.gateway_pre_ms := timing[1];
	NEW.gateway_post_ms := timing[2];
	RETURN NEW;
END $$`

// installGatewayTimingColumns adds the columns, the functions and the trigger.
// Every statement is idempotent; the column additions are metadata-only.
// Triggers on a partitioned table are cloned to its current and future
// partitions.
func installGatewayTimingColumns(tx *gorm.DB) error {
	statements := []string{
		"ALTER TABLE logs ADD COLUMN IF NOT EXISTS gateway_pre_ms float8, ADD COLUMN IF NOT EXISTS gateway_post_ms float8",
		gatewayTimingFunctionSQL,
		gatewayTimingTriggerFunctionSQL,
		"DROP TRIGGER IF EXISTS " + gatewayTimingTrigger + " ON logs",
		"CREATE TRIGGER " + gatewayTimingTrigger + " BEFORE INSERT OR UPDATE OF other ON logs FOR EACH ROW EXECUTE FUNCTION " + gatewayTimingTrigger + "()",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("gateway timing columns: %w", err)
		}
	}
	return nil
}

// backfillGatewayTimingColumnsBefore fills the columns of rows admitted before
// until (a request_ts), a day of request_ts per statement starting at the next
// row still waiting. A row already filled, or one whose Other names neither
// timing, is skipped, so a second run costs one empty probe.
func backfillGatewayTimingColumnsBefore(db *gorm.DB, until int64) error {
	const waiting = "strpos(logs.other, 'gateway_') > 0 AND logs.gateway_pre_ms IS NULL AND logs.gateway_post_ms IS NULL"
	update := "UPDATE logs SET (gateway_pre_ms, gateway_post_ms) = (SELECT timing[1], timing[2] FROM (SELECT " + gatewayTimingFunction + "(logs.other) AS timing) parsed)" +
		" WHERE logs.request_ts >= ? AND logs.request_ts < ? AND " + waiting
	pending := "SELECT MIN(request_ts) AS next FROM logs WHERE request_ts >= ? AND request_ts < ? AND " + waiting
	cursor := int64(math.MinInt64)
	for {
		var next struct{ Next *int64 }
		if err := db.Raw(pending, cursor, until).Scan(&next).Error; err != nil {
			return fmt.Errorf("gateway timing columns: find pending: %w", err)
		}
		if next.Next == nil {
			return nil
		}
		to := *next.Next + routingFactBackfillSpan
		if to > until || to < *next.Next {
			to = until
		}
		if err := db.Exec(update, *next.Next, to).Error; err != nil {
			return fmt.Errorf("gateway timing columns: backfill from %d: %w", *next.Next, err)
		}
		cursor = to
	}
}

// PrefillGatewayTimingColumns is the online half of the schema-32 upgrade.
// With the previous release still serving it installs the trigger, which from
// then on keeps every written row current, and fills the rows already there.
// It records no schema version; running it twice is harmless.
func PrefillGatewayTimingColumns(ctx context.Context, db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return errors.New("gateway timing prefill needs the PostgreSQL Telemetry database")
	}
	session := db.WithContext(WithSlowQueryExpected(ctx))
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		lastErr = session.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL lock_timeout = '3s'").Error; err != nil {
				return err
			}
			return installGatewayTimingColumns(tx)
		})
		if lastErr == nil || ctx.Err() != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if lastErr != nil {
		return lastErr
	}
	return backfillGatewayTimingColumnsBefore(session, math.MaxInt64)
}

// migrateGatewayTimingColumns makes sure the trigger exists and every row is
// filled, whether or not the online prefill ran first.
func migrateGatewayTimingColumns(db *gorm.DB) error {
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return runOneTimeMigration(db, "telemetry_gateway_timing_v32", func() error {
		session := db.WithContext(WithSlowQueryExpected(context.Background()))
		if err := session.Transaction(installGatewayTimingColumns); err != nil {
			return err
		}
		return backfillGatewayTimingColumnsBefore(session, math.MaxInt64)
	})
}
