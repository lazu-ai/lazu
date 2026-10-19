package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Each test uses populated, separate PostgreSQL schemas and no package store
// bindings. These fixtures exercise migration SQL against real catalog state.
func migrationReviewPostgresStores(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	open := func(key, suffix string) *gorm.DB {
		dsn := os.Getenv(key)
		if dsn == "" {
			t.Skip("isolated PostgreSQL test DSNs are not configured")
		}
		dialector := func(value string) safePostgresDialector {
			return safePostgresDialector{postgres.New(postgres.Config{DSN: value, PreferSimpleProtocol: true}).(*postgres.Dialector)}
		}
		admin, err := gorm.Open(dialector(dsn), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("migration_review_%d_%s", time.Now().UnixNano(), suffix)
		if err := admin.Exec("CREATE SCHEMA " + name).Error; err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := u.Query()
		query.Set("search_path", name)
		u.RawQuery = query.Encode()
		db, err := gorm.Open(dialector(u.String()), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := admin.Exec("DROP SCHEMA " + name + " CASCADE").Error; err != nil {
				t.Errorf("remove fixture schema: %v", err)
			}
			for _, connection := range []*gorm.DB{db, admin} {
				if raw, err := connection.DB(); err == nil {
					_ = raw.Close()
				}
			}
		})
		return db
	}
	return open("TEST_GATEWAY_CORE_DSN", "source"), open("TEST_GATEWAY_TELEMETRY_DSN", "target")
}

func migrationReviewSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&SchemaVersion{}, &Log{}, &RoutingDecision{}, &RoutingOutcome{}, &RoutingAttemptOutcome{}, &ChannelProbeResult{}); err != nil {
		t.Fatal(err)
	}
}

