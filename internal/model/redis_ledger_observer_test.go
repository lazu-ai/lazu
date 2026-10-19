package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestPostgresLedgerReconciliationEmptyCreditIntervalSkipsSQL(t *testing.T) {
	db, capture := postgresLedgerBatchFixture(t)
	if err := db.AutoMigrate(&LedgerCreditSync{}, &Project{}); err != nil {
		t.Fatal(err)
	}
	client, engine := consoleBalanceRedis(t)
	ctx := context.Background()
	if err := db.Model(&User{}).Where("id = ?", 1).Updates(map[string]interface{}{"quota": 1450, "credit_seq": 3}).Error; err != nil {
		t.Fatal(err)
	}
	credits := []LedgerCreditSync{{UserID: 1, Sequence: 1, Amount: 250}, {UserID: 1, Sequence: 2, Amount: -100}, {UserID: 1, Sequence: 3, Amount: 300}, {UserID: 2, Sequence: 2, Amount: 999}}
	if err := db.Create(&credits).Error; err != nil {
		t.Fatal(err)
	}
	ledgerAlertNotifier.Lock()
	oldNotifier := ledgerAlertNotifier.send
	ledgerAlertNotifier.Unlock()
	var alerts []string
	SetLedgerAlertNotifier(func(notice LedgerAlertNotice) error { alerts = append(alerts, notice.Key); return nil })
	t.Cleanup(func() { SetLedgerAlertNotifier(oldNotifier) })
	for _, scenario := range []struct {
		name, hotSequence string
		available         int64
		creditQueries     int
		wantParseError    bool
		wantAlert         bool
	}{
		{name: "equal", hotSequence: "3", available: 1450},
		{name: "hot-ahead-preserves-empty-range", hotSequence: "4", available: 1450},
		{name: "unsynced-signed-credits", hotSequence: "1", available: 1250, creditQueries: 1},
		{name: "negative-sequence-preserves-original-range", hotSequence: "-1", available: 1000, creditQueries: 1},
		{name: "malformed-sequence-still-rejected", hotSequence: "broken", available: 1450, wantParseError: true},
		{name: "unsynced-mismatch-still-alerts", hotSequence: "1", available: 1249, creditQueries: 1, wantAlert: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := client.HSet(ctx, ledger.Prefix+"wallet:1", "avail", strconv.FormatInt(scenario.available, 10), "credit_seq", scenario.hotSequence, "revision", "1", "generation", "").Err(); err != nil {
				t.Fatal(err)
			}
			before, err := client.HGetAll(ctx, ledger.Prefix+"wallet:1").Result()
			if err != nil {
				t.Fatal(err)
			}
			capture.statements = nil
			alerts = nil
			err = reconcileLedgerPayer(ctx, engine, 1)
			var parseError *strconv.NumError
			if scenario.wantParseError {
				if !errors.As(err, &parseError) {
					t.Fatalf("malformed sequence changed its parse-error contract: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			creditQueries := 0
			for _, sql := range capture.statements {
				if strings.Contains(sql, "FROM \"ledger_credit_syncs\"") && strings.Contains(sql, "SUM(amount)") {
					creditQueries++
				}
			}
			if creditQueries != scenario.creditQueries {
				t.Fatalf("credit SUM SQL count=%d, want %d; queries=%v", creditQueries, scenario.creditQueries, capture.statements)
			}
			if (len(alerts) > 0) != scenario.wantAlert {
				t.Fatalf("financial comparison changed: alerts=%v, wantAlert=%v", alerts, scenario.wantAlert)
			}
			after, err := client.HGetAll(ctx, ledger.Prefix+"wallet:1").Result()
			if err != nil || before["avail"] != after["avail"] || before["credit_seq"] != after["credit_seq"] || before["revision"] != after["revision"] {
				t.Fatalf("read-only reconciliation changed money or sequence: before=%v after=%v err=%v", before, after, err)
			}
			t.Logf("credit SUM statements=%d, alerts=%d", creditQueries, len(alerts))
		})
	}
}

func TestPostgresLedgerBlockedObserverStillCommitsFinancialOperation(t *testing.T) {
	core, _ := postgresTelemetryFixture(t)
	if err := core.AutoMigrate(&Token{}, &RedisLedgerOperation{}, &LedgerCreditSync{}, &WalletLedger{}, &ProjectSpend{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetAlert{}); err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&User{Id: 1, Username: "observer-payer", Quota: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	_, engine := consoleBalanceRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	started, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	engine.Observer = func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		<-finish
		return ctx.Err()
	}
	reported := make(chan error, 8)
	go func() { engine.Run(ctx, "postgres-observer", func(err error) { reported <- err }); close(done) }()
	var finishOnce sync.Once
	t.Cleanup(func() {
		cancel()
		finishOnce.Do(func() { close(finish) })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("financial worker did not join before fixture cleanup")
		}
	})
	select {
	case <-started:
	case <-time.After(12 * time.Second):
		t.Fatal("production observer tick did not start")
	}
	input := ledger.Input{RequestID: "observer-permanent-commit", PayerID: 1, ActorID: 1, Unlimited: true, Amount: 100, OccurredAt: time.Now().UTC()}
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, input, 70); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var count int64
		if err := core.Model(&RedisLedgerOperation{}).Where("id = ?", ledgerOperationID(input.RequestID, "settle")).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("blocked observer prevented permanent Core financial commit")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var payer User
	if err := core.First(&payer, 1).Error; err != nil || payer.Quota != billing.MicroUSD(930) || payer.UsedQuota != billing.MicroUSD(70) {
		t.Fatalf("financial commit changed money: quota=%d used=%d err=%v", payer.Quota, payer.UsedQuota, err)
	}
	var ledgers int64
	if err := core.Model(&WalletLedger{}).Where("event_id = ?", ledgerOperationID(input.RequestID, "settle")).Count(&ledgers).Error; err != nil || ledgers != 1 {
		t.Fatalf("permanent settlement lacks its wallet entry: count=%d err=%v", ledgers, err)
	}
	cancel()
	finishOnce.Do(func() { close(finish) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not exit after observer cancellation")
	}
	for {
		select {
		case err := <-reported:
			if !errors.Is(err, context.Canceled) && !(errors.Is(err, ledger.ErrUnavailable) && strings.HasSuffix(err.Error(), context.Canceled.Error())) {
				t.Fatalf("financial worker reported unexpected error: %v", err)
			}
		default:
			return
		}
	}
}
