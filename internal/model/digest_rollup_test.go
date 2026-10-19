package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func TestDigestRollupFeedsTheDigestAndIsRepeatable(t *testing.T) {
	setupMailTestDB(t)
	if err := coreDB.AutoMigrate(&Log{}, &DigestUsageHour{}, &AgentSession{}, &Option{}); err != nil {
		t.Fatal(err)
	}
	oldLog := telemetryDB
	telemetryDB = coreDB
	t.Cleanup(func() { telemetryDB = oldLog })

	owner := User{Username: "owner", Quota: 50_000_000}
	member := User{Username: "member"}
	coreDB.Create(&owner)
	coreDB.Create(&member)
	sh, _ := time.LoadLocation("Asia/Shanghai")
	day := time.Date(2026, 9, 21, 10, 0, 0, 0, sh)
	logs := []Log{
		// Own request, official price on record.
		{PublicID: "a", UserId: owner.Id, CreatedAt: day.Unix(), Type: LogTypeConsume, AmountMicrousd: billing.MicroUSD(2_000_000), PromptTokens: 100, CompletionTokens: 50, ModelName: "m1", ClientFramework: "claude-code",
			LineItems: `{"line_items":[],"total_microusd":2000000,"official":{"total_microusd":3000000}}`},
		// A team member's request billed to the owner.
		{PublicID: "b", UserId: member.Id, PayerUserID: owner.Id, CreatedAt: day.Add(26 * time.Hour).Unix(), Type: LogTypeConsume, AmountMicrousd: billing.MicroUSD(1_000_000), ModelName: "m2", ClientFramework: "curl"},
		{PublicID: "c", UserId: owner.Id, CreatedAt: day.Unix() + 60, Type: LogTypeError, ModelName: "m1"},
	}
	for i := range logs {
		if err := coreDB.Create(&logs[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	from, to := day.Add(-24*time.Hour), day.Add(72*time.Hour)
	for i := 0; i < 2; i++ {
		if err := RollupDigestHours(from, to); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	coreDB.Model(&DigestUsageHour{}).Count(&count)
	if count != 3 {
		t.Fatalf("rollup rows = %d, want 3 (m1 claude-code, m1 api error, m2 api)", count)
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
		t.Cleanup(func() {
			common.OptionMapRWMutex.Lock()
			common.OptionMap = nil
			common.OptionMapRWMutex.Unlock()
		})
	}
	common.OptionMapRWMutex.Unlock()
	if err := UpdateOption(DigestRollupSinceOption, "1"); err != nil {
		t.Fatal(err)
	}
	week := DigestPeriod{Start: time.Date(2026, 9, 21, 0, 0, 0, 0, sh), End: time.Date(2026, 9, 28, 0, 0, 0, 0, sh)}
	prev := DigestPeriod{Start: week.Start.AddDate(0, 0, -7), End: week.Start}
	usage, err := CollectDigestUsage(owner.Id, week, prev, true)
	if err != nil {
		t.Fatal(err)
	}
	c := usage.Current
	if c.Spend != 3_000_000 || c.Requests != 2 || c.Errors != 1 || c.Official != 3_000_000 || c.Compared != 2_000_000 {
		t.Fatalf("current = %+v", c)
	}
	if usage.Days[0].Spend != 2_000_000 || usage.Days[1].Spend != 1_000_000 {
		t.Fatalf("days = %+v", usage.Days[:2])
	}
	if usage.CurrentMissing || usage.PreviousMissing || usage.Wallet != 50_000_000 {
		t.Fatalf("flags/wallet = %+v", usage)
	}
	if len(usage.Sources) != 2 || usage.Sources[0].Key != "claude-code" || usage.Sources[1].Key != "api" {
		t.Fatalf("sources = %+v", usage.Sources)
	}
}

func TestDigestCandidatesIncludeManualTopUps(t *testing.T) {
	setupMailTestDB(t)
	if err := coreDB.AutoMigrate(&WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	online := User{Username: "online", Email: "online@example.com", HasToppedUp: true, Status: 1}
	manual := User{Username: "manual", Email: "manual@example.com", Status: 1}
	bonusOnly := User{Username: "bonus", Email: "bonus@example.com", Status: 1}
	noEmail := User{Username: "noemail", HasToppedUp: true, Status: 1}
	for _, u := range []*User{&online, &manual, &bonusOnly, &noEmail} {
		if err := coreDB.Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []WalletLedger{
		{UserID: manual.Id, Kind: WalletLedgerKindAdjustment, DeltaMicrousd: billing.MicroUSD(100_000_000)},
		{UserID: bonusOnly.Id, Kind: WalletLedgerKindBonus, DeltaMicrousd: billing.MicroUSD(1_000_000)},
	} {
		if err := coreDB.Create(&l).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListDigestCandidates()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int]bool{}
	for _, c := range got {
		ids[c.Id] = true
	}
	if !ids[online.Id] || !ids[manual.Id] || ids[bonusOnly.Id] || ids[noEmail.Id] || len(ids) != 2 {
		t.Fatalf("candidates = %v", ids)
	}
}
