package model

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func TestDecisionRefundRedisUsesOriginalFactsAndPreservesProofIntegration(t *testing.T) {
	url := os.Getenv("TEST_LEDGER_ADAPTER_REDIS_URL")
	if url == "" {
		t.Skip("isolated Redis URL required")
	}
	db, _ := newLedgerTestDBs(t)
	models := []interface{}{&DecisionGenerationRefund{}, &SpendBudgetCharge{}, &SpendBudget{}, &SpendBudgetAlert{}, &ProjectSpend{}}
	models = append(models, RedisLedgerModels()...)
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	oldRedis, oldEnabled, oldQueue, oldStream, oldEngine := common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled, common.RelayEventStream, GatewayLedger()
	common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled = client, true, true
	common.RelayEventStream = ledger.Prefix + "test:refund-telemetry"
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
		common.RDB, common.RedisEnabled, common.RelayEventQueueEnabled, common.RelayEventStream = oldRedis, oldEnabled, oldQueue, oldStream
		gatewayLedger.Store(oldEngine)
	})
	if _, err := BootstrapLedgerContinuity(ctx, db); err != nil {
		t.Fatal(err)
	}
	engine := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	gatewayLedger.Store(engine)
	user := User{Id: 9301, Username: "redis-refund", Quota: 1000}
	key := Token{Id: 9301, UserId: user.Id, Key: "isolated-refund", RemainQuota: 1000}
	for _, row := range []interface{}{&user, &key} {
		if err := db.Session(&gorm.Session{SkipHooks: true}).Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	when := time.Date(2026, 9, 30, 23, 59, 0, 0, time.UTC)
	budget := ledger.Budget{ID: 9301, ProjectID: 42, Scope: SpendBudgetScopeToken, TokenID: key.Id, Start: when.Truncate(24 * time.Hour).Unix(), End: when.Truncate(24 * time.Hour).Add(24 * time.Hour).Unix(), Limit: 1000, Block: true}
	input := ledger.Input{RequestID: "redis-original-refund", PayerID: user.Id, ActorID: 11, ProjectID: 42, TokenID: key.Id, Amount: 100, OccurredAt: when, Budgets: []ledger.Budget{budget}}
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, input, 80); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Drain(ctx, "refund-test"); err != nil {
		t.Fatal(err)
	}
	var original RedisLedgerOperation
	if err := db.First(&original, "id = ?", "settle:"+input.RequestID).Error; err != nil {
		t.Fatal(err)
	}
	var originalEvent ledger.Event
	if err := json.Unmarshal([]byte(original.Input), &originalEvent); err != nil {
		t.Fatal(err)
	}
	if original.Amount != 80 || originalEvent.Amount != 80 || originalEvent.PayerID != input.PayerID || originalEvent.ActorID != input.ActorID || originalEvent.TokenID != input.TokenID || originalEvent.ProjectID != input.ProjectID || !originalEvent.OccurredAt.Equal(when) || len(originalEvent.Budgets) != 1 || originalEvent.Budgets[0] != budget {
		t.Fatalf("permanent settlement lost immutable money or original budget: operation=%+v event=%+v", original, originalEvent)
	}
	// The permanent operation remains authoritative after transient expiry.
	client.Del(ctx, ledger.Prefix+"request:"+input.RequestID)
	client.HDel(ctx, ledger.Prefix+"journal", input.RequestID)
	receipt := common.InternalChargeReceipt{RequestID: input.RequestID, UserID: user.Id, TokenID: key.Id, ProjectID: 42, ActorUserID: 11, ChargedMicrousd: 100}
	for index := 0; index < 3; index++ {
		if err := RecordDecisionGenerationRefund(receipt, "invalid questions"); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := client.HGet(ctx, ledger.Prefix+"wallet:9301", "avail").Int64(); err != nil || got != 1000 {
		t.Fatalf("hot wallet refund=%d err=%v", got, err)
	}
	if _, err := engine.Drain(ctx, "refund-test"); err != nil {
		t.Fatal(err)
	}
	db.First(&user, user.Id)
	db.First(&key, key.Id)
	if user.Quota != 1000 || key.RemainQuota != 1000 || key.UsedQuota != 0 {
		t.Fatalf("permanent refund balance=%d key=%d used=%d", user.Quota, key.RemainQuota, key.UsedQuota)
	}
	var retained RedisLedgerOperation
	if err := db.First(&retained, "id = ?", original.ID).Error; err != nil || retained.Input != original.Input || retained.Amount != original.Amount {
		t.Fatalf("refund mutated original permanent financial proof: %+v %v", retained, err)
	}
	var legacyCharges int64
	if err := db.Model(&SpendBudgetCharge{}).Count(&legacyCharges).Error; err != nil || legacyCharges != 0 {
		t.Fatalf("modern settlement/refund emitted redundant legacy budget charges: count=%d err=%v", legacyCharges, err)
	}
	var total billing.MicroUSD
	if err := db.Model(&ProjectSpend{}).Select("COALESCE(SUM(amount_microusd),0)").Scan(&total).Error; err != nil || total != 0 {
		t.Fatalf("original-period daily reversal=%d err=%v", total, err)
	}
	if count, err := client.XLen(ctx, common.RelayEventStream).Result(); err != nil || count != 1 {
		t.Fatalf("refund metadata duplicated=%d err=%v", count, err)
	}
	// The refund reverses the hot budget too, so the hot counter and the SQL
	// snapshot the reconciler compares still agree.
	if spent, err := client.HGet(ctx, budget.Key(), "spent").Int64(); err != nil || spent != 0 {
		t.Fatalf("hot budget after refund spent=%d err=%v", spent, err)
	}
	expected, err := engine.Storage.Snapshot(ctx, ledger.Input{Unlimited: true, Budgets: []ledger.Budget{budget}})
	if err != nil || expected.Budgets[budget.Key()] != 0 {
		t.Fatalf("SQL budget after refund=%d err=%v", expected.Budgets[budget.Key()], err)
	}
}
