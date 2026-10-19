package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func reserveRedisMediaJob(job *MediaJob, result MediaJobBillingResult, now time.Time) (MediaJobBillingResult, error) {
	tokenID := 0
	if job.TokenID != nil {
		tokenID = *job.TokenID
	}
	if tokenID <= 0 {
		return result, errors.New("paid media job has no validated token")
	}
	if job.admission == nil || GatewayLedger() == nil {
		return result, ErrMediaJobAdmissionMissing
	}
	snapshot := &job.admission.snapshot
	if err := snapshot.Validate(now); err != nil {
		return result, err
	}
	principal := snapshot.Principal
	if snapshot.Token == nil || snapshot.Token.Id != tokenID || principal.PayerUserID != job.BillingUserID() || principal.ActorUserID != job.ActorID() || principal.ProjectID != job.ProjectID {
		return result, errors.New("media job billing principal mismatch")
	}
	key := snapshot.Token
	gens := snapshot.Generations
	budgets := snapshot.Budgets
	// RequestSnapshot contains customer input. Neither its reservation ID nor
	// its apparent admission timestamp authorizes an existing financial hold.
	reservationID := "media:" + job.PublicID
	if job.PublicID == "" {
		reservationID = "media:" + common.GenerateRequestID()
	}
	input := ledger.Input{RequestID: reservationID, ReferenceRequestID: job.RequestID, PayerID: job.BillingUserID(), ActorID: job.ActorID(), ProjectID: job.ProjectID, TokenID: tokenID, Unlimited: key.UnlimitedQuota, Amount: int64(result.CustomerCharge), PricingVersion: result.CustomerQuote.PricingVersion, Model: job.ModelName, SessionID: job.SessionID, AgentID: job.AgentID, OccurredAt: now, ExpiresAt: now.Add(45 * time.Minute).UnixMilli(), Budgets: LedgerBudgets(budgets, now)}
	input.AllowZeroBalance = result.CustomerPriceConfigured && result.CustomerCharge == 0
	if job.LedgerInputJSON != "" {
		previous, err := mediaRedisInput(*job)
		if err != nil {
			return result, err
		}
		input = previous
		reservationID = previous.RequestID
		result = mediaJobBillingResultFromReservation(*job, billing.MicroUSD(previous.Amount))
		result.CostSnapshot = buildMediaJobCostSnapshot(result)
	}
	input.Generations = nil
	input.WalletGeneration, input.TokenGeneration = "", ""
	policy := ""
	for _, g := range gens {
		input.Generations = append(input.Generations, ledger.Generation{Key: g.Key, PendingKey: g.PendingKey, Value: g.Value})
		if g.Key == fmt.Sprintf("gen:user:%d", input.PayerID) {
			input.WalletGeneration = g.Value
		}
		if g.Key == fmt.Sprintf("gen:token:%d", tokenID) {
			input.TokenGeneration = g.Value
		}
		if g.Key == "gen:policy" {
			policy = g.Value
		}
	}
	input.WalletGeneration += ":" + policy
	input.TokenGeneration += ":" + policy
	input.Rates = append([]ledger.RateLimit(nil), job.admission.rates...)
	if err := GatewayLedger().Reserve(context.Background(), input); err != nil {
		return result, err
	}
	attached := false
	defer func() {
		if attached {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := GatewayLedger().Release(ctx, input); err != nil {
			if spoolErr := GatewayLedger().PersistSettlement(input, 0, "release"); spoolErr != nil {
				common.SysError("unattached media reservation release: " + errors.Join(err, spoolErr).Error())
			}
		}
	}()
	raw, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	job.LedgerInputJSON = string(raw)
	if _, err := setMediaJobBillingReservationWithID(job, result.CustomerCharge, reservationID, input.OccurredAt); err != nil {
		return result, err
	}
	job.CostSnapshot = result.CostSnapshot
	if job.ID > 0 {
		r := CoreDB().Model(&MediaJob{}).Where("id = ? AND status IN ?", job.ID, []string{MediaJobStatusPending, MediaJobStatusRunning}).Updates(map[string]interface{}{"ledger_input_json": job.LedgerInputJSON, "request_snapshot": job.RequestSnapshot, "cost_snapshot": job.CostSnapshot})
		if r.Error != nil {
			return result, r.Error
		}
		if r.RowsAffected != 1 {
			return result, ErrMediaJobAlreadyCompleted
		}
	}
	attached = true
	return result, nil
}
func mediaRedisInput(job MediaJob) (ledger.Input, error) {
	var input ledger.Input
	if job.LedgerInputJSON == "" {
		return input, ErrMediaJobLedgerInputMissing
	}
	if err := json.Unmarshal([]byte(job.LedgerInputJSON), &input); err != nil {
		return input, err
	}
	tokenID := 0
	if job.TokenID != nil {
		tokenID = *job.TokenID
	}
	if input.PayerID != job.BillingUserID() || input.ActorID != job.ActorID() || input.ProjectID != job.ProjectID || input.TokenID != tokenID {
		return input, errors.New("media job ledger identity conflict")
	}
	return input, nil
}
func settleRedisMediaJob(job MediaJob, now time.Time) (MediaJobBillingResult, error) {
	input, err := mediaRedisInput(job)
	if err != nil {
		return MediaJobBillingResult{}, err
	}
	amount := billing.MicroUSD(input.Amount)
	result := mediaJobBillingResultFromReservation(job, amount)
	result.CostSnapshot = buildMediaJobCostSnapshot(result)
	if job.BillingSettledAt > 0 {
		result.AlreadySettled = true
		return result, nil
	}
	envelope, err := BuildConsumeLogRelayEnvelope(buildMediaJobConsumeLog(job, result, now))
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := GatewayLedger().SettleWithTelemetry(ctx, input, int64(result.CustomerCharge), envelope); err != nil {
		if spoolErr := GatewayLedger().PersistSettlementWithTelemetry(input, int64(result.CustomerCharge), "settle", envelope); spoolErr != nil {
			return result, errors.Join(err, spoolErr)
		}
	}
	// A crash before this projection is marked simply repeats the stable Redis
	// operation. The worker cannot charge again.
	if err := CoreDB().Transaction(func(tx *gorm.DB) error { return markMediaJobBillingSettledTx(tx, job.ID, result, now) }); err != nil {
		return result, err
	}
	runMediaJobChargedHooks(job, result.CustomerCharge)
	return result, nil
}
func releaseRedisMediaJob(job MediaJob) error {
	input, err := mediaRedisInput(job)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := GatewayLedger().Release(ctx, input); err != nil {
		return GatewayLedger().PersistSettlement(input, 0, "release")
	}
	return nil
}
func ExtendRedisMediaReservations(ctx context.Context) error {
	if GatewayLedger() == nil {
		return nil
	}
	active := func() *gorm.DB {
		return CoreDB().WithContext(ctx).Model(&MediaJob{}).Where("ledger_input_json <> ? AND status IN ? AND billing_settled_at = 0", "", []string{MediaJobStatusPending, MediaJobStatusRunning})
	}
	var maximum uint
	if err := active().Select("COALESCE(MAX(id),0)").Scan(&maximum).Error; err != nil {
		return err
	}
	var last uint
	var failures error
	for last < maximum {
		var jobs []MediaJob
		if err := active().Where("id > ? AND id <= ?", last, maximum).Order("id ASC").Limit(500).Find(&jobs).Error; err != nil {
			return errors.Join(failures, err)
		}
		if len(jobs) == 0 {
			break
		}
		for _, job := range jobs {
			last = job.ID
			input, err := mediaRedisInput(job)
			if err == nil {
				err = GatewayLedger().Extend(ctx, input.RequestID, time.Now().Add(45*time.Minute))
			}
			if err != nil {
				failures = errors.Join(failures, fmt.Errorf("extend media job %s: %w", job.PublicID, err))
			}
			if ctx.Err() != nil {
				return errors.Join(failures, ctx.Err())
			}
		}
	}
	return failures
}
