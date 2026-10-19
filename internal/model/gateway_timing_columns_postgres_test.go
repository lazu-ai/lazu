package model

import (
	"context"
	"testing"
)

func gatewayTimingColumns(t *testing.T, id int) (*float64, *float64) {
	t.Helper()
	var row struct {
		GatewayPreMs  *float64
		GatewayPostMs *float64
	}
	if err := TelemetryDB().Raw("SELECT gateway_pre_ms, gateway_post_ms FROM logs WHERE id = ?", id).Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.GatewayPreMs, row.GatewayPostMs
}

func expectGatewayTiming(t *testing.T, id int, pre, post *float64) {
	t.Helper()
	gotPre, gotPost := gatewayTimingColumns(t, id)
	same := func(a, b *float64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
	if !same(gotPre, pre) || !same(gotPost, post) {
		format := func(v *float64) interface{} {
			if v == nil {
				return nil
			}
			return *v
		}
		t.Fatalf("log %d timing = (%v, %v), want (%v, %v)", id, format(gotPre), format(gotPost), format(pre), format(post))
	}
}

func ms(value float64) *float64 { return &value }

func TestPostgresGatewayTimingColumnsFollowEveryWriter(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	// Rows written before the upgrade are filled by the backfill.
	if err := db.Exec(`INSERT INTO logs (id, request_ts, request_id, type, other) VALUES
		(1, 1000, 'req-before', 2, '{"gateway_pre_ms":4,"gateway_post_ms":1.5}'),
		(2, 2000, 'req-plain', 2, '{"user_agent":"x"}'),
		(3, 3000, 'req-broken', 2, '{"gateway_pre_ms":')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := PrefillGatewayTimingColumns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := PrefillGatewayTimingColumns(context.Background(), db); err != nil {
		t.Fatalf("a second prefill must be harmless: %v", err)
	}
	expectGatewayTiming(t, 1, ms(4), ms(1.5))
	expectGatewayTiming(t, 2, nil, nil)
	expectGatewayTiming(t, 3, nil, nil)

	// The request's own facts, written with the row.
	if err := db.Create(&Log{Id: 4, RequestTS: 4000, RequestID: "req-live", Type: LogTypeConsume, Other: `{"gateway_pre_ms":3}`}).Error; err != nil {
		t.Fatal(err)
	}
	expectGatewayTiming(t, 4, ms(3), nil)

	// A late diagnostic merged in SQL with jsonb ||; the earlier timing stays.
	found, err := UpdateRequestLogDiagnosticsBatch([]RequestDiagnosticsEvent{{RequestID: "req-live", Fields: map[string]interface{}{"gateway_post_ms": 9.25}}})
	if err != nil || !found["req-live"] {
		t.Fatalf("diagnostic patch found=%v err=%v", found, err)
	}
	expectGatewayTiming(t, 4, ms(3), ms(9.25))
	if err := UpdateRequestLogDiagnostics("req-live", map[string]interface{}{"gateway_pre_ms": 5}); err != nil {
		t.Fatal(err)
	}
	expectGatewayTiming(t, 4, ms(5), ms(9.25))

	// A provisional carrier replaced by the final row on conflict keeps the
	// carrier's timing unless the final row brings its own.
	if err := db.Create(&Log{Id: 5, RequestTS: 5000, PublicID: "pub-5", RequestID: "req-carrier", Type: LogTypeUnknown,
		Other: `{"_relay_provisional_target":true,"gateway_pre_ms":2}`}).Error; err != nil {
		t.Fatal(err)
	}
	conflict, err := relayLogConflictClause(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Clauses(conflict).Create(&Log{Id: 5, RequestTS: 5000, PublicID: "pub-5", RequestID: "req-carrier", Type: LogTypeConsume,
		Other: `{"gateway_post_ms":6}`}).Error; err != nil {
		t.Fatal(err)
	}
	expectGatewayTiming(t, 5, ms(2), ms(6))

	// Rewriting Other recomputes; a value that is not a number is no timing.
	if err := db.Model(&Log{}).Where("id = ?", 4).Update("other", `{"gateway_pre_ms":"5"}`).Error; err != nil {
		t.Fatal(err)
	}
	expectGatewayTiming(t, 4, nil, nil)
}

func TestPostgresGatewayTimingMigrationRunsOnce(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := db.Exec(`INSERT INTO logs (id, request_ts, request_id, type, other) VALUES (1, 1000, 'req-old', 2, '{"gateway_post_ms":8}')`).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrateGatewayTimingColumns(db); err != nil {
			t.Fatal(err)
		}
	}
	expectGatewayTiming(t, 1, nil, ms(8))
	var marks int64
	if err := db.Model(&SchemaVersion{}).Where("scope = ?", "telemetry_gateway_timing_v32").Count(&marks).Error; err != nil {
		t.Fatal(err)
	}
	if marks != 1 {
		t.Fatalf("migration marks = %d, want 1", marks)
	}
}
