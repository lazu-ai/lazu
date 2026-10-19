package model

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// jsonEraRoutingViewSQL is the schema-29 view: every column decoded from the
// JSON copy. The migration test starts from it, as production does.
func jsonEraRoutingViewSQL(parsed *schema.Schema) string {
	column := "routing_decision_json"
	if parsed.Table == "routing_outcome" {
		column = "routing_outcome_json"
	}
	expressions, columns := []string{}, []string{}
	for _, field := range parsed.Fields {
		if field.DBName == "" {
			continue
		}
		columns = append(columns, field.DBName)
		name, _ := routingJSONName(field.StructField)
		if name == "-" {
			name = field.DBName
		}
		value := "(" + column + "::jsonb ->> '" + name + "')"
		if kind := routingSQLType(field.FieldType); kind != "text" {
			value = "(" + value + ")::" + kind
		}
		expressions = append(expressions, value+" AS "+field.DBName)
	}
	return fmt.Sprintf("CREATE VIEW %s AS SELECT %s FROM logs WHERE NULLIF(%s, '') IS NOT NULL UNION ALL SELECT %s FROM %s_legacy old WHERE NOT EXISTS (SELECT 1 FROM logs newer WHERE newer.request_id = old.request_id AND NULLIF(newer.%s, '') IS NOT NULL)",
		parsed.Table, strings.Join(expressions, ","), column, strings.Join(columns, ","), parsed.Table, column)
}

