package model

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"

	"gorm.io/gorm"
)

// Slow statements are collected in-process and reported to the operator by a
// periodic task (worker.SlowQueryReportTask). A query that creeps toward its
// budget used to be visible only in container logs; the 2026-10-10 health
// outage sat at 4.9s against a five-second timeout for days before it broke.

const (
	slowQueryMaxFingerprints = 50
	slowQueryFingerprintSize = 320
)

// SlowQuery is one statement shape that ran at or above the alert threshold
// since the last report.
type SlowQuery struct {
	Fingerprint string
	Count       int
	MaxDuration time.Duration
	LastError   string
	LastAt      time.Time
}

type slowQueryTracker struct {
	mu      sync.Mutex
	entries map[string]*SlowQuery
	dropped int
}

var slowQueries = &slowQueryTracker{entries: map[string]*SlowQuery{}}

// slowQueryAlertThreshold is SLOW_SQL_ALERT_MS, one second by default.
func slowQueryAlertThreshold() time.Duration {
	ms := common.GetEnvOrDefault("SLOW_SQL_ALERT_MS", 1000)
	if ms <= 0 {
		ms = 1000
	}
	return time.Duration(ms) * time.Millisecond
}

type slowQueryExpectedKey struct{}

type slowQueryThresholdKey struct{}

// WithSlowQueryThreshold sets the alert threshold for work that has its own
// time budget, so it warns well before that budget rather than at the
// process-wide default.
func WithSlowQueryThreshold(ctx context.Context, threshold time.Duration) context.Context {
	return context.WithValue(ctx, slowQueryThresholdKey{}, threshold)
}

// WithSlowQueryExpected marks work whose statements are long by design, such
// as a migration's backfill, so they do not page the operator.
func WithSlowQueryExpected(ctx context.Context) context.Context {
	return context.WithValue(ctx, slowQueryExpectedKey{}, true)
}

var (
	slowQueryLiteral = regexp.MustCompile(`'(?:[^']|'')*'`)
	slowQueryNumber  = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	slowQuerySpace   = regexp.MustCompile(`\s+`)
)

func slowQueryFingerprint(sql string) string {
	sql = slowQueryLiteral.ReplaceAllString(sql, "?")
	sql = slowQueryNumber.ReplaceAllString(sql, "?")
	sql = strings.TrimSpace(slowQuerySpace.ReplaceAllString(sql, " "))
	if len(sql) > slowQueryFingerprintSize {
		sql = sql[:slowQueryFingerprintSize] + "…"
	}
	return sql
}

func (t *slowQueryTracker) record(sql string, elapsed time.Duration, err error, at time.Time) {
	fingerprint := slowQueryFingerprint(sql)
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entries[fingerprint]
	if entry == nil {
		if len(t.entries) >= slowQueryMaxFingerprints {
			t.dropped++
			return
		}
		entry = &SlowQuery{Fingerprint: fingerprint}
		t.entries[fingerprint] = entry
	}
	entry.Count++
	entry.LastAt = at
	if elapsed > entry.MaxDuration {
		entry.MaxDuration = elapsed
	}
	if err != nil {
		entry.LastError = err.Error()
	}
}

// DrainSlowQueries returns and clears the statements recorded since the last
// call, slowest first, with how many further shapes did not fit.
func DrainSlowQueries() ([]SlowQuery, int) {
	slowQueries.mu.Lock()
	entries, dropped := slowQueries.entries, slowQueries.dropped
	slowQueries.entries, slowQueries.dropped = map[string]*SlowQuery{}, 0
	slowQueries.mu.Unlock()
	result := make([]SlowQuery, 0, len(entries))
	for _, entry := range entries {
		result = append(result, *entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].MaxDuration > result[j].MaxDuration })
	return result, dropped
}

const slowQueryStartKey = "lazu:slow_query_start"

// registerSlowQueryCallbacks times every statement with GORM callbacks rather
// than by wrapping the logger, so SQL logs keep naming their real caller. The
// SQL text is the statement with placeholders, which is also its fingerprint.
func registerSlowQueryCallbacks(db *gorm.DB) error {
	return registerSlowQueryCallbacksAt(db, slowQueryAlertThreshold())
}

func registerSlowQueryCallbacksAt(db *gorm.DB, threshold time.Duration) error {
	before := func(tx *gorm.DB) { tx.InstanceSet(slowQueryStartKey, time.Now()) }
	after := func(tx *gorm.DB) {
		value, ok := tx.InstanceGet(slowQueryStartKey)
		if !ok {
			return
		}
		elapsed := time.Since(value.(time.Time))
		limit := threshold
		if ctx := tx.Statement.Context; ctx != nil {
			if ctx.Value(slowQueryExpectedKey{}) != nil {
				return
			}
			if override, ok := ctx.Value(slowQueryThresholdKey{}).(time.Duration); ok && override > 0 {
				limit = override
			}
		}
		if elapsed < limit || errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return
		}
		slowQueries.record(tx.Statement.SQL.String(), elapsed, tx.Error, time.Now().UTC())
	}
	callbacks := db.Callback()
	registrations := []struct {
		name          string
		before, after func(string, func(*gorm.DB)) error
	}{
		{"create", func(n string, f func(*gorm.DB)) error { return callbacks.Create().Before("*").Register(n, f) }, func(n string, f func(*gorm.DB)) error { return callbacks.Create().After("*").Register(n, f) }},
		{"query", func(n string, f func(*gorm.DB)) error { return callbacks.Query().Before("*").Register(n, f) }, func(n string, f func(*gorm.DB)) error { return callbacks.Query().After("*").Register(n, f) }},
		{"update", func(n string, f func(*gorm.DB)) error { return callbacks.Update().Before("*").Register(n, f) }, func(n string, f func(*gorm.DB)) error { return callbacks.Update().After("*").Register(n, f) }},
		{"delete", func(n string, f func(*gorm.DB)) error { return callbacks.Delete().Before("*").Register(n, f) }, func(n string, f func(*gorm.DB)) error { return callbacks.Delete().After("*").Register(n, f) }},
		{"row", func(n string, f func(*gorm.DB)) error { return callbacks.Row().Before("*").Register(n, f) }, func(n string, f func(*gorm.DB)) error { return callbacks.Row().After("*").Register(n, f) }},
		{"raw", func(n string, f func(*gorm.DB)) error { return callbacks.Raw().Before("*").Register(n, f) }, func(n string, f func(*gorm.DB)) error { return callbacks.Raw().After("*").Register(n, f) }},
	}
	for _, registration := range registrations {
		if err := registration.before("lazu:slow_query_start_"+registration.name, before); err != nil {
			return err
		}
		if err := registration.after("lazu:slow_query_end_"+registration.name, after); err != nil {
			return err
		}
	}
	return nil
}
