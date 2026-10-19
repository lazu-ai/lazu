package model

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func TestLedgerContinuityExplicitBootstrapAndLossRecoveryIntegration(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("isolated TEST_REDIS_URL required")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	oldRedis, oldEngine := common.RDB, GatewayLedger()
	oldStream, oldGroup, oldRedisEnabled, oldQueueEnabled := common.RelayEventStream, common.RelayEventGroup, common.RedisEnabled, common.RelayEventQueueEnabled
	common.RelayEventStream, common.RelayEventGroup = ledger.Prefix+"test:continuity-telemetry", "continuity-test"
	common.RedisEnabled, common.RelayEventQueueEnabled = true, true
	common.RDB = client
	gatewayLedger.Store(nil)
	clear := func() {
		keys, _ := client.Keys(ctx, ledger.Prefix+"*").Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
	}
	clear()
	t.Cleanup(func() {
		clear()
		client.Close()
		common.RDB = oldRedis
		gatewayLedger.Store(oldEngine)
		common.RelayEventStream, common.RelayEventGroup, common.RedisEnabled, common.RelayEventQueueEnabled = oldStream, oldGroup, oldRedisEnabled, oldQueueEnabled
	})
	db, logs := newLedgerTestDBs(t)
	if err := logs.AutoMigrate(&TelemetryFinancialReceipt{}); err != nil {
		t.Fatal(err)
	}
	models := append(RedisLedgerModels(), &ProjectSpend{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetAlert{}, &MediaJob{})
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	unbound := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, unbound); err == nil {
		t.Fatal("uninitialized runtime accepted")
	}
	state, err := BootstrapLedgerContinuity(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindLedgerContinuity(ctx, db, unbound); err != nil {
		t.Fatal(err)
	}
	if again, err := BootstrapLedgerContinuity(ctx, db); err != nil || again.Epoch != state.Epoch {
		t.Fatalf("bootstrap not idempotent: %+v %v", again, err)
	}
	if err := MarkLedgerDurabilityFailure(ctx, unbound.Continuity); err != nil {
		t.Fatal(err)
	}
	if err := MarkLedgerDurabilityFailure(ctx, unbound.Continuity); err != nil {
		t.Fatalf("marker retry: %v", err)
	}
	if err := BindLedgerContinuity(ctx, db, ledger.New(client, redisLedgerStorage{})); err == nil {
		t.Fatal("restart admitted after durability failure")
	}
	// The following explicit recovery also verifies recovery_required may enter
	// the stopped-writer proof workflow after complete Redis loss.
	user := User{Id: 88001, Username: "continuity-fixture", Quota: 1000}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	input := ledger.Input{RequestID: "continuity-lost", PayerID: user.Id, ActorID: user.Id, Unlimited: true, Amount: 100, OccurredAt: time.Now().UTC()}
	if err := unbound.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	unbound.SpoolDir = t.TempDir()
	if err := unbound.PersistSettlement(input, 80, "settle"); err != nil {
		t.Fatal(err)
	}
	// Lose the complete financial namespace. Core and every persistent spool live.
	clear()
	if err := unbound.Reserve(ctx, input); !errors.Is(err, ledger.ErrContinuity) {
		t.Fatalf("loss admitted: %v", err)
	}
	if _, err := BootstrapLedgerContinuity(ctx, db); err == nil {
		t.Fatal("bootstrap silently recovered lost history")
	}
	dirs := []string{unbound.SpoolDir}
	proof := LedgerRecoveryProof{Evidence: "fixture stopped all writers; classified accepted request from persistent fsync spool", Writers: []LedgerRecoveryWriter{{ID: "fixture-api", Role: "api", SpoolDirectory: dirs[0], StoppedAt: time.Now().Add(-time.Second)}}, AcceptedRequestsClassified: true}
	if _, err := RecoverLedgerContinuity(ctx, db, dirs, false, proof); err == nil {
		t.Fatal("missing stopped-writer attestation accepted")
	}
	if _, err := RecoverLedgerContinuity(ctx, db, dirs, true, proof); err == nil {
		t.Fatal("missing maintenance accepted")
	}
	client.Set(ctx, ledger.Prefix+"maintenance", "operator-recovery", 0)
	client.Set(ctx, ledger.Prefix+"nodes:live", "api", time.Minute)
	if _, err := RecoverLedgerContinuity(ctx, db, dirs, true, proof); err == nil {
		t.Fatal("live writer accepted")
	}
	client.Del(ctx, ledger.Prefix+"nodes:live")
	recovered, err := RecoverLedgerContinuity(ctx, db, dirs, true, proof)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Epoch != state.Epoch || recovered.AdmissionGeneration != state.AdmissionGeneration+1 {
		t.Fatalf("financial epoch rotated: %+v", recovered)
	}
	if err := unbound.Reserve(ctx, input); !errors.Is(err, ledger.ErrContinuity) {
		t.Fatalf("old process resumed: %v", err)
	}
	var actual User
	if err := db.First(&actual, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	if actual.Quota != 920 {
		t.Fatalf("known upstream result lost: %d", actual.Quota)
	}
	if err := ResumeLedgerContinuity(ctx, db, nil, true); err == nil {
		t.Fatal("missing spool inventory accepted")
	}
	if err := ResumeLedgerContinuity(ctx, db, dirs, true); err == nil {
		t.Fatal("resume accepted undrained recovered receipts")
	}
	if _, err := DrainRelayEvents(ctx, "continuity-recovery", 10); err != nil {
		t.Fatal(err)
	}
	if err := ResumeLedgerContinuity(ctx, db, dirs, true); err != nil {
		t.Fatal(err)
	}
	fresh := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, fresh); err != nil {
		t.Fatal(err)
	}
	if err := fresh.Reserve(ctx, ledger.Input{RequestID: "continuity-new", PayerID: user.Id, Unlimited: true, Amount: 10, OccurredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// Original financial identity remains immutable across a second replay.
	if err := fresh.Settle(ctx, input, 80); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&actual, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	if actual.Quota != 920 {
		t.Fatalf("replay double debit: %d", actual.Quota)
	}
	t.Run("legacy-historical-precharge-requires-complete-classification", func(t *testing.T) {
		clear()
		oldDB, _ := newLedgerTestDBs(t)
		if err := oldDB.AutoMigrate(RedisLedgerModels()...); err != nil {
			t.Fatal(err)
		}
		historical := WalletLedger{UserID: 42, DeltaMicrousd: billing.MicroUSD(-100), Kind: WalletLedgerKindSpend, RefType: "relay_preconsume", RefID: "historical-completed", CreatedAt: time.Now().Add(-time.Hour)}
		if err := oldDB.Create(&historical).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := BootstrapLedgerContinuity(ctx, oldDB); err == nil {
			t.Fatal("unclassified historical precharge accepted")
		}
		classified := proof
		if _, err := BootstrapLedgerContinuityWithProof(ctx, oldDB, dirs, true, &classified); err == nil {
			t.Fatal("incomplete classification accepted")
		}
		classified.LegacyPrecharges = []LedgerLegacyPrechargeClassification{{LedgerID: historical.ID, AmountMicrousd: 99, Status: "settled", Evidence: "verified prior-release receipt"}}
		if _, err := BootstrapLedgerContinuityWithProof(ctx, oldDB, dirs, true, &classified); err == nil {
			t.Fatal("wrong amount classification accepted")
		}
		classified.LegacyPrecharges[0].AmountMicrousd = 100
		classified.LegacyPrecharges[0].Status = "unresolved"
		if _, err := BootstrapLedgerContinuityWithProof(ctx, oldDB, dirs, true, &classified); err == nil {
			t.Fatal("unresolved classification accepted")
		}
		classified.LegacyPrecharges[0].Status = "settled"
		classified.Evidence = "verified every previous-release request was settled or refunded before cutover"
		if _, err := BootstrapLedgerContinuityWithProof(ctx, oldDB, dirs, true, &classified); err != nil {
			t.Fatal(err)
		}
		var preserved WalletLedger
		if err := oldDB.First(&preserved, historical.ID).Error; err != nil {
			t.Fatal(err)
		}
		if preserved.DeltaMicrousd.Int64() != -100 {
			t.Fatal("bootstrap rewrote legacy financial history")
		}
	})

}

func TestAutoBootstrapOnlyAcceptsEmptyInstallationIntegration(t *testing.T) {
	for _, scenario := range []string{"empty", "existing-user", "existing-spool", "existing-redis", "validate"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("LAZU_MIGRATION_MODE", "auto")
			client := identityRedisTest(t)
			ctx := context.Background()
			clear := func() {
				keys, _ := client.Keys(ctx, ledger.Prefix+"*").Result()
				if len(keys) > 0 {
					client.Del(ctx, keys...)
				}
			}
			clear()
			t.Cleanup(clear)
			db, _ := newLedgerTestDBs(t)
			if err := db.AutoMigrate(append(RedisLedgerModels(), &SpendBudget{}, &SpendBudgetCharge{}, &SettlementOutbox{})...); err != nil {
				t.Fatal(err)
			}
			engine := ledger.New(client, redisLedgerStorage{})
			engine.SpoolDir = t.TempDir()
			switch scenario {
			case "existing-user":
				if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&User{Id: 1, Username: "existing"}).Error; err != nil {
					t.Fatal(err)
				}
			case "existing-spool":
				if err := engine.PersistSettlement(ledger.Input{RequestID: "existing", PayerID: 1, Amount: 1}, 1, "settle"); err != nil {
					t.Fatal(err)
				}
			case "existing-redis":
				client.Set(ctx, ledger.Prefix+"wallet:1", "retained", 0)
			case "validate":
				t.Setenv("LAZU_MIGRATION_MODE", "validate")
			}
			err := bootstrapEmptyGatewayLedger(ctx, db, engine)
			if scenario == "empty" {
				if err != nil {
					t.Fatal(err)
				}
				if err := BindLedgerContinuity(ctx, db, engine); err != nil {
					t.Fatal(err)
				}
				client.Del(ctx, ledger.ContinuityKey)
				if err := bootstrapEmptyGatewayLedger(ctx, db, engine); err != nil {
					t.Fatal(err)
				}
				if err := BindLedgerContinuity(ctx, db, engine); err == nil {
					t.Fatal("automatically recovered lost Redis continuity")
				}
			} else {
				if scenario != "validate" && err == nil {
					t.Fatal("automatically bootstrapped existing evidence")
				}
				var n int64
				if err := db.Model(&LedgerContinuityState{}).Count(&n).Error; err != nil || n != 0 {
					t.Fatalf("created continuity for %s: %d %v", scenario, n, err)
				}
			}
		})
	}
}

