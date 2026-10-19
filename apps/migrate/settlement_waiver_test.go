package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestParseFailureIDsExactPositiveInventory(t *testing.T) {
	for _, raw := range []string{"", " ", "0", "-1", "1,", ",1", "1,,2", "1,1", "1,01", "1, 1", "1.0", "1e2", "0x10", "4294967296", "18446744073709551616"} {
		t.Run("reject-"+raw, func(t *testing.T) {
			if ids, err := parseFailureIDs(raw); err == nil {
				t.Fatalf("invalid inventory accepted: %q %v", raw, ids)
			}
		})
	}
	for _, item := range []struct {
		raw  string
		want []uint
	}{{"1", []uint{1}}, {" 3, 1 ,2 ", []uint{3, 1, 2}}, {"01,2", []uint{1, 2}}, {"4294967295", []uint{4294967295}}} {
		if got, err := parseFailureIDs(item.raw); err != nil || !reflect.DeepEqual(got, item.want) {
			t.Fatalf("inventory IDs/order changed: %q %v %v", item.raw, got, err)
		}
	}
}

func initialCutoverFixture(t *testing.T) (string, *gorm.DB, *redis.Client, string, string) {
	t.Helper()
	client, redisURL, stream := rollbackAuditRedis(t)
	oldRedis, oldStream, oldGroup := common.RDB, common.RelayEventStream, common.RelayEventGroup
	common.RDB, common.RelayEventStream, common.RelayEventGroup = client, stream, "initial-cutover-fixture"
	t.Cleanup(func() {
		common.RDB, common.RelayEventStream, common.RelayEventGroup = oldRedis, oldStream, oldGroup
	})
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "initial-cutover.sqlite")
	db, err := model.OpenMigrationDatabase(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
	if err := db.AutoMigrate(&model.LedgerContinuityState{}, &model.SettlementFailure{}, &model.SettlementOutbox{}, &model.WalletLedger{}, &model.RedisLedgerOperation{}, &model.LedgerCreditSync{}, &model.LedgerCounterRefresh{}, &model.LedgerLegacySettlementWaiver{}, &model.MediaJob{}); err != nil {
		t.Fatal(err)
	}
	return dsn, db, client, redisURL, stream
}

func TestInitialCutoverQuietRequiresStoppedWritersAndSpoolInventory(t *testing.T) {
	for _, item := range []struct {
		stopped bool
		dirs    []string
	}{{false, nil}, {false, []string{"spool"}}, {true, nil}} {
		if err := verifyInitialCutoverQuiet(context.Background(), nil, item.dirs, item.stopped); err == nil {
			t.Fatal("missing stop-write/spool attestation was accepted before opening stores")
		}
	}
}

func TestInitialCutoverQuietRejectsOutstandingEvidence(t *testing.T) {
	for _, scenario := range []string{"missing-maintenance", "existing-continuity", "orphaned-hot-continuity", "live-writer", "media", "reservations", "uncommitted", "exposure", "financial-dead-id", "financial-dead-stream", "old-outbox", "permanent-operation", "pending-credit", "pending-counter-refresh", "spool", "missing-spool-directory", "quarantine", "telemetry", "telemetry-pending", "telemetry-dead"} {
		t.Run(scenario, func(t *testing.T) {
			_, db, client, _, stream := initialCutoverFixture(t)
			ctx := context.Background()
			spool := t.TempDir()
			keys := []string{ledger.ContinuityKey, ledger.Prefix + "nodes:initial-cutover-fixture", ledger.Prefix + "reservations", ledger.Prefix + "uncommitted", ledger.Prefix + "exposure", ledger.Prefix + "dead_ids", ledger.DeadStream}
			t.Cleanup(func() {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
				}
			})
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "missing-maintenance":
				must(client.Del(ctx, ledger.Prefix+"maintenance").Err())
			case "existing-continuity":
				must(db.Create(&model.LedgerContinuityState{ID: 1, Epoch: "existing", AdmissionGeneration: 1, Status: "active"}).Error)
			case "orphaned-hot-continuity":
				must(client.Set(ctx, ledger.ContinuityKey, "lost-sql-epoch:1", 0).Err())
			case "live-writer":
				must(client.Set(ctx, ledger.Prefix+"nodes:initial-cutover-fixture", "writer", time.Minute).Err())
			case "media":
				must(db.Create(&model.MediaJob{UserID: 1, PublicID: "initial-media", Status: model.MediaJobStatusRunning}).Error)
			case "reservations":
				must(client.HSet(ctx, ledger.Prefix+"reservations", "pending", "reserved").Err())
			case "uncommitted":
				must(client.ZAdd(ctx, ledger.Prefix+"uncommitted", &redis.Z{Score: 1, Member: "pending"}).Err())
			case "exposure":
				must(client.Set(ctx, ledger.Prefix+"exposure", 17, 0).Err())
			case "financial-dead-id":
				must(client.HSet(ctx, ledger.Prefix+"dead_ids", "pending", "retained").Err())
			case "financial-dead-stream":
				must(client.XAdd(ctx, &redis.XAddArgs{Stream: ledger.DeadStream, Values: map[string]interface{}{"pending": "retained"}}).Err())
			case "old-outbox":
				must(db.Create(&model.SettlementOutbox{UserID: 1, RefType: "relay_settlement", RefID: "pending-outbox", Status: model.SettlementFailureStatusOpen}).Error)
			case "permanent-operation":
				must(db.Create(&model.RedisLedgerOperation{ID: "settle:pending", RequestID: "pending"}).Error)
			case "pending-credit":
				must(db.Create(&model.LedgerCreditSync{UserID: 1, Sequence: 1, Amount: 17}).Error)
			case "pending-counter-refresh":
				must(db.Create(&model.LedgerCounterRefresh{TableName: "tokens", ObjectID: 1}).Error)
			case "spool":
				must(os.WriteFile(filepath.Join(spool, "pending.json"), []byte(`{"request":"pending"}`), 0600))
			case "missing-spool-directory":
				spool = filepath.Join(spool, "absent")
			case "quarantine":
				must(os.Mkdir(filepath.Join(spool, "quarantine"), 0700))
				must(os.WriteFile(filepath.Join(spool, "quarantine", "pending.json"), []byte(`{"request":"pending"}`), 0600))
			case "telemetry":
				must(client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"pending": "retained"}}).Err())
			case "telemetry-pending":
				must(client.XGroupCreateMkStream(ctx, stream, common.RelayEventGroup, "0").Err())
				must(client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"pending": "retained"}}).Err())
				_, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: common.RelayEventGroup, Consumer: "fixture", Streams: []string{stream, ">"}, Count: 1, Block: -1}).Result()
				must(err)
			case "telemetry-dead":
				must(client.XAdd(ctx, &redis.XAddArgs{Stream: stream + ":dead", Values: map[string]interface{}{"pending": "retained"}}).Err())
			}
			if err := verifyInitialCutoverQuiet(ctx, db, []string{spool}, true); err == nil {
				t.Fatalf("outstanding %s evidence was accepted", scenario)
			}
		})
	}
}

