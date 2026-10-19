package model

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func originalChannelHealthLastOutcomesSQL(db *gorm.DB) string {
	return `SELECT COALESCE(NULLIF(d.resolved_model,''),NULLIF(d.requested_model,''),'unknown') AS model_name,
 ` + channelHealthLaneExpr() + ` AS lane,COALESCE(o.channel_id,0) AS channel_id,MAX(o.outcome_at) AS last_outcome_at
 FROM ` + channelHealthOutcomeSourceSQL(db) + ` LEFT JOIN routing_decision d ON d.id=o.decision_id
 WHERE o.outcome_at >= ? AND ` + channelHealthPopulationExpr + ` GROUP BY model_name,lane,channel_id`
}

func TestPostgresChannelHealthLastOutcomesCompatibility(t *testing.T) {
	db := routingQueryPostgresFixture(t)
	if err := cacheValidatedSchemaVersion(db, schemaVersionLog); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 10, 8, 12, 0, 0, 123456000, time.UTC)
	baseOutcome := func() map[string]interface{} {
		return map[string]interface{}{"request_id": "json-rid", "outcome_at": since.Add(time.Minute), "decision_id": 31, "channel_id": 7, "final_group": "direct", "status_code": 200}
	}
	baseDecision := func() map[string]interface{} {
		return map[string]interface{}{"id": 31, "request_id": "decision-json-rid", "decision_at": since.AddDate(-1, 0, 0), "requested_model": "requested", "resolved_model": "resolved"}
	}
	type fixture struct {
		name            string
		outcome         map[string]interface{}
		decision        interface{}
		legacy, attempt bool
		shadow          interface{}
		shadowDecision  interface{}
		duplicate       bool
		bad             bool
	}
	tests := []fixture{{name: "empty"}, {name: "json", outcome: baseOutcome(), decision: baseDecision()}, {name: "legacy", outcome: baseOutcome(), decision: baseDecision(), legacy: true}, {name: "superseded-attempt", outcome: baseOutcome(), decision: baseDecision(), attempt: true}, {name: "missing-decision", outcome: baseOutcome()}}
	add := func(name, field string, value interface{}) {
		o := baseOutcome()
		o[field] = value
		tests = append(tests, fixture{name: name, outcome: o, decision: baseDecision()})
	}
	add("explicit-offset-timezone", "outcome_at", since.Add(time.Minute).In(time.FixedZone("eight", 8*3600)))
	add("naive-timestamp-in-nonUTC-session", "outcome_at", "2026-10-08 21:00:00.123456")
	add("cutoff-inclusive", "outcome_at", since)
	add("cutoff-before-microsecond", "outcome_at", since.Add(-time.Microsecond))
	add("future-outcome", "outcome_at", since.AddDate(1, 0, 0))
	add("nil-channel", "channel_id", nil)
	add("nil-lane", "final_group", nil)
	add("empty-lane", "final_group", "")
	add("whitespace-lane", "final_group", " ")
	for _, status := range []interface{}{nil, 200, 400, 429, 500} {
		o := baseOutcome()
		o["status_code"] = status
		tests = append(tests, fixture{name: strings.ReplaceAll("status-"+toJSON(status), "\"", ""), outcome: o, decision: baseDecision()})
	}
	add("explicit-ineligible", "health_eligible", false)
	add("explicit-eligible", "health_eligible", true)
	add("null-eligible", "health_eligible", nil)
	add("provider-false-still-health", "provider_attempted", false)
	for _, value := range []interface{}{nil, "", " "} {
		d := baseDecision()
		d["resolved_model"] = value
		tests = append(tests, fixture{name: "model-" + toJSON(value), outcome: baseOutcome(), decision: d})
	}
	tests = append(tests, fixture{name: "duplicate-decision-identity", outcome: baseOutcome(), decision: baseDecision(), duplicate: true})
	for _, shadow := range []interface{}{map[string]interface{}{"request_id": "different-json-rid", "outcome_at": since.Add(-time.Hour)}, map[string]interface{}{}, nil, []interface{}{}, "scalar", 42} {
		// A nonempty JSON literal shadows by the containing log's physical identity.
		tests = append(tests, fixture{name: "global-legacy-shadow-" + toJSON(shadow), outcome: baseOutcome(), decision: baseDecision(), legacy: true, shadow: map[string]interface{}{"raw": toJSON(shadow)}})
	}
	tests = append(tests, fixture{name: "legacy-decision-globally-shadowed", outcome: baseOutcome(), decision: baseDecision(), legacy: true, shadowDecision: map[string]interface{}{"id": 31, "request_id": "other", "resolved_model": "outside", "decision_at": since.AddDate(-2, 0, 0)}})
	add("original-union-malformed-cast", "actual_input_tokens", "not-an-int")
	tests[len(tests)-1].bad = true
	add("required-malformed-cast", "channel_id", "not-an-int")
	tests[len(tests)-1].bad = true
	for _, excluded := range []string{"health_eligible", "status_code", "final_group"} {
		o := baseOutcome()
		o["actual_input_tokens"] = "not-an-int"
		switch excluded {
		case "health_eligible":
			o[excluded] = false
		case "status_code":
			o[excluded] = 400
		case "final_group":
			o[excluded] = ""
		}
		tests = append(tests, fixture{name: "excluded-bad-union-cast-" + excluded, outcome: o, decision: baseDecision(), bad: excluded == "final_group"})
	}
	o := baseOutcome()
	o["outcome_at"] = since.Add(-time.Hour)
	o["actual_input_tokens"] = "not-an-int"
	tests = append(tests, fixture{name: "outside-window-bad-cast", outcome: o, decision: baseDecision()})
	tests = append(tests, fixture{name: "malformed-json", shadow: map[string]interface{}{"raw": "{"}, bad: true})
	if err := db.Exec("SET TIME ZONE 'Asia/Shanghai'").Error; err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := db.Exec("TRUNCATE logs,routing_decision_legacy,routing_outcome_legacy,routing_attempt_outcome").Error; err != nil {
				t.Fatal(err)
			}
			if tt.outcome != nil {
				if tt.legacy || tt.attempt {
					table := "routing_outcome_legacy"
					if tt.attempt {
						table = "routing_attempt_outcome"
					}
					columns, values := "request_id,outcome_at,decision_id,channel_id,final_group,status_code", "'json-rid',?,31,7,'direct',200"
					if tt.attempt {
						columns += ",request_ts,attempt_index"
						values += ",0,1"
					}
					if err := db.Exec("INSERT INTO "+table+" ("+columns+") VALUES ("+values+")", tt.outcome["outcome_at"]).Error; err != nil {
						t.Fatal(err)
					}
				} else {
					routingQueryRawSnapshot(t, db, "carrier-skewed", since.AddDate(-3, 0, 0).UnixMilli(), nil, tt.outcome)
				}
			}
			if tt.decision != nil {
				if tt.legacy {
					if err := db.Exec("INSERT INTO routing_decision_legacy (id,request_id,decision_at,is_primary,strategy_name,requested_model,resolved_model) VALUES (31,'decision-json-rid',?,true,'baseline','requested','resolved')", since.AddDate(-1, 0, 0)).Error; err != nil {
						t.Fatal(err)
					}
				} else {
					routingQueryRawSnapshot(t, db, "decision-carrier", 0, tt.decision, nil)
				}
			}
			if tt.duplicate {
				d := baseDecision()
				d["resolved_model"] = "second-model"
				routingQueryRawSnapshot(t, db, "duplicate-decision-carrier", 0, d, nil)
			}
			if tt.shadow != nil {
				raw := tt.shadow.(map[string]interface{})["raw"]
				if err := db.Exec("INSERT INTO logs (request_id,public_id,request_ts,created_at,routing_outcome_json) VALUES ('json-rid','shadow',0,0,?)", raw).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tt.shadowDecision != nil {
				routingQueryRawSnapshot(t, db, "decision-json-rid", 0, tt.shadowDecision, nil)
			}
			// A document a typed column cannot hold is rejected when it is
			// projected, whatever window it falls in, not by a later reader.
			err := backfillRoutingFactColumns(db)
			if err == nil {
				err = markSupersededRoutingHistory(db)
			}
			if tt.bad || strings.Contains(toJSON(tt.outcome), "not-an-int") {
				if err == nil || !strings.Contains(err.Error(), "22P02") {
					t.Fatalf("malformed document was projected: %v", err)
				}
				return
			} else if err != nil {
				t.Fatal(err)
			}
			// The rollup reads whole hours up to now: a sample a microsecond
			// before the cutoff shares its hour, and a future one is not yet
			// evidence. Every other fixture must agree exactly.
			if tt.name == "cutoff-before-microsecond" || tt.name == "future-outcome" {
				return
			}
			var old []ChannelHealthLastOutcomeRow
			oldErr := db.Raw(originalChannelHealthLastOutcomesSQL(db), since).Scan(&old).Error
			for i := range old {
				old[i].Lane = NormalizeChannelLane(old[i].Lane)
			}
			got, err := QueryChannelHealthLastOutcomesContext(ChannelHealthAnalysisContext(context.Background()), since)
			if err != nil || oldErr != nil {
				t.Fatalf("old=%v new=%v", oldErr, err)
			}
			less := func(rows []ChannelHealthLastOutcomeRow) {
				sort.Slice(rows, func(i, j int) bool {
					return ChannelHealthCellKey(rows[i].ModelName, rows[i].Lane, rows[i].ChannelId) < ChannelHealthCellKey(rows[j].ModelName, rows[j].Lane, rows[j].ChannelId)
				})
			}
			less(old)
			less(got)
			// The hourly rollup keeps a route's newest evidence to the
			// millisecond; PostgreSQL stores microseconds.
			for i := range old {
				if old[i].LastOutcomeAt.Valid {
					old[i].LastOutcomeAt.Time = old[i].LastOutcomeAt.Time.Truncate(time.Millisecond)
				}
			}
			if len(old) != len(got) || !reflect.DeepEqual(old, got) && len(old) > 0 {
				t.Fatalf("different old=%+v new=%+v", old, got)
			}
		})
	}
}