// These fixtures use their own logical Redis DB and never flush a shared DB.
// Requiring an empty DB establishes that the financial keys removed on cleanup
// were created by this fixture, rather than by another test or a running app.
func ledgerOpeningContinuityFixture(t *testing.T, withWaiver bool) (*gorm.DB, *redis.Client, []string, LedgerRecoveryProof) {
	t.Helper()
	endpoint := os.Getenv("TEST_LEDGER_OPENING_REDIS_URL")
	if endpoint == "" {
		endpoint = os.Getenv("TEST_REDIS_URL")
	}
	if endpoint == "" {
		t.Skip("isolated Redis test endpoint required for opening continuity")
	}
	options, err := redis.ParseURL(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	options.DB = 10
	client := redis.NewClient(options)
	ctx := context.Background()
	count, err := client.DBSize(ctx).Result()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	if count != 0 {
		client.Close()
		t.Fatal("opening continuity fixture requires empty dedicated Redis DB 10; refusing to delete existing keys")
	}
	oldRedis, oldEngine := common.RDB, GatewayLedger()
	oldStream, oldGroup := common.RelayEventStream, common.RelayEventGroup
	oldRedisEnabled, oldQueueEnabled := common.RedisEnabled, common.RelayEventQueueEnabled
	common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled = client, true, true
	common.RelayEventStream, common.RelayEventGroup = ledger.Prefix+"test:opening-telemetry", "opening-fixture"
	gatewayLedger.Store(nil)
	t.Cleanup(func() {
		keys, err := client.Keys(ctx, ledger.Prefix+"*").Result()
		if err != nil {
			t.Errorf("opening fixture cleanup inventory: %v", err)
		} else if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Errorf("opening fixture cleanup: %v", err)
			}
		}
		_ = client.Close()
		common.RDB, common.RelayEventStream, common.RelayEventGroup = oldRedis, oldStream, oldGroup
		common.RedisEnabled, common.RelayEventQueueEnabled = oldRedisEnabled, oldQueueEnabled
		gatewayLedger.Store(oldEngine)
	})
	db, logs := newLedgerTestDBs(t)
	ledgerOpeningTestSchema(t, db)
	if err := db.AutoMigrate(RedisLedgerModels()...); err != nil {
		t.Fatal(err)
	}
	if err := logs.AutoMigrate(&TelemetryFinancialReceipt{}); err != nil {
		t.Fatal(err)
	}
	ledgerOpeningTestSeed(t, db)
	if err := db.Exec("UPDATE users SET quota = 1000 WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if withWaiver {
		legacyWaiverSource(t, db, 1)
		if _, err := ApplyLegacySettlementWaivers(ctx, db, legacyWaiverBatch(t, db, 1)); err != nil {
			t.Fatal(err)
		}
	}
	dirs := []string{t.TempDir()}
	proof := LedgerRecoveryProof{Evidence: "all original accepted requests classified; unknown historical precharges retained without replay", AcceptedRequestsClassified: true,
		Writers: []LedgerRecoveryWriter{{ID: "opening-fixture-api", Role: "api", SpoolDirectory: dirs[0], StoppedAt: time.Now().Add(-time.Second)}},
		Opening: &LedgerOpeningRequest{OperatorEvidence: "initial authority is stopped-writer Core SQL snapshot; retained backup", WritersStopped: true}}
	if err := client.Set(ctx, ledger.Prefix+"maintenance", "opening-fixture", 0).Err(); err != nil {
		t.Fatal(err)
	}
	return db, client, dirs, proof
}