func migrationReviewRoutingHistory(t *testing.T, db *gorm.DB) {
	t.Helper()
	at := time.Date(2026, time.April, 17, 12, 34, 56, 0, time.UTC)
	user, project, channel, status, tokens := 7, 11, 19, 200, 37
	attempted, failed, retried := true, false, true
	cost, charge, margin := 0.00012345, 0.00022345, 0.0001
	for i, requestID := range []string{"historical-only", "superseded-history"} {
		key := "legacy-idempotency-" + requestID
		decision := RoutingDecision{ID: int64(400 + i), RequestID: requestID, IdempotencyKey: &key, DecisionAt: at, IsPrimary: true, UserID: &user, TokenID: &project, SessionID: "historical-session", RequestedModel: "before", ResolvedModel: "legacy-model", PromptHash: "preserved-hash", StrategyName: "legacy", ChoseChannel: &channel, ChoseProvider: "legacy-provider", DecisionReason: "legacy-reason", CreatedAt: &at}
		outcome := RoutingOutcome{RequestID: requestID, OutcomeAt: at.Add(time.Second), DecisionID: decision.ID, UserID: &user, ProjectID: &project, ModelName: "legacy-model", ChannelID: &channel, ChannelName: "legacy-channel", StatusCode: &status, ProviderAttempted: &attempted, HealthFailed: &failed, ActualInputTokens: &tokens, UpstreamCostUSD: &cost, ChargedCostUSD: &charge, GrossMarginUSD: &margin, UserRetriedWithin5Min: &retried, CreatedAt: &at}
		if err := db.Create(&decision).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&outcome).Error; err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			// The current projection hides this legacy row, but the original is
			// still historical evidence and must survive copying and checksums.
			decision.ID = 9001
			decision.StrategyName = "current"
			outcome.DecisionID = decision.ID
			outcome.ModelName = "current-model"
			d, _ := json.Marshal(decision)
			o, _ := json.Marshal(outcome)
			log := Log{Id: 7001, PublicID: "log_current", RequestID: requestID, RequestTS: at.UnixMilli(), CreatedAt: at.Unix(), Type: LogTypeConsume, RoutingDecisionJSON: string(d), RoutingOutcomeJSON: string(o)}
			if err := db.Create(&log).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresTelemetryCopyPreservesHistoricalRoutingFacts(t *testing.T) {
	source, target := migrationReviewPostgresStores(t)
	migrationReviewSchema(t, source)
	migrationReviewSchema(t, target)
	migrationReviewRoutingHistory(t, source)
	if err := migrateTelemetryPartitions(target); err != nil {
		t.Fatal(err)
	}
	for retry := 0; retry < 2; retry++ {
		if err := CopyTelemetryHistory(context.Background(), source, target); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"routing_decision_legacy", "routing_outcome_legacy", "routing_decision", "routing_outcome"} {
		var count int64
		if err := target.Table(table).Count(&count).Error; err != nil || count != 2 {
			t.Errorf("%s lost historical/current routing facts: count=%d want=2 err=%v", table, count, err)
		}
	}
	left, err := BuildMigrationProof(context.Background(), source, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuildMigrationProof(context.Background(), target, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareMigrationProofs(left, right); err != nil {
		t.Errorf("copied historical routing content differs: %v", err)
	}
}

func TestPostgresTelemetryProofDetectsSupersededLegacyFactChanges(t *testing.T) {
	source, target := migrationReviewPostgresStores(t)
	for _, db := range []*gorm.DB{source, target} {
		migrationReviewSchema(t, db)
		migrationReviewRoutingHistory(t, db)
	}
	before, err := BuildMigrationProof(context.Background(), target, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Table("routing_decision_legacy").Where("request_id = ?", "superseded-history").Update("decision_reason", "corrupted-history").Error; err != nil {
		t.Fatal(err)
	}
	after, err := BuildMigrationProof(context.Background(), target, "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareMigrationProofs(before, after); err == nil {
		t.Fatal("verification accepted changed superseded legacy routing content")
	}
}

func TestPostgresTelemetryPartitionBackfillsPopulatedLegacySchema(t *testing.T) {
	db, _ := migrationReviewPostgresStores(t)
	at := time.Date(2026, time.June, 9, 10, 11, 12, 0, time.UTC)
	// These are the pre-partition columns and primary keys. Adding request_ts
	// to populated tables produces NULL rather than the Go model's zero value.
	for _, ddl := range []string{
		"CREATE TABLE logs (id bigserial PRIMARY KEY, public_id varchar(64), request_id varchar(64), created_at bigint, type bigint)",
		"CREATE TABLE routing_attempt_outcome (request_id varchar(64), attempt_index bigint, outcome_at timestamptz NOT NULL, decision_id bigint NOT NULL, channel_id bigint NOT NULL, PRIMARY KEY(request_id,attempt_index))",
		"CREATE TABLE channel_probe_result (id bigserial PRIMARY KEY, probed_at timestamptz)",
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO logs (id,public_id,request_id,created_at,type) VALUES (71,'log_old_schema','old-schema',?,?)", at.Unix(), LogTypeConsume).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO routing_attempt_outcome VALUES ('old-schema',1,?,1,1)", at).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO channel_probe_result (id,probed_at) VALUES (51,?)", at).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&SchemaVersion{}); err != nil {
		t.Fatal(err)
	}
	if err := migrateTelemetrySchema(db); err != nil {
		t.Fatal(err)
	}
	for _, spec := range telemetryPartitionSpecs {
		var nulls int64
		if err := db.Table(spec.table).Where("request_ts IS NULL").Count(&nulls).Error; err != nil || nulls != 1 {
			t.Fatalf("fixture did not reproduce newly added NULL request_ts in %s: nulls=%d err=%v", spec.table, nulls, err)
		}
	}
	if err := db.Exec("INSERT INTO logs (id,public_id,request_id,created_at,type,request_ts) VALUES (72,'log_old_zero','old-zero',?,?,0)", at.Unix(), LogTypeConsume).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO routing_attempt_outcome (request_id,attempt_index,outcome_at,decision_id,channel_id,request_ts) VALUES ('old-zero',1,?,1,1,0)", at).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO channel_probe_result (id,probed_at,request_ts) VALUES (52,?,0)", at).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	for _, spec := range telemetryPartitionSpecs {
		var rows []struct {
			RequestTS int64
			Partition string
		}
		if err := db.Table(spec.table).Select("request_ts,tableoid::regclass::text AS partition").Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.RequestTS != at.UnixMilli() || !strings.HasSuffix(row.Partition, "_202606") {
				t.Errorf("%s history entered wrong partition: %+v want timestamp=%d partition month=202606", spec.table, row, at.UnixMilli())
			}
		}
	}
	if err := AuditTelemetryPartitions(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresTelemetryPartitionOwnsAndResetsSequences(t *testing.T) {
	db, _ := migrationReviewPostgresStores(t)
	migrationReviewSchema(t, db)
	at := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&Log{Id: 9009, PublicID: "log_sequence_old", RequestID: "sequence-old", RequestTS: at.UnixMilli(), CreatedAt: at.Unix(), Type: LogTypeConsume}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&ChannelProbeResult{Id: 6007, RequestTS: at.UnixMilli(), ProbedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"logs", "channel_probe_result"} {
		var sequence string
		if err := db.Raw("SELECT COALESCE(pg_get_serial_sequence(?,'id'),'')", table).Scan(&sequence).Error; err != nil || sequence == "" {
			t.Errorf("%s partition parent does not own its ID sequence: sequence=%q err=%v", table, sequence, err)
		}
	}
	next := Log{PublicID: "log_sequence_new", RequestID: "sequence-new", RequestTS: at.UnixMilli(), CreatedAt: at.Unix(), Type: LogTypeConsume}
	if err := db.Create(&next).Error; err != nil || next.Id <= 9009 {
		t.Errorf("new log ID did not exceed migrated history: id=%d err=%v", next.Id, err)
	}
	probe := ChannelProbeResult{RequestTS: at.UnixMilli(), ProbedAt: at}
	if err := db.Create(&probe).Error; err != nil || probe.Id <= 6007 {
		t.Errorf("new probe ID did not exceed migrated history: id=%d err=%v", probe.Id, err)
	}
	if t.Failed() {
		return
	}
	for _, table := range []string{"logs", "channel_probe_result"} {
		if err := db.Exec("DROP TABLE " + table + "_unpartitioned_v24").Error; err != nil {
			t.Fatalf("removing retained original breaks live sequence dependency: %v", err)
		}
	}
	afterDrop := Log{PublicID: "log_after_archive_drop", RequestID: "after-archive-drop", RequestTS: at.UnixMilli(), CreatedAt: at.Unix(), Type: LogTypeConsume}
	if err := db.Create(&afterDrop).Error; err != nil || afterDrop.Id <= next.Id {
		t.Fatalf("new log failed after original removal: id=%d err=%v", afterDrop.Id, err)
	}
}

func TestPostgresTelemetrySequenceResetRepairsExistingPartitionOwnership(t *testing.T) {
	db, _ := migrationReviewPostgresStores(t)
	migrationReviewSchema(t, db)
	at := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&Log{Id: 9901, PublicID: "log_repair_old", RequestID: "repair-old", RequestTS: at.UnixMilli(), CreatedAt: at.Unix(), Type: LogTypeConsume}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	var sequence string
	if err := db.Raw("SELECT s.oid::regclass::text FROM pg_attrdef ad JOIN pg_attribute a ON a.attrelid=ad.adrelid AND a.attnum=ad.adnum JOIN pg_depend d ON d.classid='pg_attrdef'::regclass AND d.objid=ad.oid AND d.refclassid='pg_class'::regclass JOIN pg_class s ON s.oid=d.refobjid AND s.relkind='S' WHERE ad.adrelid=to_regclass('logs') AND a.attname='id'").Scan(&sequence).Error; err != nil || sequence == "" {
		t.Fatalf("find copied serial default: %q %v", sequence, err)
	}
	// Recreate the pre-fix migration state independently of the current
	// migration implementation: default points at a sequence owned by archive.
	if err := db.Exec("ALTER SEQUENCE " + sequence + " OWNED BY logs_unpartitioned_v24.id").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("SELECT setval(?::regclass,1,false)", sequence).Error; err != nil {
		t.Fatal(err)
	}
	if err := ResetMigrationSequences(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var repaired string
	if err := db.Raw("SELECT COALESCE(pg_get_serial_sequence('logs','id'),'')").Scan(&repaired).Error; err != nil || repaired == "" {
		t.Errorf("existing partition ownership was not repaired: %q %v", repaired, err)
	}
	next := Log{PublicID: "log_repair_new", RequestID: "repair-new", RequestTS: at.UnixMilli(), CreatedAt: at.Unix(), Type: LogTypeConsume}
	if err := db.Create(&next).Error; err != nil || next.Id <= 9901 {
		t.Errorf("existing partition sequence was not reseeded: id=%d err=%v", next.Id, err)
	}
}

func TestPostgresPartitionMarkerFailureRollsBackSchema(t *testing.T) {
	_, db := migrationReviewPostgresStores(t)
	migrationReviewSchema(t, db)
	if err := db.Exec("ALTER TABLE schema_versions ADD CONSTRAINT reject_partition_marker CHECK (scope <> 'telemetry_partition_v24')").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTelemetryPartitions(db); err == nil {
		t.Fatal("expected marker failure")
	}
	if db.Migrator().HasTable("logs_unpartitioned_v24") {
		t.Error("schema committed without migration marker")
	}
	if err := db.Exec("ALTER TABLE schema_versions DROP CONSTRAINT reject_partition_marker").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatalf("retry wedged: %v", err)
	}
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatalf("repeat failed: %v", err)
	}
}