func TestSettlementWaiveCommandRetriesOriginalProofBeforeBootstrap(t *testing.T) {
	if os.Getenv("LAZU_INITIAL_WAIVER_CHILD") == "1" {
		os.Args = []string{os.Args[0], "-command", "settlement-waive", "-writers-stopped", "-spool-dirs", os.Getenv("LAZU_INITIAL_WAIVER_SPOOL"), "-waiver-proof", os.Getenv("LAZU_INITIAL_WAIVER_PROOF")}
		main()
		return
	}
	dsn, db, _, redisURL, stream := initialCutoverFixture(t)
	spool := t.TempDir()
	original := model.SettlementFailure{ID: 1, RequestID: "initial-waiver-request", UserID: 1, TokenID: 2, QuotaDelta: 17, PreConsumed: 5, RefType: "relay_settlement", RefID: "initial-waiver-ref", Status: model.SettlementFailureStatusManualReview, Metadata: "{}"}
	if err := db.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&original, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	items, err := model.InventoryLegacySettlementWaiverItems(context.Background(), db, []uint{original.ID})
	if err != nil {
		t.Fatal(err)
	}
	proof := model.LedgerLegacySettlementWaiverBatch{Items: items, ExpectedTotalMicrousd: 17, MaxTotalMicrousd: 17, Reason: "platform assumes approved additional debit", Actor: "cli-fixture", EvidenceSHA256: strings.Repeat("a", 64), At: time.Now().UTC(), WritersStopped: true}
	encoded, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	proofPath := filepath.Join(t.TempDir(), "waiver.json")
	if err := os.WriteFile(proofPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSettlementWaiveCommandRetriesOriginalProofBeforeBootstrap$")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "LAZU_INITIAL_WAIVER_CHILD=1", "LAZU_INITIAL_WAIVER_SPOOL="+spool, "LAZU_INITIAL_WAIVER_PROOF="+proofPath, "LAZU_MIGRATION_SOURCE_URL="+dsn, "REDIS_CONN_STRING="+redisURL, "RELAY_EVENT_STREAM="+stream, "RELAY_EVENT_GROUP="+common.RelayEventGroup, "APP_ENV=development")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI waiver attempt %d failed: %v\n%s", attempt, err, output)
		}
		var count int64
		if err := db.Model(&model.LedgerLegacySettlementWaiver{}).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("CLI retry duplicated disposition: %d %v", count, err)
		}
		if err := db.Model(&model.LedgerContinuityState{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("waiver invented bootstrap state: %d %v", count, err)
		}
		var current model.SettlementFailure
		if err := db.First(&current, original.ID).Error; err != nil || !reflect.DeepEqual(current, original) {
			t.Fatal("CLI waiver changed original failure facts")
		}
	}
}