func ledgerOpeningContinuityBootstrap(t *testing.T, db *gorm.DB, dirs []string, proof LedgerRecoveryProof) LedgerContinuityState {
	t.Helper()
	state, err := BootstrapLedgerContinuityWithProof(context.Background(), db, dirs, true, &proof)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "active" || state.OpeningID != 1 || state.OpeningSHA256 == "" {
		t.Fatalf("opening was not permanently bound: %+v", state)
	}
	manifest, err := LoadLedgerOpening(context.Background(), db, LedgerOpeningBinding{state.OpeningID, state.OpeningSHA256})
	if err != nil || manifest.Epoch != state.Epoch || manifest.LegacyPrechargeCount != 1 {
		t.Fatalf("invalid opening continuity: %+v, %v", manifest, err)
	}
	return state
}

func TestLedgerContinuityOpeningBootstrapAndActiveFullAuditIntegration(t *testing.T) {
	db, client, dirs, proof := ledgerOpeningContinuityFixture(t, true)
	ctx := context.Background()
	state := ledgerOpeningContinuityBootstrap(t, db, dirs, proof)
	engine := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	if again, err := BootstrapLedgerContinuityWithProof(ctx, db, dirs, true, &proof); err != nil || again.Epoch != state.Epoch || again.OpeningSHA256 != state.OpeningSHA256 {
		t.Fatalf("active opening bootstrap not idempotent: %+v, %v", again, err)
	}
	if err := db.Exec("UPDATE users SET quota = quota - 10, credit_seq = credit_seq + 1 WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE tokens SET remain_quota = 700 WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapLedgerContinuityWithProof(ctx, db, dirs, true, &proof); err != nil {
		t.Fatalf("active full audit compared mutable balances to opening: %v", err)
	}
	if err := db.Exec("UPDATE wallet_ledgers SET metadata = ? WHERE id = 1", "modified-history").Error; err != nil {
		t.Fatal(err)
	}
	if err := BindLedgerContinuity(ctx, db, ledger.New(client, redisLedgerStorage{})); err != nil {
		t.Fatalf("startup unexpectedly performed an unbounded history scan: %v", err)
	}
	if _, err := BootstrapLedgerContinuityWithProof(ctx, db, dirs, true, &proof); err == nil {
		t.Fatal("active bootstrap skipped full historical source verification")
	}
	if err := ResumeLedgerContinuity(ctx, db, dirs, true); err == nil {
		t.Fatal("resume skipped full historical source verification")
	}
	var classifications int64
	if err := db.Model(&LedgerLegacyPrechargeRecord{}).Count(&classifications).Error; err != nil || classifications != 0 {
		t.Fatal("opening fabricated terminal classifications")
	}
}

