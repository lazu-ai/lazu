package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/ledger"
)

func TestMediaAdmissionIsPrivateAndOwnsVerifiedState(t *testing.T) {
	snapshot := &IdentitySnapshot{
		Token: &Token{Id: 7}, Actor: &UserBase{Id: 8}, Payer: &UserBase{Id: 9},
		Generations: []IdentityGeneration{{Key: "gen:ephemeral:11", Value: "verified"}},
		Budgets:     []SpendBudget{{ID: 12}},
	}
	rates := []ledger.RateLimit{{Key: "token:7", Cost: 1, Limit: 2}}
	job := MediaJob{}
	job.SetAdmission(snapshot, rates)
	snapshot.Token.Id, snapshot.Actor.Id, snapshot.Payer.Id = 70, 80, 90
	snapshot.Generations[0].Value, snapshot.Budgets[0].ID, rates[0].Cost = "changed", 120, 20
	verified := job.admission.snapshot
	if verified.Token.Id != 7 || verified.Actor.Id != 8 || verified.Payer.Id != 9 || verified.Generations[0].Value != "verified" || verified.Budgets[0].ID != 12 || job.admission.rates[0].Cost != 1 {
		t.Fatal("media admission retained mutable middleware state")
	}
	body, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "admission") || strings.Contains(string(body), "gen:ephemeral") || strings.Contains(string(body), "verified") {
		t.Fatalf("private admission exposed in customer JSON: %s", body)
	}
	var forged MediaJob
	if err := json.Unmarshal([]byte(`{"admission":{"snapshot":{"Token":{"id":7}}},"LedgerInputJSON":"forged","ledger_input_json":"forged","request":{"_billing_reserved_quota_microusd":900}}`), &forged); err != nil {
		t.Fatal(err)
	}
	if forged.admission != nil || forged.LedgerInputJSON != "" {
		t.Fatal("customer JSON forged server financial admission")
	}
}
