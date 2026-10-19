package model

import (
	"context"
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

func TestExternalLedgerDebitRespectsLiveReservationsPostgres(t *testing.T) {
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

	user := User{Id: 741, Username: "external-debit-payer", Quota: 10000}
	if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := InstallIdentityMutationFence(db); err != nil {
		t.Fatal(err)
	}
	hold := ledger.Input{RequestID: "external-live-hold", PayerID: user.Id, ActorID: user.Id, Unlimited: true, Amount: 9000}
	if err := engine.Reserve(ctx, hold); err != nil {
		t.Fatal(err)
	}
	debit := func(amount int64, id string) error {
		return AdjustUserQuotaWithLedger(AdjustUserQuotaWithLedgerInput{UserID: user.Id, DeltaQuota: billing.MicroUSD(-amount), Kind: WalletLedgerKindSpend, RefType: "external-test", RefID: id})
	}
	if err := debit(1500, "rejected"); err == nil {
		t.Fatal("SQL balance admitted money held by an upstream request")
	}
	if err := debit(600, "accepted"); err != nil {
		t.Fatal(err)
	}
	walletKey := fmt.Sprintf("%swallet:%d", ledger.Prefix, user.Id)
	balance, err := client.HGet(ctx, walletKey, "avail").Int64()
	if err != nil || balance != 400 {
		t.Fatalf("immediate debit available=%d %v", balance, err)
	}
	rollback := errors.New("force rollback")
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := adjustUserQuotaWithLedgerTx(tx, AdjustUserQuotaWithLedgerInput{UserID: user.Id, DeltaQuota: -100, Kind: WalletLedgerKindSpend, RefType: "external-test", RefID: "rollback"}); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var actual User
	if err := db.First(&actual, user.Id).Error; err != nil {
		t.Fatal(err)
	}
	if actual.Quota != 9400 || actual.CreditSeq != 1 {
		t.Fatalf("rollback changed money=%d seq=%d", actual.Quota, actual.CreditSeq)
	}
	if exists, err := client.Exists(ctx, fmt.Sprintf("%sfence:%d", ledger.Prefix, user.Id)).Result(); err != nil || exists != 0 {
		t.Fatalf("transaction leaked payer fence %d %v", exists, err)
	}
	if err := ProcessLedgerCounterRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	balance, err = client.HGet(ctx, walletKey, "avail").Int64()
	if err != nil || balance != 400 {
		t.Fatalf("refresh lost existing hold %d %v", balance, err)
	}
	if err := engine.Release(ctx, hold); err != nil {
		t.Fatal(err)
	}
	balance, err = client.HGet(ctx, walletKey, "avail").Int64()
	if err != nil || balance != 9400 {
		t.Fatalf("released hold money=%d %v", balance, err)
	}
	var pending int64
	db.Model(&LedgerCreditSync{}).Where("applied_at IS NULL").Count(&pending)
	if pending != 0 {
		t.Fatalf("immediate credit synchronization left %d tasks", pending)
	}
	health, err := ReadLedgerHealth(ctx)
	if err != nil || health.Exposure != 0 || health.OldestAgeSeconds != 0 {
		t.Fatalf("health=%+v %v", health, err)
	}
	ledgerAlertNotifier.RLock()
	oldNotifier := ledgerAlertNotifier.send
	ledgerAlertNotifier.RUnlock()
	t.Cleanup(func() { SetLedgerAlertNotifier(oldNotifier) })
	alerts := []string{}
	SetLedgerAlertNotifier(func(notice LedgerAlertNotice) error {
		alerts = append(alerts, notice.Key)
		return nil
	})
	if err := ReconcileGatewayLedger(ctx); err != nil || len(alerts) != 0 {
		t.Fatalf("consistent wallet generated an alert: %v %v", alerts, err)
	}
	// A mismatch is reported, never repaired by overwriting the hot balance.
	if err := client.HIncrBy(ctx, walletKey, "avail", -10).Err(); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileGatewayLedger(ctx); err != nil || len(alerts) != 1 || alerts[0] != fmt.Sprintf("balance:%d", user.Id) {
		t.Fatalf("unreported wallet drift: %v %v", alerts, err)
	}
	balance, err = client.HGet(ctx, walletKey, "avail").Int64()
	if err != nil || balance != 9390 {
		t.Fatalf("monitor silently changed authoritative wallet=%d %v", balance, err)
	}
	if err := client.HIncrBy(ctx, walletKey, "avail", 10).Err(); err != nil {
		t.Fatal(err)
	}
	// Financial events use seconds; both retained formats must produce the
	// same age instead of an epoch-sized false backlog alert.
	// Use the same clock as the Redis TIME health script; a Docker VM may
	// have subsecond skew from the client host even in an isolated fixture.
	serverNow, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []float64{float64(serverNow.Add(-70 * time.Second).Unix()), float64(serverNow.Add(-70 * time.Second).UnixMilli())} {
		if err := client.ZAdd(ctx, ledger.Prefix+"uncommitted", &redis.Z{Score: stamp, Member: "health-age-fixture"}).Err(); err != nil {
			t.Fatal(err)
		}
		health, err := ReadLedgerHealth(ctx)
		if err != nil || health.OldestAgeSeconds < 70 || health.OldestAgeSeconds > 72 || health.Uncommitted != 1 {
			t.Fatalf("incorrect backlog age: %+v %v", health, err)
		}
	}
	client.ZRem(ctx, ledger.Prefix+"uncommitted", "health-age-fixture")
	// Health/backup checks must retain the same exact integer as the ledger.
	for _, exposure := range []int64{123456789012345, 9007199254741017, math.MaxInt64} {
		if err := client.Set(ctx, ledger.Prefix+"exposure", exposure, 0).Err(); err != nil {
			t.Fatal(err)
		}
		health, err := ReadLedgerHealth(ctx)
		if err != nil || health.Exposure != exposure {
			t.Fatalf("health rounded exposure=%d want=%d: %v", health.Exposure, exposure, err)
		}
	}
	for _, invalid := range []string{"-1", "9223372036854775808"} {
		if err := client.Set(ctx, ledger.Prefix+"exposure", invalid, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLedgerHealth(ctx); err == nil {
			t.Fatalf("health accepted invalid exposure %s", invalid)
		}
	}
	client.Set(ctx, ledger.Prefix+"exposure", "0", 0)
}