func TestLedgerContinuityOpeningRecoveryKeepsPostCutoverProofStrictIntegration(t *testing.T) {
	db, client, dirs, initial := ledgerOpeningContinuityFixture(t, false)
	ctx := context.Background()
	state := ledgerOpeningContinuityBootstrap(t, db, dirs, initial)
	if _, err := RecoverLedgerContinuity(ctx, db, dirs, true, initial); err == nil || !strings.Contains(err.Error(), "initial opening evidence") {
		t.Fatalf("initial opening reused for later accepted work: %v", err)
	}
	proof := initial
	proof.Opening = nil
	proof.AcceptedRequestsClassified = false
	if _, err := RecoverLedgerContinuity(ctx, db, dirs, true, proof); err == nil {
		t.Fatal("post-cutover unclassified requests were waived by historical opening")
	}
	proof.AcceptedRequestsClassified = true
	engine := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	if err := ResumeLedgerContinuity(ctx, db, dirs, true); err != nil {
		t.Fatal(err)
	}
	input := ledger.Input{RequestID: "post-opening-request", PayerID: 1, ActorID: 1, Unlimited: true, Amount: 10, OccurredAt: time.Now().UTC()}
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, ledger.Prefix+"maintenance", "post-opening-recovery", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverLedgerContinuity(ctx, db, dirs, true, proof); err == nil || !strings.Contains(err.Error(), "lacks an explicit result") {
		t.Fatalf("surviving new reservation accepted without classification: %v", err)
	}
	proof.AbandonedRequestIDs = []string{input.RequestID}
	recovered, err := RecoverLedgerContinuity(ctx, db, dirs, true, proof)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Epoch != state.Epoch || recovered.AdmissionGeneration != state.AdmissionGeneration+1 || recovered.OpeningID != state.OpeningID || recovered.OpeningSHA256 != state.OpeningSHA256 {
		t.Fatalf("recovery rotated or lost the permanent opening: %+v", recovered)
	}
	if strings.Contains(recovered.RecoveryProof, `"opening"`) {
		t.Fatal("initial opening was carried forward as post-cutover recovery proof")
	}
	var user User
	if err := db.First(&user, 1).Error; err != nil || user.Quota != 1000 {
		t.Fatalf("explicit abandonment changed authoritative wallet: %d %v", user.Quota, err)
	}
	if err := ResumeLedgerContinuity(ctx, db, dirs, true); err != nil {
		t.Fatal(err)
	}
	if exists, err := client.Exists(ctx, ledger.Prefix+"maintenance").Result(); err != nil || exists != 0 {
		t.Fatal("successful strict recovery could not resume")
	}
}

