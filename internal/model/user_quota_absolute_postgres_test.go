package model

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func absoluteQuotaPostgresFixture(t *testing.T) (*gorm.DB, *redis.Client, *ledger.Engine) {
	t.Helper()
	db, client, _, _ := refreshPostgresFixture(t)
	if err := db.AutoMigrate(&WalletLedger{}, &LedgerCreditSync{}, &LedgerCounterRefresh{}, &ProjectSpend{}, &SpendBudgetCharge{}, &RedisLedgerOperation{}, &MediaJob{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&User{}).Where("id = ?", 101).Update("quota", 1000).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clear := func() {
		keys, err := client.Keys(ctx, ledger.Prefix+"*").Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	clear()
	t.Cleanup(clear)
	engine := ledger.New(client, redisLedgerStorage{})
	engine.Continuity = common.GenerateRequestID()
	engine.SpoolDir = t.TempDir()
	if err := client.Set(ctx, ledger.ContinuityKey, engine.Continuity, 0).Err(); err != nil {
		t.Fatal(err)
	}
	previous := gatewayLedger.Load()
	gatewayLedger.Store(engine)
	t.Cleanup(func() { gatewayLedger.Store(previous) })
	if err := InstallIdentityMutationFence(db); err != nil {
		t.Fatal(err)
	}
	return db, client, engine
}

func TestSetUserQuotaTargetsAvailableBalanceWithLiveMoneyPostgres(t *testing.T) {
	for _, example := range []struct {
		settled bool
		target  billing.MicroUSD
	}{{false, 500}, {true, 500}, {false, 800}, {true, 800}} {
		settled, target := example.settled, example.target
		t.Run(fmt.Sprintf("uncommitted_settlement_%t_target_%d", settled, target), func(t *testing.T) {
			db, _, engine := absoluteQuotaPostgresFixture(t)
			ctx := context.Background()
			hold := ledger.Input{RequestID: "absolute-quota-held", PayerID: 101, ActorID: 101, ProjectID: 201, TokenID: 301, Unlimited: true, Amount: 400, OccurredAt: time.Now().UTC()}
			if err := engine.Reserve(ctx, hold); err != nil {
				t.Fatal(err)
			}
			available := int64(600)
			if settled {
				if err := engine.Settle(ctx, hold, 300); err != nil {
					t.Fatal(err)
				}
				available = 700
			}
			wantDelta := target - billing.MicroUSD(available)
			before, err := engine.ReadBalances(ctx, []int{101})
			if err != nil || before[101] != available {
				t.Fatalf("fixture available=%v err=%v", before, err)
			}
			delta, err := SetUserQuotaWithLedger(101, target, WalletLedgerKindAdjustment, "absolute-target-test", "set-target", nil)
			if err != nil {
				t.Fatal(err)
			}
			if delta != wantDelta {
				t.Fatalf("absolute target used committed SQL instead of displayed available: delta=%d want=%d", delta, wantDelta)
			}
			after, err := engine.ReadBalances(ctx, []int{101})
			if err != nil || after[101] != int64(target) {
				t.Fatalf("absolute target ended at available=%v want=%d err=%v", after, target, err)
			}
			delta, err = SetUserQuotaWithLedger(101, target, WalletLedgerKindAdjustment, "absolute-target-test", "set-target", nil)
			if err != nil || delta != 0 {
				t.Fatalf("retry of same absolute target applied another change: delta=%d err=%v", delta, err)
			}
			var adjustments []WalletLedger
			if err := db.Where("ref_type = ?", "absolute-target-test").Find(&adjustments).Error; err != nil {
				t.Fatal(err)
			}
			if len(adjustments) != 1 || adjustments[0].DeltaMicrousd != wantDelta {
				t.Fatalf("absolute target permanent financial facts: %+v", adjustments)
			}
			if settled {
				event := ledger.Event{Input: hold, OperationID: hold.RequestID, Kind: "settle"}
				event.Amount = 300
				if err := engine.Storage.Apply(ctx, []ledger.Event{event}); err != nil {
					t.Fatal(err)
				}
			} else if err := engine.Release(ctx, hold); err != nil {
				t.Fatal(err)
			}
			var user User
			if err := db.Select("quota").First(&user, 101).Error; err != nil {
				t.Fatal(err)
			}
			wantCommitted := 1000 + wantDelta
			if settled {
				wantCommitted -= 300
			}
			if user.Quota != wantCommitted {
				t.Fatalf("later release/settlement lost absolute adjustment: committed=%d want=%d", user.Quota, wantCommitted)
			}
		})
	}
}

type absoluteQuotaSnapshotBarrier struct {
	ledger.Storage
	entered chan struct{}
	release chan struct{}
	blocked atomic.Bool
}

func (storage *absoluteQuotaSnapshotBarrier) Snapshot(ctx context.Context, input ledger.Input) (ledger.Snapshot, error) {
	if storage.blocked.CompareAndSwap(false, true) {
		close(storage.entered)
		select {
		case <-storage.release:
		case <-ctx.Done():
			return ledger.Snapshot{}, ctx.Err()
		}
	}
	return storage.Storage.Snapshot(ctx, input)
}

func TestSetUserQuotaFencesConcurrentMoneyBeforeSQLLockPostgres(t *testing.T) {
	db, _, engine := absoluteQuotaPostgresFixture(t)
	ctx := context.Background()
	hold := ledger.Input{RequestID: "absolute-concurrent-held", PayerID: 101, ActorID: 101, ProjectID: 201, TokenID: 301, Unlimited: true, Amount: 400, OccurredAt: time.Now().UTC()}
	if err := engine.Reserve(ctx, hold); err != nil {
		t.Fatal(err)
	}
	storage := &absoluteQuotaSnapshotBarrier{Storage: engine.Storage, entered: make(chan struct{}), release: make(chan struct{})}
	engine.Storage = storage
	type outcome struct {
		delta billing.MicroUSD
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		delta, err := SetUserQuotaWithLedger(101, 500, WalletLedgerKindAdjustment, "absolute-concurrent-test", "target", nil)
		result <- outcome{delta, err}
	}()
	finished := false
	defer func() {
		if !finished {
			close(storage.release)
			<-result
		}
	}()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("absolute target did not reach financial snapshot")
	}
	// The financial snapshot may borrow a separate SQL connection. Its caller
	// must acquire the payer fence before locking this row to avoid inversion.
	if err := db.Transaction(func(tx *gorm.DB) error {
		var user User
		return tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "NOWAIT"}).Select("id").First(&user, 101).Error
	}); err != nil {
		t.Fatalf("absolute target held a SQL row lock while waiting on financial snapshot: %v", err)
	}
	if err := engine.Settle(ctx, hold, 300); !errors.Is(err, ledger.ErrRebuilding) {
		t.Fatalf("concurrent settlement crossed absolute target fence: %v", err)
	}
	if _, err := SetUserQuotaWithLedger(101, 800, WalletLedgerKindAdjustment, "absolute-concurrent-test", "competing-target", nil); !errors.Is(err, ledger.ErrRebuilding) {
		t.Fatalf("concurrent absolute target crossed financial fence: %v", err)
	}
	reservationContext, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	other := hold
	other.RequestID, other.Amount = "absolute-concurrent-new-reservation", 50
	if err := engine.Reserve(reservationContext, other); !errors.Is(err, ledger.ErrRebuilding) && !errors.Is(err, context.DeadlineExceeded) && !(errors.Is(err, ledger.ErrUnavailable) && errors.Is(reservationContext.Err(), context.DeadlineExceeded)) {
		t.Fatalf("concurrent reservation crossed financial fence: %v", err)
	}
	close(storage.release)
	actual := <-result
	finished = true
	if actual.err != nil || actual.delta != -100 {
		t.Fatalf("absolute target result: delta=%d err=%v", actual.delta, actual.err)
	}
	balances, err := engine.ReadBalances(ctx, []int{101})
	if err != nil || balances[101] != 500 {
		t.Fatalf("concurrent operations changed target: balances=%v err=%v", balances, err)
	}
	if err := engine.Release(ctx, hold); err != nil {
		t.Fatal(err)
	}
}
