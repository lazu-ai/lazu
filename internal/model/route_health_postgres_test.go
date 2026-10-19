package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// The schema-31 backfill runs while the schema catalogue is invalidated, where
// GORM's own HasTable only counts base tables. routing_outcome and
// routing_decision are views, so a naive check backfilled probes only and the
// first thirty days of history lost every real request.
func TestPostgresRouteHealthBackfillSeesTrafficThroughViews(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	if err := migrateRoutingFactColumns(db); err != nil {
		t.Fatal(err)
	}
	if !channelHealthHasTable(db, &RoutingOutcome{}) || !channelHealthHasTable(db, &RoutingDecision{}) {
		t.Fatal("routing views must count as present without a validated catalogue")
	}
	at := time.Now().UTC().Add(-90 * time.Minute)
	channel, status, duration := 41, 200, 800
	requestID := common.GenerateRequestID()
	decision := RoutingDecision{ID: stableRoutingDecisionID(requestID), RequestID: requestID, DecisionAt: at, IsPrimary: true,
		RequestedModel: "backfill-model", ResolvedModel: "backfill-model", StrategyName: "baseline"}
	outcome := RoutingOutcome{RequestID: requestID, OutcomeAt: at, DecisionID: decision.ID, ChannelID: &channel,
		FinalGroup: ChannelLaneDirect, StatusCode: &status, TotalDurationMs: &duration}
	if err := persistRoutingSnapshot(requestID, at, &decision, &outcome); err != nil {
		t.Fatal(err)
	}
	invalidateSchemaCatalogue(db)
	if err := migrateRouteHealthRollup(db); err != nil {
		t.Fatal(err)
	}
	var rows []RouteHealthHourly
	if err := db.Where("model_name = ?", "backfill-model").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Kind != ChannelHealthKindTraffic || rows[0].Samples != 1 || rows[0].ChannelID != channel {
		t.Fatalf("backfill rows = %+v, want the one answered request as traffic", rows)
	}
	if db.Migrator().HasTable("channel_health_materialized_snapshot") {
		t.Fatal("the retired snapshot table survived the migration")
	}
}
