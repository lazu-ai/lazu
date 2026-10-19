package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func postgresTelemetryFixture(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	if os.Getenv("TEST_GATEWAY_CORE_DSN") == "" || os.Getenv("TEST_GATEWAY_TELEMETRY_DSN") == "" {
		t.Skip("isolated PostgreSQL test DSNs are not configured")
	}
	name := fmt.Sprintf("telemetry_test_%d", time.Now().UnixNano())
	open := func(dsn string) *gorm.DB {
		dialector := func(value string) safePostgresDialector {
			return safePostgresDialector{postgres.New(postgres.Config{DSN: value, PreferSimpleProtocol: true}).(*postgres.Dialector)}
		}
		admin, err := gorm.Open(dialector(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if err := admin.Exec("CREATE SCHEMA " + name).Error; err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", name)
		u.RawQuery = q.Encode()
		db, err := gorm.Open(dialector(u.String()), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = admin.Exec("DROP SCHEMA " + name + " CASCADE").Error
			raw, _ := db.DB()
			_ = raw.Close()
			raw, _ = admin.DB()
			_ = raw.Close()
		})
		return db
	}
	core, telemetry := open(os.Getenv("TEST_GATEWAY_CORE_DSN")), open(os.Getenv("TEST_GATEWAY_TELEMETRY_DSN"))
	oldCore, oldTelemetry := coreDB, telemetryDB
	coreDB, telemetryDB = core, telemetry
	t.Cleanup(func() { coreDB, telemetryDB = oldCore, oldTelemetry })
	if err := core.AutoMigrate(&User{}, &Channel{}, &Ability{}); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.AutoMigrate(telemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.AutoMigrate(&SchemaVersion{}); err != nil {
		t.Fatal(err)
	}
	return core, telemetry
}

func TestPostgresHourlyBatchMergesHistogramWithoutRowQueries(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	creates, reads, updates := 0, 0, 0
	db.Callback().Create().Before("gorm:create").Register("test:hourly-create", func(*gorm.DB) { creates++ })
	db.Callback().Query().Before("gorm:query").Register("test:hourly-read", func(*gorm.DB) { reads++ })
	db.Callback().Update().Before("gorm:update").Register("test:hourly-update", func(*gorm.DB) { updates++ })
	events := []relayResultEvent{telemetryFixture(common.GenerateRequestID()), telemetryFixture(common.GenerateRequestID())}
	logs := []Log{events[0].ConsumeLog.toLog(), events[1].ConsumeLog.toLog()}
	for index := 0; index < 2; index++ {
		if err := db.Transaction(func(tx *gorm.DB) error { return recordHourlyBatchTx(tx, logs, events) }); err != nil {
			t.Fatal(err)
		}
	}
	if creates != 4 || reads != 0 || updates != 0 {
		t.Fatalf("hourly batching creates=%d reads=%d updates=%d", creates, reads, updates)
	}
	var row DashboardUsageHourlyTotal
	if err := db.Where("scope_user_id = ?", 0).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var bins []int64
	if err := json.Unmarshal([]byte(row.LatencyBuckets), &bins); err != nil {
		t.Fatal(err)
	}
	var samples int64
	for _, bin := range bins {
		samples += bin
	}
	if row.RequestCount != 4 || row.AmountMicrousd != 4*123 || samples != 4 {
		t.Fatalf("hourly counters or histograms lost: %+v bins=%v", row, bins)
	}
}

func TestPostgresCachedSchemaChecksKeepReadinessOffSQL(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := cacheValidatedSchemaVersion(db, schemaVersionLog); err != nil {
		t.Fatal(err)
	}
	queries := 0
	db.Callback().Query().Before("gorm:query").Register("test:schema-queries", func(*gorm.DB) { queries++ })
	db.Callback().Raw().Before("gorm:raw").Register("test:schema-raw", func(*gorm.DB) { queries++ })
	for index := 0; index < 10; index++ {
		if err := validateSchemaVersion(db.Session(&gorm.Session{}), schemaVersionLog); err != nil {
			t.Fatal(err)
		}
		if !db.Session(&gorm.Session{}).Migrator().HasTable(&Log{}) || !db.Migrator().HasColumn(&Log{}, "request_ts") {
			t.Fatal("cached schema lost the validated table/column")
		}
	}
	if queries != 0 {
		t.Fatalf("validated runtime checks performed %d SQL queries", queries)
	}
}

func TestPostgresRollbackCopyOverwritesExistingHistoricalRow(t *testing.T) {
	core, telemetry := postgresTelemetryFixture(t)
	if err := core.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	row := Log{Id: 91, RequestID: common.GenerateRequestID(), PublicID: "log_rollback_existing", RequestTS: time.Now().UnixMilli(), CreatedAt: time.Now().Unix(), Type: LogTypeConsume, AmountMicrousd: 123}
	if err := telemetry.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	stale := row
	stale.AmountMicrousd = 1
	if err := core.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		if err := CopyTelemetryHistoryForRollback(context.Background(), telemetry, core); err != nil {
			t.Fatal(err)
		}
	}
	var actual Log
	if err := core.Where("id = ? AND request_ts = ?", row.Id, row.RequestTS).First(&actual).Error; err != nil {
		t.Fatal(err)
	}
	if actual.AmountMicrousd != row.AmountMicrousd {
		t.Fatalf("rollback preserved stale core amount %d instead of %d", actual.AmountMicrousd, row.AmountMicrousd)
	}
	var count int64
	if err := core.Model(&Log{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("rollback retry duplicated rows: %d %v", count, err)
	}
}

func TestPostgresTelemetryPartitionMigrationAndRollback(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	now := time.Now().UTC()
	historical := now.AddDate(0, -3, 0)
	decision := RoutingDecision{RequestID: "historical-request", DecisionAt: historical, IsPrimary: true, StrategyName: "baseline"}
	if err := db.Create(&decision).Error; err != nil {
		t.Fatal(err)
	}
	outcome := RoutingOutcome{RequestID: decision.RequestID, DecisionID: decision.ID, OutcomeAt: historical}
	if err := db.Create(&outcome).Error; err != nil {
		t.Fatal(err)
	}
	oldLog := Log{RequestID: decision.RequestID, CreatedAt: historical.Unix(), Type: LogTypeConsume}
	if err := db.Create(&oldLog).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	if err := AuditTelemetryPartitions(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := GetOutcomeByRequestID(decision.RequestID); err != nil {
		t.Fatal(err)
	}
	event := telemetryFixture(common.GenerateRequestID())
	event.RoutingDecision.ID = stableRoutingDecisionID(event.RoutingDecision.RequestID)
	event.RoutingOutcome.DecisionID = event.RoutingDecision.ID
	// A newer projected fact can supersede an older physical row for the
	// same request. Rollback must restore exactly the view's current facts.
	prior := RoutingDecision{RequestID: event.RoutingDecision.RequestID, DecisionAt: historical, IsPrimary: true, StrategyName: "old"}
	if err := db.Table("routing_decision_legacy").Create(&prior).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("routing_outcome_legacy").Create(&RoutingOutcome{RequestID: prior.RequestID, DecisionID: prior.ID, OutcomeAt: historical}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := createRoutingDecisionSync(event.RoutingDecision); err != nil {
		t.Fatal(err)
	}
	if err := recordRoutingOutcomeSync(event.RoutingOutcome); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := persistRelayResultBatch([]relayResultEvent{event}); err != nil {
			t.Fatal(err)
		}
	}
	if err := UpdateOutcomeQualitySignals(event.RoutingOutcome.RequestID, true, false); err != nil {
		t.Fatal(err)
	}
	if err := persistRoutingSnapshotEvent(event); err != nil {
		t.Fatal(err)
	}
	actual, err := GetOutcomeByRequestID(event.RoutingOutcome.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.UserRetriedWithin5Min == nil || !*actual.UserRetriedWithin5Min {
		t.Fatalf("quality patch not visible: %+v", actual)
	}
	var logs int64
	if err := db.Model(&Log{}).Where("request_id = ?", event.ConsumeLog.RequestID).Count(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if logs != 1 {
		t.Fatalf("completion duplicated provisional log: %d", logs)
	}
	if err := MaintainTelemetryPartitions(context.Background(), now, 180); err != nil {
		t.Fatal(err)
	}
	if err := PrepareLegacyTelemetryRollback(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	newDecision := RoutingDecision{RequestID: common.GenerateRequestID(), DecisionAt: now, IsPrimary: true, StrategyName: "rollback"}
	if err := db.Create(&newDecision).Error; err != nil {
		t.Fatalf("old writer cannot insert after rollback: %v", err)
	}
	var n int64
	if err := db.Model(&RoutingOutcome{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rollback lost history/new outcomes: %d", n)
	}
	// After rollback, old images read the physical table. The current runtime
	// lookup requires the validated compatibility-view schema, which no longer
	// exists here. Verify the legacy read preserves the same quality fields.
	var restored RoutingOutcome
	err = db.Where("request_id = ?", event.RoutingOutcome.RequestID).First(&restored).Error
	actual = &restored
	if err != nil || actual.UserRetriedWithin5Min == nil || !*actual.UserRetriedWithin5Min {
		t.Fatalf("rollback lost the current quality fields: %+v %v", actual, err)
	}
}
