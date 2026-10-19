package model

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type ledgerBatchSQLCapture struct {
	gormlogger.Interface
	statements []string
}

func (s *ledgerBatchSQLCapture) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	statement, _ := query()
	s.statements = append(s.statements, statement)
}

func postgresLedgerBatchFixture(t *testing.T) (*gorm.DB, *ledgerBatchSQLCapture) {
	t.Helper()
	core, _ := postgresTelemetryFixture(t)
	if err := core.AutoMigrate(&Token{}, &RedisLedgerOperation{}, &WalletLedger{}, &ProjectSpend{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetAlert{}); err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&[]User{{Id: 1, Username: "batch-owner-one", Quota: 1000, UsedQuota: 10}, {Id: 2, Username: "batch-owner-two", Quota: 1000, UsedQuota: 10}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&[]Token{{Id: 2, UserId: 1, Key: common.GenerateRequestID(), RemainQuota: 700, UsedQuota: 20}, {Id: 3, UserId: 2, Key: common.GenerateRequestID(), RemainQuota: 800, UsedQuota: 30}, {Id: 4, UserId: 1, Key: common.GenerateRequestID(), RemainQuota: 400, UnlimitedQuota: true}}).Error; err != nil {
		t.Fatal(err)
	}
	capture := &ledgerBatchSQLCapture{Interface: gormlogger.Default.LogMode(gormlogger.Silent)}
	db := core.Session(&gorm.Session{Logger: capture})
	SetCoreDB(db)
	return db, capture
}

func ledgerBatchEvent(id string, payer, tokenID, project int, amount int64, at time.Time) ledger.Event {
	return ledger.Event{Input: ledger.Input{RequestID: id, PayerID: payer, ActorID: payer, TokenID: tokenID, ProjectID: project, Amount: amount, OccurredAt: at}, OperationID: id, Kind: "settle"}
}

func assertFreshLedgerBatchStatements(t *testing.T, statements []string) {
	t.Helper()
	operationReads, operationInserts, userUpdates, tokenUpdates := 0, 0, 0, 0
	for _, statement := range statements {
		upper := strings.ToUpper(statement)
		if strings.HasPrefix(upper, "SELECT") && strings.Contains(upper, "REDIS_LEDGER_OPERATIONS") {
			operationReads++
		}
		if strings.HasPrefix(upper, "INSERT INTO REDIS_LEDGER_OPERATIONS") && strings.Contains(upper, "RETURNING ID") {
			operationInserts++
		}
		if strings.Contains(upper, "UPDATE USERS U SET") {
			userUpdates++
		}
		if strings.Contains(upper, "UPDATE TOKENS T SET") {
			tokenUpdates++
		}
	}
	if operationReads != 0 || operationInserts != 1 || userUpdates != 1 || tokenUpdates != 1 {
		t.Fatalf("fresh batch did per-operation reads/counter writes: operations reads=%d inserts=%d users=%d keys=%d", operationReads, operationInserts, userUpdates, tokenUpdates)
	}
}

func TestPostgresLedgerBatch500FreshOperationsUseConstantCounterStatements(t *testing.T) {
	db, capture := postgresLedgerBatchFixture(t)
	at := time.Now().UTC()
	events := make([]ledger.Event, 500)
	for i := range events {
		events[i] = ledgerBatchEvent(fmt.Sprintf("fixture-batch-%03d", i), 1, 2, 7, 1, at.Add(time.Duration(i)*time.Nanosecond))
	}
	if err := (redisLedgerStorage{}).Apply(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	assertFreshLedgerBatchStatements(t, capture.statements)
	var user User
	var key Token
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&key, 2).Error; err != nil {
		t.Fatal(err)
	}
	if user.Quota != 500 || user.UsedQuota != 510 || key.RemainQuota != 200 || key.UsedQuota != 520 || key.LastUsedAt != events[len(events)-1].OccurredAt.Unix() {
		t.Fatalf("500 fresh operations lost aggregate money: user=%+v key=%+v", user, key)
	}
	for _, entity := range []interface{}{&RedisLedgerOperation{}, &WalletLedger{}} {
		var count int64
		if err := db.Model(entity).Count(&count).Error; err != nil || count != 500 {
			t.Fatalf("missing permanent batch facts %T count=%d err=%v", entity, count, err)
		}
	}
	var daily ProjectSpend
	if err := db.First(&daily).Error; err != nil || daily.AmountMicrousd != 500 {
		t.Fatalf("project aggregate=%+v err=%v", daily, err)
	}
}

func TestPostgresLedgerBatchReplayConflictAndAtomicFailures(t *testing.T) {
	db, capture := postgresLedgerBatchFixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	first := ledgerBatchEvent("fixture-batch-limited", 1, 2, 7, 50, at)
	first.Budgets = []ledger.Budget{{ID: 31, Start: at.Add(-time.Hour).Unix(), End: at.Add(time.Hour).Unix(), Limit: 1000, Block: true, ProjectID: 7, TokenID: 2}}
	unlimited := ledgerBatchEvent("fixture-batch-unlimited", 1, 4, 7, 30, at.Add(time.Second))
	unlimited.Unlimited = true
	other := ledgerBatchEvent("fixture-batch-other", 2, 3, 8, 20, at.Add(2*time.Second))
	refund := ledgerBatchEvent("fixture-batch-refund", 1, 2, 7, -10, at.Add(3*time.Second))
	refund.Kind = "refund"
	events := []ledger.Event{first, unlimited, other, refund}
	if err := (redisLedgerStorage{}).Apply(ctx, events); err != nil {
		t.Fatal(err)
	}
	assertFreshLedgerBatchStatements(t, capture.statements)
	// Snapshot both counters and permanent/derived facts for every subsequent
	// rejection, including new inserts before a conflicting historical ID.
	snapshot := func() string {
		t.Helper()
		var users []User
		var tokens []Token
		var ops []RedisLedgerOperation
		var wallet []WalletLedger
		var spend []ProjectSpend
		for _, query := range []struct {
			target interface{}
			order  string
		}{{&users, "id"}, {&tokens, "id"}, {&ops, "id"}, {&wallet, "id"}, {&spend, "event_key"}} {
			if err := db.Order(query.order).Find(query.target).Error; err != nil {
				t.Fatal(err)
			}
		}
		raw, err := json.Marshal([]interface{}{users, tokens, ops, wallet, spend})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	var users []User
	var tokens []Token
	db.Order("id").Find(&users)
	db.Order("id").Find(&tokens)
	if users[0].Quota != 930 || users[0].UsedQuota != 80 || users[1].Quota != 980 || users[1].UsedQuota != 30 || tokens[0].RemainQuota != 660 || tokens[0].UsedQuota != 60 || tokens[0].LastUsedAt != refund.OccurredAt.Unix() || tokens[1].RemainQuota != 780 || tokens[1].UsedQuota != 50 || tokens[2].RemainQuota != 400 || tokens[2].UsedQuota != 30 {
		t.Fatal("mixed payer/key/refund/unlimited batch counters were not exact")
	}
	before := snapshot()
	replay := append([]ledger.Event(nil), events...)
	for i := range replay {
		replay[i].OccurredAt = replay[i].OccurredAt.In(time.FixedZone("fixture-offset", 3600))
		replay[i].Rates = []ledger.RateLimit{}
		replay[i].Generations = []ledger.Generation{}
		replay[i].TelemetryEnvelope = `{"transient":"different presentation"}`
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := (redisLedgerStorage{}).Apply(ctx, replay); err != nil {
			t.Fatalf("canonical replay attempt %d: %v", attempt, err)
		}
	}
	if snapshot() != before {
		t.Fatal("triple replay changed permanent or derived financial facts")
	}
	for _, scenario := range []struct {
		name string
		edit func(*ledger.Event)
	}{
		{"same-amount-wrong-actor", func(e *ledger.Event) { e.ActorID = 99 }},
		{"same-amount-wrong-budget", func(e *ledger.Event) {
			e.Budgets = append([]ledger.Budget(nil), e.Budgets...)
			e.Budgets[0].Limit++
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			conflict := first
			scenario.edit(&conflict)
			fresh := ledgerBatchEvent("fixture-fresh-before-conflict-"+scenario.name, 1, 2, 7, 1, at)
			if err := (redisLedgerStorage{}).Apply(ctx, []ledger.Event{fresh, conflict}); err == nil {
				t.Fatal("historical financial conflict accepted")
			}
			if snapshot() != before {
				t.Fatal("historical conflict did not roll back new inserts and counters")
			}
			fresh.RequestID, fresh.OperationID = "fixture-same-batch-conflict", "fixture-same-batch-conflict"
			conflict = fresh
			conflict.ActorID = 99
			if err := (redisLedgerStorage{}).Apply(ctx, []ledger.Event{fresh, conflict}); err == nil {
				t.Fatal("same-batch conflicting identity was silently dropped")
			}
			if snapshot() != before {
				t.Fatal("same-batch conflict changed money")
			}
		})
	}
	t.Run("tampered-permanent-input", func(t *testing.T) {
		id := ledgerOperationID(first.OperationID, first.Kind)
		var stored RedisLedgerOperation
		if err := db.First(&stored, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		bad := first
		bad.Amount++
		_, raw, err := canonicalLedgerFinancialEvent(bad)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&RedisLedgerOperation{}).Where("id = ?", id).Update("input", raw).Error; err != nil {
			t.Fatal(err)
		}
		if err := (redisLedgerStorage{}).Apply(ctx, []ledger.Event{first}); err == nil {
			t.Fatal("tampered permanent JSON was trusted")
		}
		if err := db.Model(&RedisLedgerOperation{}).Where("id = ?", id).Update("input", stored.Input).Error; err != nil {
			t.Fatal(err)
		}
		if snapshot() != before {
			t.Fatal("rejecting tampered JSON changed money")
		}
	})
	for _, missing := range []struct {
		name         string
		payer, token int
	}{{"missing-payer", 99, 2}, {"missing-key", 1, 99}} {
		t.Run(missing.name, func(t *testing.T) {
			valid := ledgerBatchEvent("fixture-valid-before-"+missing.name, 1, 2, 7, 1, at)
			invalid := ledgerBatchEvent("fixture-"+missing.name, missing.payer, missing.token, 7, 1, at)
			if err := (redisLedgerStorage{}).Apply(ctx, []ledger.Event{valid, invalid}); err == nil {
				t.Fatal("missing counter identity did not abort transaction")
			}
			if snapshot() != before {
				t.Fatal("missing identity left partial inserts or money updates")
			}
		})
	}
	for _, overflow := range []struct {
		name, column string
		entity       interface{}
		id           int
		value        int64
	}{
		{"wallet-underflow", "quota", &User{}, 1, math.MinInt64 + 5},
		{"user-used-overflow", "used_quota", &User{}, 1, math.MaxInt64 - 5},
		{"key-remaining-underflow", "remain_quota", &Token{}, 2, math.MinInt64 + 5},
		{"key-used-overflow", "used_quota", &Token{}, 2, math.MaxInt64 - 5},
	} {
		t.Run(overflow.name, func(t *testing.T) {
			var original int64
			if err := db.Model(overflow.entity).Where("id = ?", overflow.id).Pluck(overflow.column, &original).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(overflow.entity).Where("id = ?", overflow.id).Update(overflow.column, billing.MicroUSD(overflow.value)).Error; err != nil {
				t.Fatal(err)
			}
			configured := snapshot()
			event := ledgerBatchEvent("fixture-"+overflow.name, 1, 2, 7, 10, at)
			if err := (redisLedgerStorage{}).Apply(ctx, []ledger.Event{event}); err == nil {
				t.Fatal("SQL bigint range violation was accepted")
			}
			if snapshot() != configured {
				t.Fatal("SQL overflow did not roll back entire financial transaction")
			}
			if err := db.Model(overflow.entity).Where("id = ?", overflow.id).Update(overflow.column, billing.MicroUSD(original)).Error; err != nil {
				t.Fatal(err)
			}
			if snapshot() != before {
				t.Fatal("overflow fixture cleanup did not restore unchanged ledger")
			}
		})
	}
}