func TestPostgresRoutingColumnsMigrationFromJSONViews(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	for _, model := range []interface{}{&RoutingDecision{}, &RoutingOutcome{}} {
		parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("DROP VIEW " + parsed.Table).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(jsonEraRoutingViewSQL(parsed)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE INDEX lazu_logs_snapshot_outcome_req_v1 ON logs ((routing_outcome_json::jsonb ->> 'request_id')) WHERE routing_outcome_json <> ''").Error; err != nil {
		t.Fatal(err)
	}

	// Rows as schema 29 wrote them: JSON only, across several days, plus a
	// legacy fact one of them supersedes and one that stays visible.
	base := time.Date(2026, 10, 9, 6, 0, 0, 0, time.UTC)
	want := map[string]RoutingOutcome{}
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * 30 * time.Hour)
		requestID := fmt.Sprintf("req-%d", i)
		status, ttft, streaming, cost := 200+i, 100*i, i%2 == 0, 0.25*float64(i)
		outcome := RoutingOutcome{RequestID: requestID, OutcomeAt: at, DecisionID: stableRoutingDecisionID(requestID), FinalGroup: ChannelLaneAgent, StatusCode: &status, TTFTMs: &ttft, WasStreaming: &streaming, UpstreamCostUSD: &cost, FinishReason: "stop"}
		decision := RoutingDecision{ID: stableRoutingDecisionID(requestID), RequestID: requestID, DecisionAt: at.Add(-time.Second), IsPrimary: true, ResolvedModel: "model-x", StrategyName: "baseline"}
		row := Log{RequestID: requestID, CreatedAt: at.Unix(), RequestTS: at.UnixMilli(), Type: LogTypeConsume}
		ensureConsumeLogPublicID(&row)
		foldRelayRoutingIntoLog(&row, relayResultEvent{RoutingDecision: &decision, RoutingOutcome: &outcome})
		row.RoutingOutcome, row.RoutingDecision = LogRoutingOutcome{}, LogRoutingDecision{}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		want[requestID] = outcome
	}
	for _, requestID := range []string{"req-0", "legacy-only"} {
		if err := db.Table("routing_outcome_legacy").Create(&RoutingOutcome{RequestID: requestID, OutcomeAt: base.Add(-48 * time.Hour), DecisionID: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var before []RoutingOutcome
	if err := db.Table("routing_outcome").Order("request_id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}

	if err := migrateRoutingFactColumns(db); err != nil {
		t.Fatal(err)
	}
	if err := migrateRoutingFactColumns(db); err != nil {
		t.Fatal("the one-time migration must be idempotent: ", err)
	}

	var pending int64
	if err := db.Model(&Log{}).Where("routing_outcome_json <> '' AND ro_request_id IS NULL OR routing_decision_json <> '' AND rd_id IS NULL").Count(&pending).Error; err != nil || pending != 0 {
		t.Fatalf("rows left unprojected: %d %v", pending, err)
	}
	for _, table := range []string{"routing_outcome", "routing_decision"} {
		var definition string
		if err := db.Raw("SELECT pg_get_viewdef(?::regclass, true)", table).Scan(&definition).Error; err != nil {
			t.Fatal(err)
		}
		if strings.Contains(definition, "jsonb") || !strings.Contains(definition, "logs.r") {
			t.Fatalf("%s still decodes JSON: %s", table, definition)
		}
	}
	var after []RoutingOutcome
	if err := db.Table("routing_outcome").Order("request_id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stripRoutingCreatedAt(before), stripRoutingCreatedAt(after)) {
		t.Fatalf("view contents changed\nbefore=%+v\nafter=%+v", before, after)
	}
	if len(after) != 6 {
		t.Fatalf("want five log outcomes and one unsuperseded legacy outcome, got %d", len(after))
	}
	var decision RoutingDecision
	if err := db.Table("routing_decision").Where("request_id = ? AND is_primary", "req-3").First(&decision).Error; err != nil || decision.ResolvedModel != "model-x" {
		t.Fatalf("decision lookup: %+v %v", decision, err)
	}
	var indexes []string
	if err := db.Raw("SELECT indexname FROM pg_indexes WHERE tablename = 'logs'").Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	have := strings.Join(indexes, ",")
	for _, index := range routingFactIndexes {
		if !strings.Contains(have, index.name) {
			t.Errorf("missing index %s", index.name)
		}
	}
	if strings.Contains(have, "lazu_logs_snapshot_outcome_req_v1") {
		t.Error("retired JSON expression index survived")
	}

	// New writes land in the typed columns directly and read back the same.
	freshID := common.GenerateRequestID()
	at := base.Add(200 * time.Hour)
	status := 503
	fresh := RoutingOutcome{RequestID: freshID, OutcomeAt: at, DecisionID: 9, FinalGroup: ChannelLaneDirect, StatusCode: &status}
	if err := persistRoutingSnapshot(freshID, at, &RoutingDecision{ID: 9, RequestID: freshID, DecisionAt: at, IsPrimary: true, StrategyName: "baseline"}, &fresh); err != nil {
		t.Fatal(err)
	}
	got, err := GetOutcomeByRequestID(freshID)
	if err != nil || got.StatusCode == nil || *got.StatusCode != 503 || !got.OutcomeAt.Equal(at) {
		t.Fatalf("fresh outcome: %+v %v", got, err)
	}
	if err := UpdateOutcomeQualitySignals(freshID, true, false); err != nil {
		t.Fatal(err)
	}
	if got, err = GetOutcomeByRequestID(freshID); err != nil || got.UserRetriedWithin5Min == nil || !*got.UserRetriedWithin5Min {
		t.Fatalf("quality signal not visible through the typed view: %+v %v", got, err)
	}
	var stored Log
	if err := db.Where("request_id = ?", freshID).First(&stored).Error; err != nil || !strings.Contains(stored.RoutingOutcomeJSON, `"user_retried_within_5min": true`) && !strings.Contains(stored.RoutingOutcomeJSON, `"user_retried_within_5min":true`) {
		t.Fatalf("JSON copy drifted from its columns: %v %s", err, stored.RoutingOutcomeJSON)
	}
}

func stripRoutingCreatedAt(rows []RoutingOutcome) []RoutingOutcome {
	out := make([]RoutingOutcome, len(rows))
	for i, row := range rows {
		row.CreatedAt = nil
		row.OutcomeAt = row.OutcomeAt.UTC()
		out[i] = row
	}
	return out
}

var _ = gorm.ErrRecordNotFound
