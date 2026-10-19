package model

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// The deployed query is an independent oracle for the batching optimization.
const originalPostgresGatewayPerformanceQuery = `WITH measured AS MATERIALIZED (
 SELECT to_char(to_timestamp(request_ts / 1000.0) AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
 (COALESCE(NULLIF(other,''),'{}')::jsonb ->> 'gateway_pre_ms')::double precision AS pre,
 (COALESCE(NULLIF(other,''),'{}')::jsonb ->> 'gateway_post_ms')::double precision AS post
 FROM logs WHERE request_ts >= ? AND request_ts < ?)
 SELECT day,count(pre) AS samples,count(post) AS post_samples,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY pre) AS pre_p50,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY pre) AS pre_p95,
 percentile_cont(0.5) WITHIN GROUP(ORDER BY post) AS post_p50,
 percentile_cont(0.95) WITHIN GROUP(ORDER BY post) AS post_p95
 FROM measured GROUP BY day ORDER BY day`

func TestPostgresGatewayTimingColumnsMatchHistoricalJSONAndUTC(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := installGatewayTimingColumns(db); err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if err := db.Exec("SET TIME ZONE 'America/Los_Angeles'").Error; err != nil {
		t.Fatal(err)
	}
	start := time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC) // 1 BC, shares YYYY with 1 AD.
	end := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rows := []Log{
		{Id: 1, RequestTS: start.UnixMilli(), Other: `{"gateway_pre_ms":12}`},
		{Id: 2, RequestTS: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), Other: `{"gateway_pre_ms":28}`},
		{Id: 3, RequestTS: -1, Other: `{"gateway_pre_ms":4.5,"gateway_post_ms":-3}`},
		{Id: 4, RequestTS: day.UnixMilli(), Other: `{"gateway_\u0070re_ms":10,"gateway_post_ms":0}`},
		{Id: 5, RequestTS: day.Add(24*time.Hour - time.Millisecond).UnixMilli(), Other: `{"gateway_pre_ms":30,"gateway_post_ms":20}`},
		{Id: 6, RequestTS: day.Add(24 * time.Hour).UnixMilli(), Other: `{"gateway_pre_ms":null}`},
		{Id: 7, RequestTS: day.Add(24*time.Hour + time.Second).UnixMilli(), Other: `null`},
		{Id: 8, RequestTS: day.Add(24*time.Hour + 2*time.Second).UnixMilli(), Other: `[]`},
		{Id: 9, RequestTS: day.Add(24*time.Hour + 3*time.Second).UnixMilli(), Other: `"gateway_pre_ms"`},
		{Id: 10, RequestTS: day.Add(24*time.Hour + 4*time.Second).UnixMilli(), Other: ""},
		{Id: 11, RequestTS: day.Add(48 * time.Hour).UnixMilli(), Other: ""},
		{Id: 12, RequestTS: end.UnixMilli(), Other: `{"gateway_pre_ms":999}`},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE logs SET other=NULL WHERE id=11").Error; err != nil {
		t.Fatal(err)
	}
	var expected []GatewayTimingDay
	if err := db.Raw(originalPostgresGatewayPerformanceQuery, start.UnixMilli(), end.UnixMilli()).Scan(&expected).Error; err != nil {
		t.Fatal(err)
	}
	actual, err := GatewayPerformanceDays(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("batched UTC/JSON semantics changed: got %+v want %+v", actual, expected)
	}
	if len(actual) != 5 || actual[0].Day != "0001-01-01" || actual[0].Samples != 2 || actual[0].PreP50 == nil || *actual[0].PreP50 != 20 {
		t.Fatalf("historical BC/AD formatted-day grouping changed: %+v", actual)
	}
	// The columns hold JSON numbers only. A string, or a document that is not
	// JSON, is no measurement; the historical query cast the first and failed
	// the whole report on the second.
	before := actual
	if err := db.Create([]Log{
		{Id: 13, RequestTS: day.UnixMilli(), Other: "invalid JSON"},
		{Id: 14, RequestTS: day.Add(time.Second).UnixMilli(), Other: `{"gateway_pre_ms":"7","gateway_post_ms":[1]}`},
	}).Error; err != nil {
		t.Fatal(err)
	}
	actual, err = GatewayPerformanceDays(context.Background(), start, end)
	if err != nil {
		t.Fatalf("malformed metadata must not fail the report: %v", err)
	}
	if !reflect.DeepEqual(actual, before) {
		t.Fatalf("non-numeric timings were counted: got %+v want %+v", actual, before)
	}
}

func TestPostgresGatewayPerformancePreservesUnknownAndExactPercentiles(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := installGatewayTimingColumns(db); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 2)
	rows := []Log{
		{Id: 1, RequestTS: start.UnixMilli(), Other: `{"gateway_pre_ms":10,"gateway_post_ms":0}`},
		{Id: 2, RequestTS: start.Add(time.Second).UnixMilli(), Other: `{"gateway_pre_ms":30}`},
		{Id: 3, RequestTS: start.Add(2 * time.Second).UnixMilli(), Other: `{}`},
		{Id: 4, RequestTS: start.Add(3 * time.Second).UnixMilli(), Other: ""},
		{Id: 5, RequestTS: start.Add(4 * time.Second).UnixMilli(), Other: `{"gateway_post_ms":20}`},
		{Id: 6, RequestTS: start.AddDate(0, 0, 1).UnixMilli(), Other: `{}`},
		{Id: 7, RequestTS: end.UnixMilli(), Other: `{"gateway_pre_ms":999,"gateway_post_ms":999}`},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	days, err := GatewayPerformanceDays(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 {
		t.Fatalf("expected two observed days inside exclusive end, got %d", len(days))
	}
	first := days[0]
	if first.Day != "2026-10-01" || first.Samples != 2 || first.PostSamples != 2 ||
		first.PreP50 == nil || *first.PreP50 != 20 || first.PreP95 == nil || *first.PreP95 != 29 ||
		first.PostP50 == nil || *first.PostP50 != 10 || first.PostP95 == nil || *first.PostP95 != 19 {
		t.Fatalf("observed samples or continuous percentiles changed: %+v", first)
	}
	unknown := days[1]
	if unknown.Day != "2026-10-02" || unknown.Samples != 0 || unknown.PostSamples != 0 ||
		unknown.PreP50 != nil || unknown.PreP95 != nil || unknown.PostP50 != nil || unknown.PostP95 != nil {
		t.Fatalf("unmeasured history must remain an observed day with null timings: %+v", unknown)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GatewayPerformanceDays(ctx, start, end); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation from the PostgreSQL query, got %v", err)
	}
}