func toJSON(value interface{}) string { body, _ := json.Marshal(value); return string(body) }

func TestPostgresChannelHealthAnalysisBudget(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	var policy struct {
		ReadOnly string
		Parallel string
		JIT      string
		Timeout  string
	}
	query := `SELECT current_setting('transaction_read_only') AS read_only,current_setting('max_parallel_workers_per_gather') AS parallel,current_setting('jit') AS jit,current_setting('statement_timeout') AS timeout`
	if err := channelHealthScan(db.WithContext(ChannelHealthAnalysisContext(context.Background())), query, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.ReadOnly != "on" || policy.Parallel != "0" || policy.JIT != "off" || policy.Timeout != "5s" {
		t.Fatalf("unbounded policy %+v", policy)
	}
	var outside struct{ ReadOnly string }
	if err := db.Raw("SELECT current_setting('transaction_read_only') AS read_only").Scan(&outside).Error; err != nil || outside.ReadOnly != "off" {
		t.Fatalf("policy leaked: %+v %v", outside, err)
	}
	ctx, cancel := context.WithTimeout(ChannelHealthAnalysisContext(context.Background()), 25*time.Millisecond)
	defer cancel()
	var value int
	start := time.Now()
	err := channelHealthScan(db.WithContext(ctx), "SELECT 1 FROM pg_sleep(10)", &value)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("deadline not propagated: %v %v", err, time.Since(start))
	}
}