func TestLedgerContinuityOpeningWaivedReleaseAndAbandonCannotReplayIntegration(t *testing.T) {
	for _, scenario := range []string{"settle", "release", "abandon"} {
		t.Run(scenario, func(t *testing.T) {
			db, _, dirs, proof := ledgerOpeningContinuityFixture(t, true)
			state := ledgerOpeningContinuityBootstrap(t, db, dirs, proof)
			proof.Opening = nil
			if scenario == "abandon" {
				proof.AbandonedRequestIDs = []string{"waiver-request-1"}
			} else {
				amount := int64(0)
				if scenario == "settle" {
					amount = 17
				}
				proof.Intents = []ledger.Event{{Input: ledger.Input{RequestID: "waiver-request-1", PayerID: 41, ActorID: 53, ProjectID: 47, TokenID: 43, Unlimited: true, Amount: amount, OccurredAt: time.Now().UTC()}, OperationID: "waiver-request-1", Kind: scenario}}
			}
			if _, err := RecoverLedgerContinuity(context.Background(), db, dirs, true, proof); !errors.Is(err, ErrLegacySettlementWaived) {
				t.Fatalf("waived %s was allowed to replay: %v", scenario, err)
			}
			var current LedgerContinuityState
			if err := db.First(&current, 1).Error; err != nil || current.AdmissionGeneration != state.AdmissionGeneration || current.Status != "active" {
				t.Fatalf("rejected waiver replay changed continuity: %+v %v", current, err)
			}
			var operations int64
			if err := db.Model(&RedisLedgerOperation{}).Count(&operations).Error; err != nil || operations != 0 {
				t.Fatal("rejected waived identity created a permanent paid operation")
			}
		})
	}
}

