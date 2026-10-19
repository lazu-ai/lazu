package model

import (
	"errors"

	"github.com/lazu-ai/lazu/internal/ledger"
)

var ErrMediaJobAdmissionMissing = errors.New("media job has no verified server admission")
var ErrMediaJobLedgerInputMissing = errors.New("media job has no durable Redis financial reservation")
var ErrMediaJobBillingAdmission = errors.New("media job financial admission failed")

// This holder is Go-only: neither GORM nor customer request JSON can populate
// it. The identity is the one middleware actually verified, including an
// ephemeral credential's expiry and generations, and its prepared rate buckets.
type mediaJobAdmission struct {
	snapshot IdentitySnapshot
	rates    []ledger.RateLimit
}

func cloneMediaJobAdmission(snapshot *IdentitySnapshot, rates []ledger.RateLimit) *mediaJobAdmission {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	copy.Generations = append([]IdentityGeneration(nil), snapshot.Generations...)
	copy.Budgets = append([]SpendBudget(nil), snapshot.Budgets...)
	if snapshot.Token != nil {
		token := *snapshot.Token
		copy.Token = &token
	}
	if snapshot.Actor != nil {
		actor := *snapshot.Actor
		copy.Actor = &actor
	}
	if snapshot.Payer != nil {
		payer := *snapshot.Payer
		copy.Payer = &payer
	}
	return &mediaJobAdmission{snapshot: copy, rates: append([]ledger.RateLimit(nil), rates...)}
}

func (input *CreateMediaJobInput) SetAdmission(snapshot *IdentitySnapshot, rates []ledger.RateLimit) {
	input.admission = cloneMediaJobAdmission(snapshot, rates)
}

func (job *MediaJob) SetAdmission(snapshot *IdentitySnapshot, rates []ledger.RateLimit) {
	job.admission = cloneMediaJobAdmission(snapshot, rates)
}

func modernMediaBilling() bool {
	return GatewayLedgerEnabled() || CoreDB() != nil && CoreDB().Dialector.Name() == "postgres"
}