// Test-only dialect dispatch retains PostgreSQL SQL/binding while selecting the
// previous canonical-view source. This compares both populations in one DB.
type canonicalHealthPostgresDialect struct{ gorm.Dialector }

func (canonicalHealthPostgresDialect) Name() string { return "canonical-health-baseline" }

func TestPostgresChannelHealthSnapshotQueryParity(t *testing.T) {
	db := routingQueryPostgresFixture(t)
	if err := cacheValidatedSchemaVersion(db, schemaVersionLog); err != nil {
		t.Fatal(err)
	}
	priorPostgres, priorSQLite := common.UsingPostgreSQL, common.UsingSQLite
	common.UsingPostgreSQL, common.UsingSQLite = true, false
	t.Cleanup(func() { common.UsingPostgreSQL, common.UsingSQLite = priorPostgres, priorSQLite })
	since := time.Date(2026, 10, 1, 17, 13, 54, 0, time.UTC)
	for _, sql := range []string{
		`INSERT INTO telemetry_channels (id,name,status) VALUES (7,'fixture',1)`,
		`INSERT INTO telemetry_abilities ("group",model,channel_id,enabled,role) VALUES ('direct','resolved',7,true,'primary')`,
		`INSERT INTO routing_decision_legacy (id,request_id,decision_at,is_primary,strategy_name,requested_model,resolved_model) VALUES (31,'legacy-decision','2025-01-01',true,'baseline','requested','resolved')`,
		`INSERT INTO routing_outcome_legacy (request_id,outcome_at,decision_id,channel_id,final_group,status_code,total_duration_ms,ttft_ms,answer_ttft_ms,actual_output_tokens,actual_input_tokens,was_streaming) VALUES ('legacy-ok','2026-10-08',31,7,'direct',200,2000,400,450,100,250,true),('legacy-error','2026-10-07',31,7,'direct',500,1000,NULL,NULL,NULL,NULL,false),('legacy-shadow','2026-10-06',31,7,'direct',200,1200,250,250,100,200,true)`,
		`INSERT INTO routing_attempt_outcome (request_ts,request_id,attempt_index,outcome_at,decision_id,channel_id,final_group,status_code,total_duration_ms) VALUES (0,'retry',0,'2026-10-08',31,7,'direct',500,1234)`,
		`INSERT INTO channel_probe_result (request_ts,id,model_name,lane,channel_id,probed_at,success,latency_ms,ttft_ms,answer_ttft_ms,output_tokens,skipped) VALUES (0,1,'resolved','direct',7,'2026-10-08',true,1500,200,200,100,false)`,
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	routingQuerySnapshot(t, db, "legacy-shadow", 0, nil, map[string]interface{}{"request_id": "different-json-id", "outcome_at": since.Add(-time.Hour), "decision_id": 31, "channel_id": 7, "final_group": "direct", "status_code": 200})
	routingQuerySnapshot(t, db, "json-current", since.AddDate(-3, 0, 0).UnixMilli(), map[string]interface{}{"id": 45, "request_id": "json-decision", "decision_at": since.AddDate(-2, 0, 0), "resolved_model": "resolved"}, map[string]interface{}{"request_id": "json-outcome", "outcome_at": since.Add(time.Hour), "decision_id": 45, "channel_id": 7, "final_group": "direct", "status_code": 200, "actual_output_tokens": 50, "actual_input_tokens": 125, "total_duration_ms": 1300, "ttft_ms": 150, "answer_ttft_ms": 150, "was_streaming": true})
	oldStore := db.Session(&gorm.Session{NewDB: true})
	oldStore.Dialector = canonicalHealthPostgresDialect{db.Dialector}
	checks := []struct {
		name string
		run  func() (interface{}, error)
	}{
		{"evidence", func() (interface{}, error) { return QueryChannelHealthEvidence(since) }},
		{"buckets", func() (interface{}, error) { return QueryChannelHealthBuckets(since, 7200, 84) }},
		{"coarse-buckets", func() (interface{}, error) { return QueryChannelHealthBuckets(since, 14400, 42) }},
		{"channel-percentiles", func() (interface{}, error) { return QueryChannelHealthChannelPercentiles(since) }},
		{"lane-percentiles", func() (interface{}, error) {
			return QueryChannelHealthPercentilesWithAliases(since, map[string]string{"resolved": "public-model"})
		}},
		{"public-percentiles", func() (interface{}, error) {
			return QueryChannelHealthPublicPercentilesWithAliases(since, map[string]string{"resolved": "public-model"}, []string{"public-model"})
		}},
		{"excluded-route", func() (interface{}, error) {
			return QueryChannelHealthPercentilesWithAliases(since, nil, ChannelHealthRouteRef{ModelName: "resolved", Lane: "direct", ChannelId: 7})
		}},
		{"last-probes", func() (interface{}, error) { return QueryChannelHealthLastProbes(since) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			telemetryDB = oldStore
			old, err := check.run()
			telemetryDB = db
			if err != nil {
				t.Fatal(err)
			}
			got, err := check.run()
			if err != nil {
				t.Fatal(err)
			}
			canonical := func(value interface{}) string {
				body, _ := json.Marshal(value)
				var rows []json.RawMessage
				if json.Unmarshal(body, &rows) == nil {
					sort.Slice(rows, func(i, j int) bool { return string(rows[i]) < string(rows[j]) })
					body, _ = json.Marshal(rows)
				}
				return string(body)
			}
			if canonical(old) != canonical(got) {
				t.Fatalf("different output old=%s new=%s", canonical(old), canonical(got))
			}
		})
	}
	if path := os.Getenv("CHANNEL_HEALTH_EVIDENCE_EXPORT"); path != "" {
		sentinel := errors.New("export query without execution")
		var capture map[string]interface{}
		const callback = "health-budget:export-evidence"
		if err := db.Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
			if strings.Contains(tx.Statement.SQL.String(), "WITH events AS") {
				capture = map[string]interface{}{"sql": tx.Statement.SQL.String(), "args": tx.Statement.Vars, "since": since, "scope": "exact Evidence query captured immediately before native SQL; no customer data"}
				tx.AddError(sentinel)
			}
		}); err != nil {
			t.Fatal(err)
		}
		_, err := QueryChannelHealthEvidence(since)
		db.Callback().Row().Remove(callback)
		if !errors.Is(err, sentinel) || capture == nil {
			t.Fatalf("capture failed: %v", err)
		}
		body, _ := json.MarshalIndent(capture, "", "  ")
		if err := os.WriteFile(path, append(body, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
