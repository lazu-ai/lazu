package model

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
)

func TestLegacyRollbackMarkerRejectsUnsupportedOrNewerTargetsWithoutChangingHistory(t *testing.T) {
	db := withSchemaVersionTestDB(t)
	if err := db.AutoMigrate(&SchemaVersion{}, &WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{schemaVersionPrimary, schemaVersionLog} {
		if err := db.Create(&SchemaVersion{Scope: scope, Version: SchemaVersionCurrent}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&WalletLedger{UserID: 7, DeltaMicrousd: 1234567, RefID: "retained-fact"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{-1, 0, 22, 26, 27} {
		if err := MarkLegacySchemaRollbackToVersion(context.Background(), db, version); err == nil {
			t.Fatalf("accepted unsupported legacy target %d", version)
		}
	}
	var versions []SchemaVersion
	if err := db.Order("scope").Find(&versions).Error; err != nil || len(versions) != 2 || versions[0].Version != SchemaVersionCurrent || versions[1].Version != SchemaVersionCurrent {
		t.Fatalf("rejected target changed schema markers: %+v %v", versions, err)
	}
	if err := MarkLegacySchemaRollback(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := MarkLegacySchemaRollbackToVersion(context.Background(), db, 25); err == nil {
		t.Fatal("rollback marker pretended to upgrade schema23 to25")
	}
	var fact WalletLedger
	if err := db.First(&fact).Error; err != nil || fact.DeltaMicrousd.Int64() != 1234567 || fact.RefID != "retained-fact" {
		t.Fatalf("marker checks changed retained wallet fact: %+v %v", fact, err)
	}
}

func TestLegacyRollbackOpeningInvalidationCommitsWithMarkersIntegration(t *testing.T) {
	for _, scenario := range []string{"success", "marker write fails"} {
		t.Run(scenario, func(t *testing.T) {
			db, client, dirs, proof := ledgerOpeningContinuityFixture(t, true)
			ctx := context.Background()
			state := ledgerOpeningContinuityBootstrap(t, db, dirs, proof)
			if err := db.AutoMigrate(&SchemaVersion{}); err != nil {
				t.Fatal(err)
			}
			for _, scope := range []string{schemaVersionPrimary, schemaVersionLog} {
				if err := db.Create(&SchemaVersion{Scope: scope, Version: SchemaVersionCurrent}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "marker write fails" {
				if err := db.Callback().Create().Before("gorm:create").Register("test:opening_rollback_marker_failure", func(tx *gorm.DB) {
					if row, ok := tx.Statement.Dest.(*SchemaVersion); ok && row.Scope == schemaVersionLog && row.Version == 25 {
						tx.AddError(errors.New("fixture final marker write failed"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			err := MarkLegacySchemaRollbackToVersion(ctx, db, 25)
			var after LedgerContinuityState
			if readErr := db.First(&after, 1).Error; readErr != nil {
				t.Fatal(readErr)
			}
			var versions []SchemaVersion
			if readErr := db.Order("scope").Find(&versions).Error; readErr != nil || len(versions) != 2 {
				t.Fatalf("markers: %+v, %v", versions, readErr)
			}
			if scenario == "marker write fails" {
				if err == nil || after.Status != "active" || versions[0].Version != SchemaVersionCurrent || versions[1].Version != SchemaVersionCurrent {
					t.Fatalf("failed marker left partial rollback: state=%+v markers=%+v err=%v", after, versions, err)
				}
				return
			}
			if err != nil || after.Status != "legacy_rollback" || after.OpeningID != state.OpeningID || after.OpeningSHA256 != state.OpeningSHA256 || versions[0].Version != 25 || versions[1].Version != 25 {
				t.Fatalf("rollback did not retain+invalidate opening atomically: state=%+v markers=%+v err=%v", after, versions, err)
			}
			if err := BindLedgerContinuity(ctx, db, ledger.New(client, redisLedgerStorage{})); err == nil {
				t.Fatal("new runtime bound a legacy rollback opening")
			}
			proof.Opening = nil
			if _, err := RecoverLedgerContinuity(ctx, db, dirs, true, proof); err == nil {
				t.Fatal("recovery reactivated a legacy rollback opening")
			}
			if err := ResumeLedgerContinuity(ctx, db, dirs, true); err == nil {
				t.Fatal("resume reactivated a legacy rollback opening")
			}
			if _, err := BootstrapLedgerContinuityWithProof(ctx, db, dirs, true, &proof); err == nil {
				t.Fatal("bootstrap replaced a legacy rollback opening")
			}
			// Restoring a schema number alone is not a new cutover protocol.
			if err := db.Model(&SchemaVersion{}).Where("scope IN ?", []string{schemaVersionPrimary, schemaVersionLog}).Update("version", SchemaVersionCurrent).Error; err != nil {
				t.Fatal(err)
			}
			if err := BindLedgerContinuity(ctx, db, ledger.New(client, redisLedgerStorage{})); err == nil {
				t.Fatal("schema marker alone revived the invalidated opening")
			}
			if _, err := VerifyLedgerOpeningHistory(ctx, db, LedgerOpeningBinding{state.OpeningID, state.OpeningSHA256}); err != nil {
				t.Fatalf("rollback removed historical opening evidence: %v", err)
			}
		})
	}
}

func TestPostgresLegacyRollbackRestoresSchema25WriterShapesAndHistory(t *testing.T) {
	core, telemetry := postgresTelemetryFixture(t)
	ctx := context.Background()
	if err := core.AutoMigrate(append(TelemetrySchemaModels(), &SchemaVersion{}, &Token{}, &WalletLedger{}, &SignupAttribution{})...); err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&SchemaVersion{Scope: schemaVersionPrimary, Version: SchemaVersionCurrent}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := core.Session(&gorm.Session{SkipHooks: true}).Create(&User{Id: 77, Username: "rollback-schema25", Quota: 1234567}).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Session(&gorm.Session{SkipHooks: true}).Create(&Token{Id: 77, UserId: 77, Key: "isolated-rollback-key", RemainQuota: 765432}).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&WalletLedger{UserID: 77, DeltaMicrousd: 1234567, RefType: "fixture", RefID: "retained-money", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&SignupAttribution{UserID: 77, Source: "chatgpt.com", SelfReported: "reddit", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Create(&Log{Id: 11, PublicID: "retained-log", RequestID: "retained-request", RequestTS: now.Unix(), CreatedAt: now.Unix(), UserId: 77, Type: LogTypeConsume, AmountMicrousd: 321}).Error; err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Create(&RoutingAttemptOutcome{RequestTS: now.Unix(), RequestID: "retained-attempt", AttemptIndex: 1, DecisionID: 1, ChannelID: 1, OutcomeAt: now, StatusCode: 503}).Error; err != nil {
		t.Fatal(err)
	}
	if err := telemetry.Create(&ChannelProbeResult{Id: 31, RequestTS: now.UnixMilli(), ModelName: "retained-model", ProbedAt: now, Success: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := CopyTelemetryHistoryForRollback(ctx, telemetry, core); err != nil {
		t.Fatal(err)
	}
	read := func(table, order string) []string {
		t.Helper()
		var rows []string
		if err := core.Raw("SELECT to_jsonb(row)::text FROM " + table + " row ORDER BY " + order).Scan(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}
	tables := map[string]string{"users": "id", "tokens": "id", "wallet_ledgers": "id", "signup_attributions": "user_id", "logs": "id", "routing_attempt_outcome": "request_id,attempt_index", "channel_probe_result": "id"}
	before := map[string][]string{}
	for table, order := range tables {
		before[table] = read(table, order)
	}
	// The schema25 model has no request_ts field; the old upsert also names
	// (request_id,attempt_index), which the schema26 primary key cannot serve.
	if err := core.Exec("INSERT INTO logs(public_id,request_id,created_at,type) VALUES ('before-prepare','before-prepare',?,?)", now.Unix(), LogTypeConsume).Error; err == nil {
		t.Fatal("fixture no longer reproduces the omitted partition timestamp failure")
	}
	if err := PrepareLegacyTelemetryWrites(ctx, core); err != nil {
		t.Fatal(err)
	}
	if err := MarkLegacySchemaRollbackToVersion(ctx, core, 25); err != nil {
		t.Fatal(err)
	}
	for table, order := range tables {
		if !reflect.DeepEqual(before[table], read(table, order)) {
			t.Fatalf("preparing schema25 rollback changed retained %s rows", table)
		}
	}
	var versions []SchemaVersion
	if err := core.Order("scope").Find(&versions).Error; err != nil || len(versions) != 2 || versions[0].Version != 25 || versions[1].Version != 25 {
		t.Fatalf("previous schema25 image will reject prepared Core: %+v %v", versions, err)
	}
	if err := ResetMigrationSequences(ctx, core); err != nil {
		t.Fatal(err)
	}
	if err := core.Exec("INSERT INTO logs(public_id,request_id,created_at,type,amount_microusd) VALUES ('old-writer','old-writer',?,?,456) ON CONFLICT DO NOTHING", now.Unix(), LogTypeConsume).Error; err != nil {
		t.Fatalf("schema25 log writer cannot insert: %v", err)
	}
	for _, status := range []int{500, 502} {
		if err := core.Exec("INSERT INTO routing_attempt_outcome(request_id,attempt_index,outcome_at,decision_id,channel_id,status_code) VALUES ('old-writer',1,?,1,1,?) ON CONFLICT(request_id,attempt_index) DO UPDATE SET status_code=EXCLUDED.status_code", now, status).Error; err != nil {
			t.Fatalf("schema25 routing attempt upsert cannot write: %v", err)
		}
	}
	if err := core.Exec("INSERT INTO channel_probe_result(probed_at,model_name,success) VALUES (?,'old-writer',true)", now).Error; err != nil {
		t.Fatalf("schema25 probe writer cannot insert: %v", err)
	}
	var count int64
	if err := core.Table("routing_attempt_outcome").Where("request_id=? AND status_code=?", "old-writer", 502).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("old upsert duplicated/lost routing attempt: %d %v", count, err)
	}
	if err := PrepareLegacyTelemetryWrites(ctx, core); err != nil {
		t.Fatalf("prepared rollback cannot be retried: %v", err)
	}
}

func TestPostgresLegacyRollbackRejectsConflictingAttemptHistory(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	now := time.Now().UTC()
	for _, stamp := range []int64{1, 2} {
		if err := db.Create(&RoutingAttemptOutcome{RequestTS: stamp, RequestID: "conflicting-attempt", AttemptIndex: 1, DecisionID: stamp, ChannelID: 1, OutcomeAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := PrepareLegacyTelemetryWrites(context.Background(), db); err == nil {
		t.Fatal("legacy rollback accepted two retained facts with the same old identity")
	}
	var count int64
	if err := db.Model(&RoutingAttemptOutcome{}).Where("request_id=?", "conflicting-attempt").Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("rejected rollback removed conflicting history: %d %v", count, err)
	}
	var defaults []string
	if err := db.Raw("SELECT COALESCE(column_default,'<none>') FROM information_schema.columns WHERE table_schema=current_schema() AND table_name IN ('logs','routing_attempt_outcome','channel_probe_result') AND column_name='request_ts' ORDER BY table_name").Scan(&defaults).Error; err != nil || !reflect.DeepEqual(defaults, []string{"<none>", "<none>", "<none>"}) {
		t.Fatalf("failed legacy preparation left partial defaults: %v %v", defaults, err)
	}
}

func TestPostgresLegacyRollbackRejectsPartitionedCoreTables(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	if err := PrepareLegacyTelemetryWrites(context.Background(), db); err == nil {
		t.Fatal("legacy writer preparation accepted partitioned tables that cannot enforce old identities")
	}
	var partitioned bool
	if err := db.Raw("SELECT relkind='p' FROM pg_class WHERE oid=to_regclass('logs')").Scan(&partitioned).Error; err != nil || !partitioned {
		t.Fatalf("rejected preparation changed the partitioned source: partitioned=%v err=%v", partitioned, err)
	}
}
