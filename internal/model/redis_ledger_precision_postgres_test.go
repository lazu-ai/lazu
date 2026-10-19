package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

func TestRedisLedgerExactLargeMoneyPostgres(t *testing.T) {
	dsn, redisURL := os.Getenv("TEST_GATEWAY_CORE_DSN"), os.Getenv("TEST_LEDGER_ADAPTER_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("isolated PostgreSQL and Redis URLs required")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	schema := "decimal_ledger_" + strings.ToLower(strings.ReplaceAll(common.GenerateRequestID(), "-", ""))
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schema + " CASCADE"); sqlDB, _ := base.DB(); sqlDB.Close() })
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{&User{}, &Token{}, &Project{}, &WalletLedger{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetAlert{}, &SpendBudgetReservation{}, &ProjectSpend{}, &MediaJob{}}
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
	oldCore, oldRedis, oldEnabled, oldEngine := CoreDB(), common.RDB, common.RedisEnabled, GatewayLedger()
	SetCoreDB(db)
	common.RDB, common.RedisEnabled = client, true
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
		SetCoreDB(oldCore)
		common.RDB, common.RedisEnabled = oldRedis, oldEnabled
		gatewayLedger.Store(oldEngine)
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	if _, err := BootstrapLedgerContinuity(ctx, db); err != nil {
		t.Fatal(err)
	}
	engine := ledger.New(client, redisLedgerStorage{})
	engine.HardLimit = math.MaxInt64
	engine.SpoolDir = t.TempDir()
	if err := BindLedgerContinuity(ctx, db, engine); err != nil {
		t.Fatal(err)
	}
	gatewayLedger.Store(engine)
	const initial int64 = 9007199254741017
	const priorSpent int64 = 9007199254741011
	const reserve int64 = 623456789012345
	const actual int64 = 333456789012345
	const refund int64 = 111111111111111
	user := User{Id: 921, Username: "decimal-payer", Quota: billing.MicroUSD(initial), CreditSeq: initial}
	project := Project{ID: 922, PublicID: "prj_decimal_ledger", OwnerUserID: user.Id, Name: "Exact decimal ledger"}
	key := Token{Id: 923, UserId: user.Id, ProjectID: project.ID, Key: "decimal-ledger-test", RemainQuota: billing.MicroUSD(initial)}
	for _, row := range []interface{}{&user, &project, &key} {
		if err := db.Session(&gorm.Session{SkipHooks: true}).Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	budget := SpendBudget{ID: 924, ProjectID: project.ID, TokenID: key.Id, Scope: SpendBudgetScopeToken, Period: SpendBudgetPeriodDay, LimitMicrousd: 9007199254740991, Enforcement: SpendBudgetEnforcementAlert, Enabled: true}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	input := ledger.Input{RequestID: "postgres-decimal", PayerID: user.Id, ActorID: user.Id, ProjectID: project.ID, TokenID: key.Id, Amount: reserve, OccurredAt: at, Budgets: LedgerBudgets([]SpendBudget{budget}, at), PricingVersion: 1791000000123456789}
	legacy := SpendBudgetCharge{BudgetID: budget.ID, ProjectID: project.ID, Scope: budget.Scope, TokenID: key.Id, UserID: user.Id, RequestID: "historic-decimal", Period: budget.Period, PeriodStart: input.Budgets[0].Start, PeriodEnd: input.Budgets[0].End, AmountMicrousd: billing.MicroUSD(priorSpent), CreatedAt: at}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	walletKey := fmt.Sprintf("%swallet:%d", ledger.Prefix, user.Id)
	tokenKey := fmt.Sprintf("%stoken:%d", ledger.Prefix, key.Id)
	assertHot := func(balance, tokenAmount, spent, reserved int64) {
		t.Helper()
		for field, want := range map[string]int64{"avail": balance, "credit_seq": initial} {
			if got, err := client.HGet(ctx, walletKey, field).Int64(); err != nil || got != want {
				t.Fatalf("hot %s=%d want%d %v", field, got, want, err)
			}
		}
		if got, err := client.Get(ctx, tokenKey).Int64(); err != nil || got != tokenAmount {
			t.Fatalf("hot key=%d want%d %v", got, tokenAmount, err)
		}
		for field, want := range map[string]int64{"spent": spent, "reserved": reserved} {
			if got, err := client.HGet(ctx, input.Budgets[0].Key(), field).Int64(); err != nil || got != want {
				t.Fatalf("budget %s=%d want%d %v", field, got, want, err)
			}
		}
	}
	assertSQL := func(balance, tokenAmount, spent int64) {
		t.Helper()
		var u User
		var k Token
		if err := db.First(&u, user.Id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.First(&k, key.Id).Error; err != nil {
			t.Fatal(err)
		}
		if int64(u.Quota) != balance || int64(k.RemainQuota) != tokenAmount {
			t.Fatalf("SQL wallet=%d key=%d want%d,%d", u.Quota, k.RemainQuota, balance, tokenAmount)
		}
		amount, err := sumSpendMicrousdForBudgetWithDB(db, budget, input.Budgets[0].Start, input.Budgets[0].End)
		if err != nil || int64(amount) != spent {
			t.Fatalf("SQL budget=%d want%d %v", amount, spent, err)
		}
	}
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertHot(initial-reserve, initial-reserve, priorSpent, reserve)
	if err := engine.Settle(ctx, input, actual); err != nil {
		t.Fatal(err)
	}
	if err := engine.Refund(ctx, input, refund, "postgres-decimal-refund"); err != nil {
		t.Fatal(err)
	}
	net := actual - refund
	assertHot(initial-net, initial-net, priorSpent+net, 0)
	if err := client.XGroupCreateMkStream(ctx, ledger.Stream, ledger.Group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: ledger.Group, Consumer: "decimal-crashed", Streams: []string{ledger.Stream, ">"}, Count: 1, Block: time.Millisecond}).Result()
	if err != nil || len(streams) != 1 || len(streams[0].Messages) != 1 {
		t.Fatalf("read crash boundary %+v %v", streams, err)
	}
	message := streams[0].Messages[0]
	var committed ledger.Event
	if err := json.Unmarshal([]byte(message.Values["event"].(string)), &committed); err != nil || committed.Amount != actual || committed.PricingVersion != input.PricingVersion {
		t.Fatalf("SQL event decimal fact %+v %v", committed, err)
	}
	committed.StreamID = message.ID
	if err := (redisLedgerStorage{}).Apply(ctx, []ledger.Event{committed}); err != nil {
		t.Fatal(err)
	}
	assertSQL(initial-actual, initial-actual, priorSpent+actual)
	// A cold snapshot across SQL commit-before-ACK must omit the committed
	// journal fact, subtract the uncommitted refund once, and preserve decimals.
	client.Del(ctx, walletKey, tokenKey, input.Budgets[0].Key())
	if err := engine.Initialize(ctx, input); err != nil {
		t.Fatal(err)
	}
	assertHot(initial-net, initial-net, priorSpent+net, 0)
	if err := client.Do(ctx, "XCLAIM", ledger.Stream, ledger.Group, "decimal-abandoned", 0, message.ID, "IDLE", 60000).Err(); err != nil {
		t.Fatal(err)
	}
	if n, err := engine.Drain(ctx, "decimal-recovered"); err != nil || n != 2 {
		t.Fatalf("SQL replay=%d %v", n, err)
	}
	assertSQL(initial-net, initial-net, priorSpent+net)
	assertHot(initial-net, initial-net, priorSpent+net, 0)
	var ops []RedisLedgerOperation
	if err := db.Order("id").Find(&ops).Error; err != nil || len(ops) != 2 {
		t.Fatalf("permanent ops %+v %v", ops, err)
	}
	for _, op := range ops {
		var event ledger.Event
		if err := json.Unmarshal([]byte(op.Input), &event); err != nil || event.Amount != op.Amount || event.PricingVersion != input.PricingVersion {
			t.Fatalf("permanent serialized decimal %+v %v", event, err)
		}
	}
	var ledgerSum int64
	if err := db.Model(&WalletLedger{}).Select("COALESCE(SUM(delta_microusd),0)").Scan(&ledgerSum).Error; err != nil || ledgerSum != -net {
		t.Fatalf("wallet history=%d want%d %v", ledgerSum, -net, err)
	}
	if exposure, err := client.Get(ctx, ledger.Prefix+"exposure").Int64(); err != nil || exposure != 0 {
		t.Fatalf("ACK exact exposure=%d %v", exposure, err)
	}
	if err := AdjustUserQuotaWithLedger(AdjustUserQuotaWithLedgerInput{UserID: user.Id, DeltaQuota: billing.MicroUSD(reserve), Kind: WalletLedgerKindTopup, RefType: "decimal_test", RefID: "large-credit"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.RetryCredits(ctx); err != nil {
		t.Fatal(err)
	}
	assertSQL(initial-net+reserve, initial-net, priorSpent+net)
	if sequence, err := client.HGet(ctx, walletKey, "credit_seq").Int64(); err != nil || sequence != initial+1 {
		t.Fatalf("large SQL credit sequence=%d %v", sequence, err)
	}
	if amount, err := client.HGet(ctx, walletKey, "avail").Int64(); err != nil || amount != initial-net+reserve {
		t.Fatalf("large SQL credit wallet=%d %v", amount, err)
	}
	client.Del(ctx, walletKey, tokenKey, input.Budgets[0].Key())
	if err := engine.Initialize(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.RetryCredits(ctx); err != nil {
		t.Fatal(err)
	}
	if amount, err := client.HGet(ctx, walletKey, "avail").Int64(); err != nil || amount != initial-net+reserve {
		t.Fatalf("large credit rebuild duplicated funds=%d %v", amount, err)
	}
	t.Run("new-scope-historical-plus-pending-overflow", func(t *testing.T) {
		definition := SpendBudget{ID: 925, Scope: SpendBudgetScopeUser, UserID: 42, Period: SpendBudgetPeriodDay, LimitMicrousd: 9007199254740991, Enforcement: SpendBudgetEnforcementAlert, Enabled: true}
		if err := db.Create(&definition).Error; err != nil {
			t.Fatal(err)
		}
		probe := ledger.Input{RequestID: "cold-range-regroup", Unlimited: true, Budgets: LedgerBudgets([]SpendBudget{definition}, at)}
		history := SpendBudgetCharge{BudgetID: definition.ID, Scope: definition.Scope, UserID: 42, RequestID: "cold-range-history", AmountMicrousd: math.MaxInt64 - 50, CreatedAt: at}
		if err := db.Create(&history).Error; err != nil {
			t.Fatal(err)
		}
		pending := ledger.Event{Input: ledger.Input{RequestID: "cold-range-pending", PayerID: 100, ActorID: 42, TokenID: 101, Unlimited: true, Amount: 100, OccurredAt: at}, OperationID: "cold-range-pending", Kind: "settle"}
		raw, err := json.Marshal(pending)
		if err != nil {
			t.Fatal(err)
		}
		client.HSet(ctx, ledger.Prefix+"journal", pending.OperationID, string(raw))
		client.ZAdd(ctx, ledger.Prefix+"uncommitted", &redis.Z{Score: float64(at.Unix()), Member: pending.OperationID})
		defer client.HDel(ctx, ledger.Prefix+"journal", pending.OperationID)
		defer client.ZRem(ctx, ledger.Prefix+"uncommitted", pending.OperationID)
		if _, err := (redisLedgerStorage{}).Snapshot(ctx, probe); !errors.Is(err, ErrLedgerCounterRange) {
			t.Fatalf("historical + pending spend wrapped instead of failing closed: %v", err)
		}
		if err := engine.Initialize(ctx, probe); !errors.Is(err, ErrLedgerCounterRange) {
			t.Fatalf("overflow cold initializer result: %v", err)
		}
		if exists, err := client.Exists(ctx, probe.Budgets[0].Key(), ledger.Prefix+"wallet:0").Result(); err != nil || exists != 0 {
			t.Fatalf("overflow installed financial counters: keys=%d %v", exists, err)
		}
	})
	t.Run("new-platform-scope-holds-across-payers-overflow", func(t *testing.T) {
		definition := SpendBudget{ID: 926, Scope: SpendBudgetScopeOrg, Period: SpendBudgetPeriodDay, LimitMicrousd: 9007199254740991, Enforcement: SpendBudgetEnforcementAlert, Enabled: true}
		if err := db.Create(&definition).Error; err != nil {
			t.Fatal(err)
		}
		probe := ledger.Input{RequestID: "cold-range-platform", Unlimited: true, Budgets: LedgerBudgets([]SpendBudget{definition}, at)}
		holds := make(map[string]interface{}, 1025)
		for index := 0; index < 1025; index++ {
			hold := ledger.Input{RequestID: fmt.Sprintf("range-hold-%d", index), PayerID: 100 + index, ActorID: 100 + index, ProjectID: 100 + index, Unlimited: true, Amount: 9007199254740991, OccurredAt: at}
			raw, err := json.Marshal(hold)
			if err != nil {
				t.Fatal(err)
			}
			holds[hold.RequestID] = string(raw)
		}
		if err := client.HSet(ctx, ledger.Prefix+"reservations", holds).Err(); err != nil {
			t.Fatal(err)
		}
		defer client.Del(ctx, ledger.Prefix+"reservations")
		if _, err := (redisLedgerStorage{}).Snapshot(ctx, probe); !errors.Is(err, ErrLedgerCounterRange) {
			t.Fatalf("new global hold aggregate wrapped instead of failing closed: %v", err)
		}
		if err := engine.Initialize(ctx, probe); !errors.Is(err, ErrLedgerCounterRange) {
			t.Fatalf("overflow platform initializer result: %v", err)
		}
		if exists, err := client.Exists(ctx, probe.Budgets[0].Key(), ledger.Prefix+"wallet:0").Result(); err != nil || exists != 0 {
			t.Fatalf("overflow installed platform counters: keys=%d %v", exists, err)
		}
	})
}
