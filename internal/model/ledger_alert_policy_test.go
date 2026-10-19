package model

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
)

// An unchanged drift used to mail every hour until its period ended (budget
// 90 sent 100 identical mails). It now mails once a day; a changed drift
// still mails within the hour.
func TestLedgerAlertRepeatsUnchangedFindingDaily(t *testing.T) {
	client, _ := consoleBalanceRedis(t)
	ctx := context.Background()
	ledgerAlertNotifier.RLock()
	previous := ledgerAlertNotifier.send
	ledgerAlertNotifier.RUnlock()
	t.Cleanup(func() { SetLedgerAlertNotifier(previous) })
	var sent []LedgerAlertNotice
	fail := false
	SetLedgerAlertNotifier(func(notice LedgerAlertNotice) error {
		if fail {
			return errors.New("mail provider down")
		}
		sent = append(sent, notice)
		return nil
	})
	lookups := 0
	finding := func(signature string) LedgerAlert {
		return LedgerAlert{Key: "budget:90", Message: "budget_id=90 " + signature, Signature: signature, Facts: func(context.Context) []LedgerAlertFact {
			lookups++
			return []LedgerAlertFact{{Label: "Key", Value: "Seedream launch verification (#1000903)"}}
		}}
	}
	hourPasses := func() { client.Del(ctx, ledger.Prefix+"alert:budget:90") }
	dayPasses := func() { client.Del(ctx, ledger.Prefix+"alert:budget:90", ledger.Prefix+"alert_seen:budget:90") }

	notifyLedgerFinding(ctx, finding("drift=22"))
	notifyLedgerFinding(ctx, finding("drift=22"))
	if len(sent) != 1 || len(sent[0].Facts) != 1 || lookups != 1 {
		t.Fatalf("first finding sent=%d lookups=%d", len(sent), lookups)
	}
	hourPasses()
	notifyLedgerFinding(ctx, finding("drift=22"))
	if len(sent) != 1 || lookups != 1 {
		t.Fatalf("unchanged drift repeated within a day: sent=%d lookups=%d", len(sent), lookups)
	}
	notifyLedgerFinding(ctx, finding("drift=29"))
	if len(sent) != 2 {
		t.Fatalf("changed drift was not sent after the hourly throttle: sent=%d", len(sent))
	}
	notifyLedgerFinding(ctx, finding("drift=35"))
	if len(sent) != 2 {
		t.Fatalf("changed drift beat the hourly throttle: sent=%d", len(sent))
	}
	dayPasses()
	notifyLedgerFinding(ctx, finding("drift=29"))
	if len(sent) != 3 {
		t.Fatalf("unchanged drift was not repeated after a day: sent=%d", len(sent))
	}
	if ttl := client.TTL(ctx, ledger.Prefix+"alert_seen:budget:90").Val(); ttl < 23*time.Hour {
		t.Fatalf("finding remembered for %s, want a day", ttl)
	}

	// A failed delivery releases the claim so the next tick retries.
	dayPasses()
	fail = true
	notifyLedgerFinding(ctx, finding("drift=40"))
	fail = false
	notifyLedgerFinding(ctx, finding("drift=40"))
	if len(sent) != 4 {
		t.Fatalf("failed delivery was not retried: sent=%d", len(sent))
	}
}

func TestLedgerBudgetAlertFactsNameTheKeyAndTheDirectRows(t *testing.T) {
	db := setupFileModelTestDB(t)
	if err := db.AutoMigrate(&Project{}, &SpendBudgetCharge{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Project{ID: 1, OwnerUserID: 1, Name: "root"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Token{Id: 1000903, UserId: 1, Key: "facts-key", Name: "Seedream launch verification"}).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	budget := ledger.Budget{ID: 90, ProjectID: 1, Scope: SpendBudgetScopeToken, TokenID: 1000903, Period: SpendBudgetPeriodMonth, Start: start.Unix(), End: start.AddDate(0, 1, 0).Unix()}
	at := start.Add(9 * 24 * time.Hour)
	for i, amount := range []int64{5, 3, 10, 4} {
		row := SpendBudgetCharge{BudgetID: 90, ProjectID: 1, Scope: SpendBudgetScopeToken, TokenID: 1000903, RequestID: "file_storage_upload:" + string(rune('a'+i)), AmountMicrousd: billing.MicroUSD(amount), CreatedAt: at}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&SpendBudgetCharge{BudgetID: 90, ProjectID: 1, TokenID: 1000903, RequestID: "settled", LedgerOperationID: "settle:x", AmountMicrousd: billing.MicroUSD(1000), CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}

	facts := ledgerBudgetAlertFacts(context.Background(), budget, 824500, 824522, 0, 0)
	text := map[string]string{}
	for _, fact := range facts {
		text[fact.Label] = fact.Value
	}
	want := map[string]string{
		"Key":             "Seedream launch verification (#1000903)",
		"项目":              "root (#1)",
		"Redis 已花":        "$0.824500",
		"数据库已花":           "$0.824522",
		"差额（数据库 − Redis）": "+$0.000022",
		"未经账本写入的 SQL 记录":  "4 行 · $0.000022",
	}
	for label, value := range want {
		if text[label] != value {
			t.Fatalf("fact %q = %q, want %q (all: %v)", label, text[label], value, facts)
		}
	}
	if _, ok := text["Redis 预留"]; ok {
		t.Fatalf("equal reservations were reported: %v", facts)
	}
	if !strings.HasPrefix(text["预算"], "#90 · month · 2026-10-01") {
		t.Fatalf("budget fact = %q", text["预算"])
	}
}

func TestLedgerAlertUSD(t *testing.T) {
	for micro, want := range map[int64]string{0: "$0.000000", 22: "$0.000022", 824522: "$0.824522", -1_500_000: "-$1.500000"} {
		if got := ledgerAlertUSD(micro); got != want {
			t.Fatalf("ledgerAlertUSD(%d) = %q, want %q", micro, got, want)
		}
	}
	if got := ledgerAlertDrift(824522, 824500); got != "-$0.000022" {
		t.Fatalf("negative drift = %q", got)
	}
}