func TestLedgerContinuityOpeningRejectsUnsafeInitialProofIntegration(t *testing.T) {
	for _, scenario := range []string{"unclassified", "intent", "abandon", "fake terminal", "no maintenance", "telemetry", "opening writer attestation", "future opening", "empty opening evidence", "effective SQL hold"} {
		t.Run(scenario, func(t *testing.T) {
			db, client, dirs, proof := ledgerOpeningContinuityFixture(t, false)
			ctx := context.Background()
			switch scenario {
			case "unclassified":
				proof.AcceptedRequestsClassified = false
			case "intent":
				proof.Intents = []ledger.Event{{Input: ledger.Input{RequestID: "old-unknown", PayerID: 1, Amount: 0}, OperationID: "old-unknown", Kind: "release"}}
			case "abandon":
				proof.AbandonedRequestIDs = []string{"old-unknown"}
			case "fake terminal":
				proof.LegacyPrecharges = []LedgerLegacyPrechargeClassification{{LedgerID: 1, AmountMicrousd: 100, Status: "settled", Evidence: "unsupported terminal claim"}}
			case "no maintenance":
				if err := client.Del(ctx, ledger.Prefix+"maintenance").Err(); err != nil {
					t.Fatal(err)
				}
			case "telemetry":
				if err := client.XAdd(ctx, &redis.XAddArgs{Stream: common.RelayEventStream, Values: map[string]any{"payload": "unpersisted"}}).Err(); err != nil {
					t.Fatal(err)
				}
			case "opening writer attestation":
				proof.Opening.WritersStopped = false
			case "future opening":
				proof.Opening.CapturedAt = time.Now().Add(time.Hour)
			case "empty opening evidence":
				proof.Opening.OperatorEvidence = ""
			case "effective SQL hold":
				if err := db.Create(&SpendBudgetReservation{BudgetID: 1, RequestID: "active-cutover-hold", ReservedMicrousd: 1, ExpiresAt: time.Now().Add(time.Hour).Unix()}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := BootstrapLedgerContinuityWithProof(ctx, db, dirs, true, &proof); err == nil {
				t.Fatal("unsafe initial opening proof accepted")
			}
			for _, model := range []any{&LedgerContinuityState{}, &LedgerOpeningManifest{}, &RedisLedgerOperation{}} {
				var count int64
				if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("rejected initial proof changed financial state: %d %v", count, err)
				}
			}
		})
	}
}

func TestLedgerContinuityOpeningKnownPostCutoverSettlementRecoveryIntegration(t *testing.T) {
	db, client, dirs, proof := ledgerOpeningContinuityFixture(t, false)
	ctx := context.Background()
	initial := ledgerOpeningContinuityBootstrap(t, db, dirs, proof)
	engine := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	if err := ResumeLedgerContinuity(ctx, db, dirs, true); err != nil {
		t.Fatal(err)
	}
	input := ledger.Input{RequestID: "known-post-cutover", PayerID: 1, ActorID: 1, Unlimited: true, Amount: 100, OccurredAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Amount = 80
	proof.Opening = nil
	proof.Intents = []ledger.Event{{Input: input, OperationID: input.RequestID, Kind: "settle"}}
	for attempt := 0; attempt < 2; attempt++ {
		if err := client.Set(ctx, ledger.Prefix+"maintenance", "known-result-recovery", 0).Err(); err != nil {
			t.Fatal(err)
		}
		state, err := RecoverLedgerContinuity(ctx, db, dirs, true, proof)
		if err != nil {
			t.Fatal(err)
		}
		if state.OpeningSHA256 != initial.OpeningSHA256 || state.OpeningID != initial.OpeningID || state.Epoch != initial.Epoch {
			t.Fatal("known post-cutover recovery rewrote the initial opening")
		}
		var wallet User
		if err := db.First(&wallet, 1).Error; err != nil || wallet.Quota != 920 {
			t.Fatalf("known recovery lost or double-debited result: quota=%d err=%v", wallet.Quota, err)
		}
		var operations int64
		if err := db.Model(&RedisLedgerOperation{}).Count(&operations).Error; err != nil || operations != 1 {
			t.Fatalf("permanent recovery dedupe failed: count=%d err=%v", operations, err)
		}
		if attempt == 0 {
			if err := ResumeLedgerContinuity(ctx, db, dirs, true); err == nil {
				t.Fatal("known-result recovery resumed before telemetry receipt drain")
			}
		}
		if _, err := DrainRelayEvents(ctx, "opening-known-recovery", 10); err != nil {
			t.Fatal(err)
		}
		if err := ResumeLedgerContinuity(ctx, db, dirs, true); err != nil {
			t.Fatal(err)
		}
	}
	var receipts int64
	if err := TelemetryDB().Model(&TelemetryFinancialReceipt{}).Count(&receipts).Error; err != nil || receipts != 1 {
		t.Fatalf("financial receipt replay failed to dedupe: %d %v", receipts, err)
	}
}

func TestLedgerContinuityOpeningStartupBindingRejectsTamperingIntegration(t *testing.T) {
	for _, scenario := range []string{"missing id", "missing digest", "different digest", "different epoch", "manifest changed"} {
		t.Run(scenario, func(t *testing.T) {
			db, client, dirs, proof := ledgerOpeningContinuityFixture(t, false)
			ledgerOpeningContinuityBootstrap(t, db, dirs, proof)
			var err error
			switch scenario {
			case "missing id":
				err = db.Exec("UPDATE ledger_continuity_states SET opening_id = 0").Error
			case "missing digest":
				err = db.Exec("UPDATE ledger_continuity_states SET opening_sha256 = ''").Error
			case "different digest":
				err = db.Exec("UPDATE ledger_continuity_states SET opening_sha256 = ?", strings.Repeat("0", 64)).Error
			case "different epoch":
				err = db.Exec("UPDATE ledger_continuity_states SET epoch = ?", "changed-continuity-epoch").Error
			case "manifest changed":
				err = db.Exec("UPDATE ledger_opening_manifests SET operator_evidence = ?", "changed-opening-evidence").Error
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := BindLedgerContinuity(context.Background(), db, ledger.New(client, redisLedgerStorage{})); err == nil {
				t.Fatal("startup admitted a broken permanent opening binding")
			}
		})
	}
}

func TestLedgerContinuityOpeningUsesExplicitFirstEpochIntegration(t *testing.T) {
	db, _, dirs, proof := ledgerOpeningContinuityFixture(t, false)
	proof.Opening.Epoch = "explicit-first-opening-epoch"
	state := ledgerOpeningContinuityBootstrap(t, db, dirs, proof)
	if state.Epoch != proof.Opening.Epoch {
		t.Fatal("bootstrap replaced the explicit opening epoch")
	}
}
