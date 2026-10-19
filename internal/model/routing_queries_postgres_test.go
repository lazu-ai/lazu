package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRoutingQualityQuerySQL(t *testing.T) {
	store, err := gorm.Open(postgres.New(postgres.Config{DSN: "postgres://fixture:fixture@127.0.0.1:1/fixture?sslmode=disable", PreferSimpleProtocol: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC()
	start := end.Add(-5 * time.Minute)
	predicate := "user_id = ? AND prompt_hash = ? AND decision_at > ? AND decision_at <= ?"
	args := []interface{}{1, strings.Repeat("f", 64), start, end}
	query, err := routingFactQuery(store, &RoutingDecision{}, predicate, args...)
	if err != nil {
		t.Fatal(err)
	}
	statement := store.Raw("SELECT EXISTS (?) AS present", query.Select("1").Limit(1)).Statement
	if !strings.Contains(statement.SQL.String(), "OFFSET 0") || len(statement.Vars) != 9 || statement.Vars[8] != 1 || strings.Contains(statement.SQL.String(), "request_ts") {
		t.Fatalf("unexpected scoped query: %s vars=%d", statement.SQL.String(), len(statement.Vars))
	}
	old := store.Raw("SELECT count(*) FROM routing_decision WHERE "+predicate, args...).Statement
	result := map[string]interface{}{"scope": "dry-run only; no SQL connection or credentials", "old": map[string]interface{}{"sql": old.SQL.String(), "vars": old.Vars}, "new": map[string]interface{}{"sql": statement.SQL.String(), "vars": statement.Vars}}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("BENCHMARK_JSON %s", body)
}

func routingQueryPostgresFixture(t *testing.T) *gorm.DB {
	t.Helper()
	_, db := postgresTelemetryFixture(t)
	for _, model := range []interface{}{&RoutingDecision{}, &RoutingOutcome{}} {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("ALTER TABLE " + statement.Schema.Table + " RENAME TO " + statement.Schema.Table + "_legacy").Error; err != nil {
			t.Fatal(err)
		}
		if err := createRoutingCompatibilityViewTx(db, statement.Schema, model); err != nil {
			t.Fatal(err)
		}
	}
	// The shipped typed-column indexes, so plan assertions see production's.
	for _, index := range routingFactIndexes {
		if err := db.Exec("CREATE INDEX " + index.name + " ON logs (" + index.column + ")").Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func routingQueryRawSnapshot(t *testing.T, db *gorm.DB, carrier string, requestTS int64, decision interface{}, outcome interface{}) {
	t.Helper()
	encode := func(value interface{}) string {
		if value == nil {
			return ""
		}
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	// Bypass request-time hooks so zero/skewed history is exercised exactly.
	if err := db.Exec(`INSERT INTO logs (request_id,public_id,request_ts,created_at,routing_decision_json,routing_outcome_json)
		VALUES (?,?,?,0,?,?)`, carrier, "fixture_"+carrier, requestTS, encode(decision), encode(outcome)).Error; err != nil {
		t.Fatal(err)
	}
}

// routingQuerySnapshot stores a raw JSON copy and projects it into the typed
// columns exactly as the schema-30 backfill does.
func routingQuerySnapshot(t *testing.T, db *gorm.DB, carrier string, requestTS int64, decision interface{}, outcome interface{}) {
	t.Helper()
	routingQueryRawSnapshot(t, db, carrier, requestTS, decision, outcome)
	syncRoutingFactColumns(t, db)
}

func syncRoutingFactColumns(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := backfillRoutingFactColumns(db); err != nil {
		t.Fatal(err)
	}
	if err := markSupersededRoutingHistory(db); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRoutingQualityQueriesPreserveCompatibility(t *testing.T) {
	db := routingQueryPostgresFixture(t)
	start := time.Date(2026, 10, 8, 12, 0, 0, 123456000, time.UTC)
	end := start.Add(5 * time.Minute)
	uid := 77
	base := RoutingDecision{ID: 31, RequestID: "json-request", DecisionAt: start.Add(time.Minute), UserID: &uid, SessionID: "session", PromptHash: "prompt", ResolvedModel: "other", StrategyName: "baseline"}
	tests := []struct {
		name string
		seed func()
	}{
		{"empty", func() {}},
		{"json-unknown-request-time", func() { routingQuerySnapshot(t, db, "carrier", 0, base, nil) }},
		{"json-skewed-request-time", func() { routingQuerySnapshot(t, db, "carrier", start.AddDate(-3, 0, 0).UnixMilli(), base, nil) }},
		{"strict-start", func() { row := base; row.DecisionAt = start; routingQuerySnapshot(t, db, "carrier", 0, row, nil) }},
		{"inclusive-end-with-offset", func() {
			row := base
			row.DecisionAt = end.In(time.FixedZone("offset", 8*3600))
			routingQuerySnapshot(t, db, "carrier", 0, row, nil)
		}},
		{"past-end-microsecond", func() {
			row := base
			row.DecisionAt = end.Add(time.Microsecond)
			routingQuerySnapshot(t, db, "carrier", 0, row, nil)
		}},
		{"nil-user-and-null-model", func() {
			row := map[string]interface{}{"id": 31, "request_id": base.RequestID, "decision_at": base.DecisionAt, "user_id": nil, "session_id": "session", "prompt_hash": "prompt", "resolved_model": nil}
			routingQuerySnapshot(t, db, "carrier", 0, row, nil)
		}},
		{"empty-resolved-model", func() { row := base; row.ResolvedModel = ""; routingQuerySnapshot(t, db, "carrier", 0, row, nil) }},
		{"nonprimary-and-duplicate-json", func() {
			routingQuerySnapshot(t, db, "carrier-a", 0, base, nil)
			routingQuerySnapshot(t, db, "carrier-b", 0, base, nil)
		}},
		{"legacy", func() {
			if err := db.Table("routing_decision_legacy").Create(&base).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"legacy-globally-shadowed-outside-window", func() {
			if err := db.Table("routing_decision_legacy").Create(&base).Error; err != nil {
				t.Fatal(err)
			}
			row := base
			row.RequestID = "different-json-identity"
			row.DecisionAt = start.Add(-time.Hour)
			row.UserID = nil
			row.SessionID = "other"
			routingQuerySnapshot(t, db, base.RequestID, start.AddDate(-3, 0, 0).UnixMilli(), row, nil)
		}},
		{"legacy-shadowed-by-json-null-fields", func() {
			if err := db.Table("routing_decision_legacy").Create(&base).Error; err != nil {
				t.Fatal(err)
			}
			routingQuerySnapshot(t, db, base.RequestID, 0, map[string]interface{}{}, nil)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := db.Exec("TRUNCATE logs,routing_decision_legacy,routing_outcome_legacy").Error; err != nil {
				t.Fatal(err)
			}
			test.seed()
			for _, user := range []*int{nil, new(int), &uid} {
				for _, prompt := range []string{"", "prompt", "absent"} {
					var count int64
					if user != nil && *user != 0 && prompt != "" {
						if err := db.Model(&RoutingDecision{}).Where("user_id = ? AND prompt_hash = ? AND decision_at > ? AND decision_at <= ?", *user, prompt, start, end).Count(&count).Error; err != nil {
							t.Fatal(err)
						}
					}
					got, err := HasUserRetriedContext(context.Background(), user, prompt, start, end)
					if err != nil || got != (count > 0) {
						t.Fatalf("retry user=%v prompt=%q: got=%v err=%v baseline=%d", user, prompt, got, err, count)
					}
				}
			}
			for _, session := range []string{"", "session", "absent"} {
				for _, model := range []string{"", "original", "other"} {
					var count int64
					if session != "" && model != "" {
						if err := db.Model(&RoutingDecision{}).Where("session_id = ? AND resolved_model <> ? AND decision_at > ? AND decision_at <= ?", session, model, start, end).Count(&count).Error; err != nil {
							t.Fatal(err)
						}
					}
					got, err := HasUserSwitchedModelContext(context.Background(), session, model, start, end)
					if err != nil || got != (count > 0) {
						t.Fatalf("switch session=%q model=%q: got=%v err=%v baseline=%d", session, model, got, err, count)
					}
				}
			}
		})
	}
}

func TestPostgresRoutingQualityCandidateAndDecisionParity(t *testing.T) {
	db := routingQueryPostgresFixture(t)
	start := time.Date(2026, 10, 8, 12, 0, 0, 123456000, time.UTC)
	end := start.Add(time.Hour)
	uid := 77
	truth := true
	for i, at := range []time.Time{start.Add(-time.Microsecond), start, start.Add(time.Minute), end, end.Add(time.Microsecond)} {
		id := fmt.Sprintf("json-%d", i)
		decision := RoutingDecision{ID: int64(i + 10), RequestID: id, DecisionAt: at, IsPrimary: true, UserID: &uid, SessionID: "session", PromptHash: "prompt", ResolvedModel: "model", StrategyName: "baseline"}
		outcome := RoutingOutcome{RequestID: id, OutcomeAt: at, DecisionID: decision.ID, UserID: &uid}
		if i == 2 {
			outcome.UserRetriedWithin5Min = &truth
			outcome.UserSwitchedModel = &truth
		}
		routingQuerySnapshot(t, db, "carrier-"+id, 0, decision, outcome)
		if i == 1 {
			// The view uses UNION ALL, including duplicate JSON identities.
			// Candidate iteration must retain both carriers rather than dedupe.
			routingQuerySnapshot(t, db, "duplicate-"+id, 0, nil, outcome)
		}
		legacy := RoutingOutcome{RequestID: fmt.Sprintf("legacy-%d", i), OutcomeAt: at, DecisionID: int64(i + 40), UserID: &uid}
		if err := db.Table("routing_outcome_legacy").Create(&legacy).Error; err != nil {
			t.Fatal(err)
		}
	}
	shadow := RoutingOutcome{RequestID: "shadow", OutcomeAt: start.Add(time.Minute), DecisionID: 81}
	if err := db.Table("routing_outcome_legacy").Create(&shadow).Error; err != nil {
		t.Fatal(err)
	}
	routingQuerySnapshot(t, db, "shadow", 0, nil, map[string]interface{}{"request_id": "different", "outcome_at": start.Add(-time.Hour)})
	var baseline []RoutingOutcome
	if err := db.Where("outcome_at BETWEEN ? AND ?", start, end).Where("user_retried_within_5min IS NULL OR user_switched_model IS NULL").Order("request_id").Find(&baseline).Error; err != nil {
		t.Fatal(err)
	}
	got, err := QueryOutcomesNeedingBackfillContext(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	normalize := func(rows []RoutingOutcome) []string {
		m := make([]string, 0, len(rows))
		for _, row := range rows {
			body, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			m = append(m, string(body))
		}
		sort.Strings(m)
		return m
	}
	if !reflect.DeepEqual(normalize(got), normalize(baseline)) {
		t.Fatalf("candidate rows differ: got=%+v baseline=%+v", got, baseline)
	}
	for _, id := range []string{"json-0", "json-1", "legacy-0", "legacy-4", "shadow", "different", "absent"} {
		var old RoutingOutcome
		oldErr := db.Where("request_id = ?", id).First(&old).Error
		actual, actualErr := GetOutcomeByRequestIDContext(context.Background(), id)
		if errors.Is(oldErr, gorm.ErrRecordNotFound) {
			if !errors.Is(actualErr, gorm.ErrRecordNotFound) {
				t.Fatalf("missing outcome %q error=%v", id, actualErr)
			}
		} else if oldErr != nil || actualErr != nil || !reflect.DeepEqual(*actual, old) {
			t.Fatalf("outcome %q parity: baseline=%+v got=%+v olderr=%v err=%v", id, old, actual, oldErr, actualErr)
		}
	}
	for _, id := range []string{"json-0", "json-1", "json-2", "absent"} {
		var old RoutingDecision
		oldErr := db.Where("request_id = ? AND is_primary = ?", id, true).First(&old).Error
		actual, actualErr := GetDecisionByRequestIDContext(context.Background(), id)
		if errors.Is(oldErr, gorm.ErrRecordNotFound) {
			if !errors.Is(actualErr, gorm.ErrRecordNotFound) {
				t.Fatalf("missing decision error=%v", actualErr)
			}
		} else if oldErr != nil || actualErr != nil || !reflect.DeepEqual(*actual, old) {
			t.Fatalf("decision parity: baseline=%+v got=%+v olderr=%v err=%v", old, actual, oldErr, actualErr)
		}
	}
}

func TestPostgresRoutingQualityQueriesCancelBlockedSQL(t *testing.T) {
	db := routingQueryPostgresFixture(t)
	lock := db.Begin()
	if lock.Error != nil {
		t.Fatal(lock.Error)
	}
	defer lock.Rollback()
	if err := lock.Exec("LOCK logs IN ACCESS EXCLUSIVE MODE").Error; err != nil {
		t.Fatal(err)
	}
	uid := 77
	for name, call := range map[string]func(context.Context) error{
		"candidates": func(ctx context.Context) error {
			_, err := QueryOutcomesNeedingBackfillContext(ctx, time.Now().Add(-time.Hour), time.Now())
			return err
		},
		"decision": func(ctx context.Context) error { _, err := GetDecisionByRequestIDContext(ctx, "fixture"); return err },
		"outcome":  func(ctx context.Context) error { _, err := GetOutcomeByRequestIDContext(ctx, "fixture"); return err },
		"receipt": func(ctx context.Context) error {
			_, err := GetConsumeLogByRequestIDForTokenContext(ctx, 1, 1, "fixture")
			return err
		},
		"retry": func(ctx context.Context) error {
			_, err := HasUserRetriedContext(ctx, &uid, "prompt", time.Now(), time.Now().Add(time.Minute))
			return err
		},
		"switch": func(ctx context.Context) error {
			_, err := HasUserSwitchedModelContext(ctx, "session", "model", time.Now(), time.Now().Add(time.Minute))
			return err
		},
		"update": func(ctx context.Context) error {
			return UpdateOutcomeQualitySignalsContext(ctx, "fixture", false, false)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			before := time.Now()
			err := call(ctx)
			if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) || time.Since(before) > 2*time.Second {
				t.Fatalf("blocked SQL did not obey deadline: err=%v ctx=%v elapsed=%s", err, ctx.Err(), time.Since(before))
			}
		})
	}
}

func TestPostgresOutcomeReceiptUsesIndexedRequestLookup(t *testing.T) {
	db := routingQueryPostgresFixture(t)
	if err := db.Exec(`INSERT INTO routing_outcome_legacy (request_id,decision_id,outcome_at)
		SELECT 'legacy-'||n,n,now() FROM generate_series(1,12000) n`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO logs (request_id,public_id,request_ts,created_at,routing_outcome_json)
		SELECT 'legacy-'||n,'fixture_'||n,0,0,jsonb_build_object('request_id','json-'||n,'decision_id',n)::text FROM generate_series(1,10000) n`).Error; err != nil {
		t.Fatal(err)
	}
	syncRoutingFactColumns(t, db)
	for _, table := range []string{"logs", "routing_outcome_legacy"} {
		if err := db.Exec("ANALYZE " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"json-9999", "legacy-11999", "absent"} {
		query, err := routingFactQuery(db.Session(&gorm.Session{DryRun: true}), &RoutingOutcome{}, "request_id = ?", id)
		if err != nil {
			t.Fatal(err)
		}
		statement := query.First(&RoutingOutcome{}).Statement
		var plan string
		if err := db.Raw("EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+statement.SQL.String(), statement.Vars...).Scan(&plan).Error; err != nil {
			t.Fatal(err)
		}
		if strings.Contains(plan, `"Join Type": "Anti"`) || !strings.Contains(plan, "logs_ro_request_id") || !strings.Contains(plan, "idx_request_id") {
			t.Fatalf("request %q lost indexed scoped lookup: %s", id, plan)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		actual, err := GetOutcomeByRequestIDContext(ctx, id)
		cancel()
		if id == "absent" {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("missing outcome: %v", err)
			}
		} else if err != nil || actual.RequestID != id {
			t.Fatalf("request lookup: actual=%+v err=%v", actual, err)
		}
		t.Logf("indexed receipt lookup %s: %s", id, plan)
	}
}

func TestPostgresRoutingQualityHistoryPlan(t *testing.T) {
	db := routingQueryPostgresFixture(t)
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	if err := db.Exec(`INSERT INTO routing_decision_legacy (id,request_id,decision_at,is_primary,user_id,prompt_hash,session_id,resolved_model,strategy_name)
		SELECT n,'legacy-'||n,?::timestamptz,true,77,'prompt','session','other','baseline' FROM generate_series(1,12000) n`, start.Add(time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO logs (request_id,public_id,request_ts,created_at,routing_decision_json)
		SELECT 'legacy-'||n,'fixture_'||n,0,0,jsonb_build_object('id',n+20000,'request_id','json-'||n,'decision_at',?::timestamptz,'user_id',999,'prompt_hash','other')::text FROM generate_series(1,10000) n`, start).Error; err != nil {
		t.Fatal(err)
	}
	syncRoutingFactColumns(t, db)
	for _, table := range []string{"logs", "routing_decision_legacy"} {
		if err := db.Exec("ANALYZE " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	predicate := "user_id = ? AND prompt_hash = ? AND decision_at > ? AND decision_at <= ?"
	query, err := routingFactQuery(db, &RoutingDecision{}, predicate, 77, "prompt", start, start.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	statement := db.Session(&gorm.Session{DryRun: true}).Raw("SELECT EXISTS (?) AS present", query.Select("1").Limit(1)).Statement
	var plan string
	if err := db.Raw("EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+statement.SQL.String(), statement.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(plan), &decoded); err != nil {
		t.Fatal(err)
	}
	antiJoin := false
	var inspect func(interface{})
	inspect = func(node interface{}) {
		switch value := node.(type) {
		case map[string]interface{}:
			antiJoin = antiJoin || value["Join Type"] == "Anti"
			for _, child := range value {
				inspect(child)
			}
		case []interface{}:
			for _, child := range value {
				inspect(child)
			}
		}
	}
	inspect(decoded)
	if antiJoin || !strings.Contains(plan, "idx_request_id") {
		t.Fatalf("expected parameterized indexed global shadow lookup, plan=%s", plan)
	}
	var oldPlan string
	if err := db.Raw("EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT count(*) FROM routing_decision WHERE "+predicate, 77, "prompt", start, start.Add(5*time.Minute)).Scan(&oldPlan).Error; err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic 12000 legacy/10000 JSON facts; original plan=%s optimized plan=%s", oldPlan, plan)
	uid := 77
	if present, err := HasUserRetriedContext(context.Background(), &uid, "prompt", start, start.Add(5*time.Minute)); err != nil || !present {
		t.Fatalf("unshadowed legacy match lost: %v %v", present, err)
	}
}
