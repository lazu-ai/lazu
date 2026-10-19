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

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestRedisLedgerPostgresRecovery(t *testing.T) {
	dsn := os.Getenv("TEST_GATEWAY_CORE_DSN")
	redisURL := os.Getenv("TEST_LEDGER_ADAPTER_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("isolated Postgres and Redis URLs required")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	schema := "ledger_test_" + strings.ReplaceAll(common.GenerateRequestID(), "-", "")
	schema = strings.ToLower(schema)
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schema + " CASCADE"); sqlDB, _ := base.DB(); sqlDB.Close() })
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{&User{}, &Token{}, &Project{}, &WalletLedger{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetAlert{}, &ProjectSpend{}, &MediaJob{}}
	models = append(models, RedisLedgerModels()...)
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	oldCore, oldRedis, oldEnabled, oldGateway := CoreDB(), common.RDB, common.RedisEnabled, GatewayLedger()
	SetCoreDB(db)
	common.RDB = client
	common.RedisEnabled = true
	t.Cleanup(func() {
		keys, _ := client.Keys(ctx, ledger.Prefix+"*").Result()
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
		client.Close()
		SetCoreDB(oldCore)
		common.RDB = oldRedis
		common.RedisEnabled = oldEnabled
		gatewayLedger.Store(oldGateway)
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	keys, _ := client.Keys(ctx, ledger.Prefix+"*").Result()
	if len(keys) > 0 {
		client.Del(ctx, keys...)
	}
	if _, err := BootstrapLedgerContinuity(ctx, db); err != nil {
		t.Fatal(err)
	}
	engine := ledger.New(client, redisLedgerStorage{})
	if err := BindLedgerContinuity(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	engine.SpoolDir = t.TempDir()
	gatewayLedger.Store(engine)
	user := User{Id: 741, Username: "ledger-payer", Quota: 10_000}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	project := Project{ID: 741, PublicID: "ledger-project", OwnerUserID: user.Id, Name: "ledger"}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	key := Token{ProjectID: project.ID, Id: 741, UserId: user.Id, Key: "isolated-test-key", RemainQuota: 5000}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	budget := SpendBudget{ProjectID: project.ID, ID: 741, Scope: SpendBudgetScopeToken, TokenID: key.Id, Period: SpendBudgetPeriodDay, LimitMicrousd: 1000, Enabled: true}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	input := ledger.Input{ProjectID: project.ID, RequestID: "postgres-recovery", PayerID: user.Id, ActorID: user.Id, TokenID: key.Id, Amount: 1000, OccurredAt: at, Budgets: LedgerBudgets([]SpendBudget{budget}, at)}
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, input, 700); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Drain(ctx, "adapter"); err != nil {
		t.Fatal(err)
	}
	assertBalances := func(wallet, keyAmount int64) {
		t.Helper()
		var u User
		var k Token
		if err := db.First(&u, user.Id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&k, key.Id).Error; err != nil {
			t.Fatal(err)
		}
		if int64(u.Quota) != wallet || int64(k.RemainQuota) != keyAmount {
			t.Fatalf("SQL balances wallet=%d key=%d want %d,%d", u.Quota, k.RemainQuota, wallet, keyAmount)
		}
	}
	assertBalances(9300, 4300)
	event := ledger.Event{Input: input, OperationID: input.RequestID, Kind: "settle"}
	event.Amount = 700
	if err := (redisLedgerStorage{}).Apply(ctx, []ledger.Event{event, event}); err != nil {
		t.Fatal(err)
	}
	assertBalances(9300, 4300)
	var records int64
	if err := db.Model(&WalletLedger{}).Where("ref_id = ?", input.RequestID).Count(&records).Error; err != nil {
		t.Fatal(err)
	}
	if records != 1 {
		t.Fatalf("financial records %d want1", records)
	}
	// Erasing request state cannot authorize another debit after durable commit.
	client.Del(ctx, ledger.Prefix+"request:"+input.RequestID)
	if err := engine.Settle(ctx, input, 700); err != nil {
		t.Fatal(err)
	}
	assertBalances(9300, 4300)
	if err := AdjustUserQuotaWithLedger(AdjustUserQuotaWithLedgerInput{UserID: user.Id, DeltaQuota: 1000, Kind: WalletLedgerKindTopup, RefType: "test_topup", RefID: "once"}); err != nil {
		t.Fatal(err)
	}
	// A snapshot includes the pending credit's sequence; its later retry is noop.
	client.Del(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, user.Id))
	if err := engine.Initialize(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.RetryCredits(ctx); err != nil {
		t.Fatal(err)
	}
	balance, err := client.HGet(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, user.Id), "avail").Int64()
	if err != nil || balance != 10300 {
		t.Fatalf("rebuild + pending topup =%d (%v), want10300", balance, err)
	}
	// Cold counter rebuild retains live reservations and uncommitted charges.
	second := input
	second.RequestID = "postgres-second"
	second.Amount = 200
	if err := engine.Reserve(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, second, 100); err != nil {
		t.Fatal(err)
	}
	third := input
	third.RequestID = "postgres-third"
	third.Amount = 100
	if err := engine.Reserve(ctx, third); err != nil {
		t.Fatal(err)
	}
	client.Del(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, user.Id), fmt.Sprintf("%stoken:%d", ledger.Prefix, key.Id), input.Budgets[0].Key())
	if err := engine.Initialize(ctx, third); err != nil {
		t.Fatal(err)
	}
	balance, err = client.HGet(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, user.Id), "avail").Int64()
	if err != nil || balance != 10100 {
		t.Fatalf("cold balance=%d %v want10100", balance, err)
	}
	reserved, _ := client.HGet(ctx, input.Budgets[0].Key(), "reserved").Int64()
	spent, _ := client.HGet(ctx, input.Budgets[0].Key(), "spent").Int64()
	if reserved != 100 || spent != 800 {
		t.Fatalf("cold budget spent=%d reserved=%d", spent, reserved)
	}
	if err := engine.Release(ctx, third); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Drain(ctx, "adapter"); err != nil {
		t.Fatal(err)
	}
	assertBalances(10200, 4200)
	var sum billing.MicroUSD
	if err := db.Model(&SpendBudgetCharge{}).Select("COALESCE(SUM(amount_microusd),0)").Scan(&sum).Error; err != nil {
		t.Fatal(err)
	}
	if sum != 0 {
		t.Fatalf("modern path wrote redundant budget proof rows: %d", sum)
	}
	var dailyRows int64
	db.Model(&ProjectSpend{}).Where("aggregated = ?", true).Count(&dailyRows)
	if dailyRows != 1 {
		t.Fatalf("daily rows=%d want1", dailyRows)
	}
	calculated, err := sumProjectSpendTx(db, budget, input.Budgets[0].Start, input.Budgets[0].End)
	if err != nil || calculated != 800 {
		t.Fatalf("daily+rollback budget=%d %v", calculated, err)
	}
	client.Set(ctx, ledger.Prefix+"maintenance", "test rollback", 0)
	report, err := PrepareLegacyLedgerRollback(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	var legacyProofSum billing.MicroUSD
	if err := db.Model(&SpendBudgetCharge{}).Select("SUM(amount_microusd)").Scan(&legacyProofSum).Error; err != nil {
		t.Fatal(err)
	}
	if legacyProofSum != 800 {
		t.Fatalf("materialized rollback budget proof sum=%d want800", legacyProofSum)
	}
	if report.LegacyRows != 2 || report.DailyRows != 1 {
		t.Fatalf("rollback %+v", report)
	}
	calculated, err = sumProjectSpendTx(db, budget, input.Budgets[0].Start, input.Budgets[0].End)
	if err != nil || calculated != 800 {
		t.Fatalf("restored budget=%d %v", calculated, err)
	}
	if _, err := PrepareLegacyLedgerRollback(ctx, true); err != nil {
		t.Fatal(err)
	}
	t.Run("budget-definition-change-with-inflight-and-pending-charges", func(t *testing.T) {
		client.Del(ctx, ledger.Prefix+"maintenance")
		pending := input
		pending.RequestID, pending.Amount = "budget-change-pending", 100
		if err := engine.Reserve(ctx, pending); err != nil {
			t.Fatal(err)
		}
		if err := engine.Settle(ctx, pending, 50); err != nil {
			t.Fatal(err)
		}
		held := input
		held.RequestID, held.Amount = "budget-change-held", 100
		if err := engine.Reserve(ctx, held); err != nil {
			t.Fatal(err)
		}
		budget.Period = SpendBudgetPeriodMonth
		if err := db.Model(&budget).Update("period", budget.Period).Error; err != nil {
			t.Fatal(err)
		}
		monthly := input
		monthly.RequestID, monthly.Amount = "budget-monthly-new", 60
		monthly.Budgets = LedgerBudgets([]SpendBudget{budget}, at)
		assertBudget := func(key string, spentWant, reservedWant int64) {
			t.Helper()
			spent, spendErr := client.HGet(ctx, key, "spent").Int64()
			reserved, reservedErr := client.HGet(ctx, key, "reserved").Int64()
			if spendErr != nil || reservedErr != nil || spent != spentWant || reserved != reservedWant {
				t.Fatalf("budget %s spent=%d reserved=%d want %d,%d errors=%v,%v", key, spent, reserved, spentWant, reservedWant, spendErr, reservedErr)
			}
		}
		if err := engine.Initialize(ctx, monthly); err != nil {
			t.Fatal(err)
		}
		assertBudget(monthly.Budgets[0].Key(), 850, 100)
		if err := engine.Reserve(ctx, monthly); err == nil {
			t.Fatal("new monthly budget ignored the old request's hold")
		}
		if err := engine.Settle(ctx, held, 80); err != nil {
			t.Fatal(err)
		}
		assertBudget(monthly.Budgets[0].Key(), 930, 0)
		if err := engine.Reserve(ctx, monthly); err != nil {
			t.Fatal(err)
		}
		if err := engine.Settle(ctx, monthly, 10); err != nil {
			t.Fatal(err)
		}
		assertBudget(monthly.Budgets[0].Key(), 940, 0)
		if _, err := engine.Drain(ctx, "budget-changes"); err != nil {
			t.Fatal(err)
		}
		budgetOnly, err := (redisLedgerStorage{}).Snapshot(ctx, ledger.Input{Unlimited: true, Budgets: monthly.Budgets})
		if err != nil || budgetOnly.Budgets[monthly.Budgets[0].Key()] != 940 {
			t.Fatalf("budget-only authoritative snapshot %+v %v", budgetOnly, err)
		}
		oldScopeHold := monthly
		oldScopeHold.RequestID, oldScopeHold.Amount = "budget-old-scope-held", 10
		if err := engine.Reserve(ctx, oldScopeHold); err != nil {
			t.Fatal(err)
		}
		budget.Scope, budget.TokenID, budget.UserID = SpendBudgetScopeUser, 0, user.Id
		if err := db.Model(&budget).Updates(map[string]interface{}{"scope": budget.Scope, "token_id": 0, "user_id": user.Id}).Error; err != nil {
			t.Fatal(err)
		}
		userScope := monthly
		userScope.RequestID = "budget-user-scope-new"
		userScope.Budgets = LedgerBudgets([]SpendBudget{budget}, at)
		if userScope.Budgets[0].Key() == monthly.Budgets[0].Key() {
			t.Fatal("changed scope reused the old budget counter")
		}
		if err := engine.Initialize(ctx, userScope); err != nil {
			t.Fatal(err)
		}
		assertBudget(userScope.Budgets[0].Key(), 940, 10)
		if err := engine.Release(ctx, oldScopeHold); err != nil {
			t.Fatal(err)
		}
		assertBudget(monthly.Budgets[0].Key(), 940, 0)
		assertBudget(userScope.Budgets[0].Key(), 940, 0)
		platform := SpendBudget{ID: 742, Scope: SpendBudgetScopeOrg, Period: SpendBudgetPeriodMonth, LimitMicrousd: 1000, Enforcement: SpendBudgetEnforcementBlock, Enabled: true}
		if err := db.Create(&platform).Error; err != nil {
			t.Fatal(err)
		}
		legacy := SpendBudgetCharge{BudgetID: platform.ID, Scope: platform.Scope, RequestID: "platform-historical", Period: platform.Period, AmountMicrousd: 7, CreatedAt: at}
		if err := db.Create(&legacy).Error; err != nil {
			t.Fatal(err)
		}
		global := monthly
		global.RequestID, global.Amount = "new-platform-budget", 10
		global.Budgets = LedgerBudgets([]SpendBudget{platform}, at)
		if err := engine.Initialize(ctx, global); err != nil {
			t.Fatal(err)
		}
		assertBudget(global.Budgets[0].Key(), 947, 0)
		if err := engine.Reserve(ctx, global); err != nil {
			t.Fatal(err)
		}
		if err := engine.Settle(ctx, global, 5); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Drain(ctx, "platform-budget"); err != nil {
			t.Fatal(err)
		}
		budgetOnly, err = (redisLedgerStorage{}).Snapshot(ctx, ledger.Input{Unlimited: true, Budgets: global.Budgets})
		if err != nil || budgetOnly.Budgets[global.Budgets[0].Key()] != 952 {
			t.Fatalf("new platform budget lost historical operations or doubled modern budget charges: %+v %v", budgetOnly, err)
		}
		assertBudget(global.Budgets[0].Key(), 952, 0)
	})
	t.Run("cold-snapshot-more-than-postgres-bind-limit", func(t *testing.T) {
		cold := ledger.Input{PayerID: user.Id, Unlimited: true}
		before, err := (redisLedgerStorage{}).Snapshot(ctx, cold)
		if err != nil {
			t.Fatal(err)
		}
		const total = 66000
		for start := 0; start < total; start += 500 {
			pipe := client.Pipeline()
			for n := start; n < min(start+500, total); n++ {
				id := fmt.Sprintf("snapshot-scale-%d", n)
				event := ledger.Event{Input: ledger.Input{RequestID: id, PayerID: user.Id, Unlimited: true, Amount: 1, OccurredAt: at}, OperationID: id, Kind: "settle"}
				raw, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				pipe.HSet(ctx, ledger.Prefix+"journal", id, string(raw))
				pipe.ZAdd(ctx, ledger.Prefix+"uncommitted", &redis.Z{Score: float64(at.Unix()), Member: id})
			}
			if _, err := pipe.Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		// Retained acknowledged history must not be parsed or included at all.
		if err := client.HSet(ctx, ledger.Prefix+"journal", "acked-malformed-history", "invalid retained payload").Err(); err != nil {
			t.Fatal(err)
		}
		after, err := (redisLedgerStorage{}).Snapshot(ctx, cold)
		if err != nil || after.Balance != before.Balance-total {
			t.Fatalf("chunked pending snapshot: before=%d after=%d error=%v", before.Balance, after.Balance, err)
		}
	})

}
