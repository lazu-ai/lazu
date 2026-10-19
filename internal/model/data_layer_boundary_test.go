package model

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
)

type postgresBoundaryTestDialect struct{ gorm.Dialector }

func (postgresBoundaryTestDialect) Name() string { return "postgres" }

func boundaryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormsqlite.Open(filepath.Join(t.TempDir(), "boundary.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	oldCore, oldTelemetry := CoreDB(), ConfiguredTelemetryDB()
	SetCoreDB(db)
	SetTelemetryDB(db)
	t.Cleanup(func() { SetCoreDB(oldCore); SetTelemetryDB(oldTelemetry); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return db
}

func TestPostgresTelemetryOutageNeverFallsBackToSQL(t *testing.T) {
	db := boundaryTestDB(t)
	db.Dialector = postgresBoundaryTestDialect{db.Dialector}
	oldRedis, oldEnabled, oldQueue := common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled
	common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled = nil, false, false
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled = oldRedis, oldEnabled, oldQueue
	})
	var queries atomic.Int64
	count := func(*gorm.DB) { queries.Add(1) }
	_ = db.Callback().Create().Before("gorm:create").Register("boundary:create", count)
	_ = db.Callback().Query().Before("gorm:query").Register("boundary:query", count)
	_ = db.Callback().Raw().Before("gorm:raw").Register("boundary:raw", count)
	_ = db.Callback().Row().Before("gorm:row").Register("boundary:row", count)
	log := &Log{RequestID: "queue-down", Type: LogTypeConsume, CreatedAt: time.Now().Unix()}
	if err := publishConsumeLogEvent(log); err == nil {
		t.Fatal("unavailable queue was accepted")
	}
	outcome := &RoutingOutcome{RequestID: log.RequestID, DecisionID: 1, OutcomeAt: time.Now()}
	if err := RecordRoutingOutcome(outcome); err == nil {
		t.Fatal("outcome queue failure was ignored")
	}
	if err := RecordRoutingOutcomeSync(outcome); err == nil {
		t.Fatal("durable outcome queue failure was ignored")
	}
	if err := RecordRoutingAttemptOutcome(&RoutingAttemptOutcome{RequestID: log.RequestID}); err == nil {
		t.Fatal("attempt queue failure was ignored")
	}
	RecordRoutingDecision(&RoutingDecision{RequestID: log.RequestID})
	c, _ := gin.CreateTestContext(nil)
	MarkRelayResultFlushExpected(c)
	queueConsumeLogForRelayResult(c, log)
	if _, present, err := BuildRelayResultEnvelope(c); err != nil || !present {
		t.Fatalf("spool lost full envelope during outage: present=%v err=%v", present, err)
	}
	if err := PersistManualRoutingOutcome(c, outcome); err == nil {
		t.Fatal("manual submission queue failure was ignored")
	}
	if !FlushRelayResultEvent(c, outcome) {
		t.Fatal("relay flush was not handled")
	}
	if queries.Load() != 0 {
		t.Fatalf("API telemetry issued %d SQL operations during queue outage", queries.Load())
	}
}

func TestPostgresPricingRegistryOwnsNegativeLookups(t *testing.T) {
	db := boundaryTestDB(t)
	db.Dialector = postgresBoundaryTestDialect{db.Dialector}
	oldSnapshot, oldFlag := activePricingSnapshot.Load(), common.PricingSnapshotEnabled
	common.PricingSnapshotEnabled = false
	t.Cleanup(func() {
		activePricingSnapshot = atomic.Value{}
		if oldSnapshot != nil {
			activePricingSnapshot.Store(oldSnapshot)
		}
		common.PricingSnapshotEnabled = oldFlag
	})
	installPricingRegistry(pricingRegistry{SellPrices: []ModelSellPrice{{ModelName: "cached-model", ChannelGroup: ChannelLaneDirect, Active: true}}, LoadedAt: time.Now()})
	var queries atomic.Int64
	_ = db.Callback().Query().Before("gorm:query").Register("boundary:query", func(*gorm.DB) { queries.Add(1) })
	_ = db.Callback().Row().Before("gorm:row").Register("boundary:row", func(*gorm.DB) { queries.Add(1) })
	if _, ok := GetActiveModelSellPrice("cached-model", ChannelLaneDirect); !ok {
		t.Fatal("PG mandatory snapshot was disabled by compatibility flag")
	}
	if _, ok := GetActiveModelSellPrice("missing-model", ChannelLaneDirect); ok {
		t.Fatal("absent model received a price")
	}
	if queries.Load() != 0 {
		t.Fatal("negative pricing lookup queried Core")
	}
}

func TestPricingRegistryDelayedPublisherCannotOverwriteAdminIntegration(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("isolated TEST_REDIS_URL required")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	db := boundaryTestDB(t)
	db.Dialector = postgresBoundaryTestDialect{db.Dialector}
	oldRedis, oldSnapshot := common.RDB, activePricingSnapshot.Load()
	common.RDB = client
	ctx := context.Background()
	t.Cleanup(func() {
		client.Del(ctx, pricingRegistryRedisKey, pricingRegistryRedisKey+":revision")
		client.Close()
		common.RDB = oldRedis
		activePricingSnapshot = atomic.Value{}
		if oldSnapshot != nil {
			activePricingSnapshot.Store(oldSnapshot)
		}
	})
	oldRevision, err := beginControlSnapshot(ctx, pricingRegistryRedisKey)
	if err != nil {
		t.Fatal(err)
	}
	newRevision, err := beginControlSnapshot(ctx, pricingRegistryRedisKey)
	if err != nil {
		t.Fatal(err)
	}
	registry := func(revision, version int64, at time.Time) pricingRegistry {
		return pricingRegistry{Revision: revision, LoadedAt: at, SellPrices: []ModelSellPrice{{ModelName: "race-model", ChannelGroup: ChannelLaneDirect, PricingVersion: version}}}
	}
	if err := publishControlSnapshot(ctx, pricingRegistryRedisKey, registry(newRevision, 2, time.Now()), newRevision); err != nil {
		t.Fatal(err)
	}
	// The delayed old SQL read finishes later. Its timestamp cannot authorize
	// either a shared overwrite or a stale local installation.
	if err := publishControlSnapshot(ctx, pricingRegistryRedisKey, registry(oldRevision, 1, time.Now().Add(time.Hour)), oldRevision); err != nil {
		t.Fatal(err)
	}
	if err := refreshPricingSnapshotFromRedis(); err != nil {
		t.Fatal(err)
	}
	price, ok := GetActiveModelSellPrice("race-model", ChannelLaneDirect)
	if !ok || price.PricingVersion != 2 {
		t.Fatalf("stale jobs price replaced admin commit: %+v", price)
	}
}

func TestConsumeEnvelopePreservesFactsAndLegacyChannelName(t *testing.T) {
	row := &Log{RequestID: "media-request", Type: LogTypeConsume, CreatedAt: time.Now().Unix(), ChannelName: "provider-name"}
	raw, err := BuildConsumeLogRelayEnvelope(row)
	if err != nil {
		t.Fatal(err)
	}
	var event relayResultEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil || event.ConsumeLog == nil {
		t.Fatalf("invalid consume envelope: %v", err)
	}
	foldRelayRoutingIntoLog(row, relayResultEvent{RoutingOutcome: &RoutingOutcome{RequestID: row.RequestID}})
	if row.ChannelName != "provider-name" || row.PublicID == "" {
		t.Fatal("legacy routing payload erased consume facts")
	}
}

func TestLedgerBatchUnlimitedCountersAndBudgetAlertsWithoutChargeRows(t *testing.T) {
	db := boundaryTestDB(t)
	models := []interface{}{&User{}, &Token{}, &WalletLedger{}, &ProjectSpend{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetAlert{}}
	models = append(models, RedisLedgerModels()...)
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&User{Id: 1, Username: "batch-payer", Quota: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&Token{Id: 1, UserId: 1, Key: "test-only", UnlimitedQuota: true, RemainQuota: 500}).Error; err != nil {
		t.Fatal(err)
	}
	for _, budget := range []SpendBudget{
		{ID: 1, ProjectID: 1, Scope: SpendBudgetScopeToken, TokenID: 1, Period: SpendBudgetPeriodDay, LimitMicrousd: 100, WarningThresholdPct: 80, BurstMultiplier: 2, Enabled: true},
		{ID: 2, Scope: SpendBudgetScopeUser, UserID: 1, Period: SpendBudgetPeriodDay, LimitMicrousd: 100, WarningThresholdPct: 80, BurstMultiplier: 2, Enabled: true},
	} {
		if err := db.Create(&budget).Error; err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	event := func(id, kind string, amount int64, when time.Time) ledger.Event {
		return ledger.Event{Input: ledger.Input{RequestID: id, PayerID: 1, ActorID: 1, TokenID: 1, ProjectID: 1, Unlimited: true, Amount: amount, OccurredAt: when}, OperationID: id, Kind: kind}
	}
	storage := redisLedgerStorage{}
	if err := storage.Apply(context.Background(), []ledger.Event{event("previous", "settle", 30, at.AddDate(0, 0, -1))}); err != nil {
		t.Fatal(err)
	}
	batch := []ledger.Event{event("consume", "settle", 90, at), event("refund", "refund", -20, at.Add(time.Second))}
	if err := storage.Apply(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	if err := storage.Apply(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	var token Token
	if err := db.First(&token, 1).Error; err != nil {
		t.Fatal(err)
	}
	if token.RemainQuota != 500 || token.UsedQuota != 100 || token.LastUsedAt != at.Add(time.Second).Unix() {
		t.Fatalf("unlimited counter state: remaining=%d used=%d last=%d", token.RemainQuota, token.UsedQuota, token.LastUsedAt)
	}
	var charges, alerts int64
	db.Model(&SpendBudgetCharge{}).Count(&charges)
	db.Model(&SpendBudgetAlert{}).Count(&alerts)
	if charges != 0 || alerts != 4 {
		t.Fatalf("per-request charges=%d alerts=%d, want0,4", charges, alerts)
	}
	var warning SpendBudgetAlert
	if err := db.Where("budget_id = ? AND alert_type = ?", 1, SpendBudgetAlertThreshold).First(&warning).Error; err != nil {
		t.Fatal(err)
	}
	if warning.SpentMicrousd != 90 {
		t.Fatalf("same-batch refund hid threshold crossing: %d", warning.SpentMicrousd)
	}
}

func TestPaymentDocumentClaimExpiresWithoutHoldingRemoteLock(t *testing.T) {
	db := boundaryTestDB(t)
	if err := db.AutoMigrate(&TopUp{}); err != nil {
		t.Fatal(err)
	}
	order := TopUp{ProjectID: 1, PublicID: "document-claim", TradeNo: "document-claim", StripeCheckoutSessionID: "cs_fixture", Status: common.TopUpStatusSuccess, InvoiceStatus: "pending", CompleteTime: 1000}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	for i, at := range []int64{2000, 2001, 2300} {
		claimed, err := ClaimProjectTopUpsForDocumentReconciliation(10, 999, at)
		want := 1
		if i == 1 {
			want = 0
		}
		if err != nil || len(claimed) != want {
			t.Fatalf("claim at %d returned%d: %v", at, len(claimed), err)
		}
	}
}
