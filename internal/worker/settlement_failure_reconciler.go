package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

type SettlementFailureReconcilerTask struct{}

type SettlementFailureReconcileStats struct {
	Scanned      int
	Claimed      int
	Resolved     int
	Retried      int
	ManualReview int
}

func NewSettlementFailureReconcilerTask() *SettlementFailureReconcilerTask {
	return &SettlementFailureReconcilerTask{}
}

func (t *SettlementFailureReconcilerTask) Name() string {
	return "settlement_failure_reconciler"
}

func (t *SettlementFailureReconcilerTask) Interval() time.Duration {
	seconds := common.GetEnvOrDefault("SETTLEMENT_FAILURE_RECONCILER_INTERVAL_SECONDS", 30)
	if seconds <= 0 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func (t *SettlementFailureReconcilerTask) Run(ctx context.Context) error {
	if !common.GetEnvOrDefaultBool("SETTLEMENT_FAILURE_RECONCILER_ENABLED", true) {
		return nil
	}
	stats, err := ReconcileSettlementFailuresOnce(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	if stats.Resolved > 0 || stats.Retried > 0 || stats.ManualReview > 0 {
		common.SysLog(fmt.Sprintf("settlement failure reconciler: scanned=%d claimed=%d resolved=%d retried=%d manual_review=%d",
			stats.Scanned, stats.Claimed, stats.Resolved, stats.Retried, stats.ManualReview))
	}
	return nil
}

func ReconcileSettlementFailuresOnce(ctx context.Context, now time.Time) (SettlementFailureReconcileStats, error) {
	stats := SettlementFailureReconcileStats{}
	if model.CoreDB() == nil {
		return stats, nil
	}
	if _, err := model.ReopenStaleSettlementFailureClaims(now.Add(-settlementFailureClaimTimeout()), now); err != nil {
		return stats, err
	}
	rows, err := model.ListSettlementFailuresForRetry(settlementFailureReconcileLimit(), now)
	if err != nil {
		return stats, err
	}
	stats.Scanned = len(rows)
	for _, row := range rows {
		select {
		case <-ctx.Done():
			return stats, ctx.Err()
		default:
		}

		claimed, err := model.ClaimSettlementFailureForRetry(row.ID, now)
		if err != nil {
			return stats, err
		}
		if !claimed {
			continue
		}
		stats.Claimed++
		row.Attempts++

		outcome, err := reconcileSettlementFailure(row, now)
		if err != nil {
			return stats, err
		}
		switch outcome {
		case model.SettlementFailureStatusResolved:
			stats.Resolved++
		case model.SettlementFailureStatusOpen:
			stats.Retried++
		case model.SettlementFailureStatusManualReview:
			stats.ManualReview++
		}
	}
	return stats, nil
}

func reconcileSettlementFailure(row model.SettlementFailure, now time.Time) (string, error) {
	if waived, err := model.LegacySettlementFailureWaiverBlocksReplay(context.Background(), model.CoreDB(), row); err != nil {
		return "", err
	} else if waived {
		return model.SettlementFailureStatusManualReview, nil
	}
	resolve := func() (string, error) {
		if err := finalizeProjectSettlement(row.ProjectID, row.UserID, row.ActorUserID, row.TokenID, row.RequestID, row.QuotaDelta+row.PreConsumed, row.CreatedAt, row.LedgerMetadata()); err != nil {
			return scheduleSettlementFailureRetry(row, now, err)
		}
		return model.SettlementFailureStatusResolved, model.ResolveSettlementFailure(row.ID, now)
	}

	exists, err := model.WalletLedgerEventExists(row.UserID, row.RefType, row.RefID)
	if err != nil {
		return scheduleSettlementFailureRetry(row, now, err)
	}
	if exists {
		return resolve()
	}

	err = model.AdjustRelayQuotaWithLedger(model.AdjustRelayQuotaWithLedgerInput{
		AcceptedSettlement: true,
		UserID:             row.UserID,
		TokenID:            row.TokenID,
		TokenKey:           row.TokenKey,
		TokenUnlimited:     row.TokenUnlimited,
		IsPlayground:       row.IsPlayground,
		QuotaDelta:         row.QuotaDelta,
		RefType:            row.RefType,
		RefID:              row.RefID,
		PricingVersion:     row.PricingVersion,
		Metadata:           row.LedgerMetadata(),
	})
	if err == nil || isWalletLedgerDuplicateError(err) {
		return resolve()
	}
	if row.QuotaDelta < 0 && isSettlementFailureMissingTokenError(err) {
		refundErr := model.AdjustUserQuotaWithLedger(model.AdjustUserQuotaWithLedgerInput{
			UserID:         row.UserID,
			DeltaQuota:     -row.QuotaDelta,
			Kind:           model.WalletLedgerKindRefund,
			RefType:        row.RefType,
			RefID:          row.RefID,
			PricingVersion: row.PricingVersion,
			Metadata:       row.LedgerMetadata(),
		})
		if refundErr == nil || isWalletLedgerDuplicateError(refundErr) {
			return resolve()
		}
		err = refundErr
	}
	if isSettlementFailureManualReviewError(err) || row.Attempts >= settlementFailureMaxAttempts() {
		return model.SettlementFailureStatusManualReview, model.MarkSettlementFailureManualReview(row.ID, err.Error(), now)
	}
	return scheduleSettlementFailureRetry(row, now, err)
}

func scheduleSettlementFailureRetry(row model.SettlementFailure, now time.Time, err error) (string, error) {
	nextRetryAt := now.Add(settlementFailureBackoff(row.Attempts))
	return model.SettlementFailureStatusOpen, model.ScheduleSettlementFailureRetry(row.ID, err.Error(), nextRetryAt, now)
}

func settlementFailureReconcileLimit() int {
	limit := common.GetEnvOrDefault("SETTLEMENT_FAILURE_RECONCILER_BATCH_SIZE", 50)
	if limit <= 0 {
		return 50
	}
	return limit
}

func settlementFailureMaxAttempts() int {
	attempts := common.GetEnvOrDefault("SETTLEMENT_FAILURE_RECONCILER_MAX_ATTEMPTS", 10)
	if attempts <= 0 {
		return 10
	}
	return attempts
}

func settlementFailureClaimTimeout() time.Duration {
	seconds := common.GetEnvOrDefault("SETTLEMENT_FAILURE_RECONCILER_CLAIM_TIMEOUT_SECONDS", 300)
	if seconds <= 0 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

func settlementFailureBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	baseSeconds := common.GetEnvOrDefault("SETTLEMENT_FAILURE_RECONCILER_BACKOFF_SECONDS", 30)
	if baseSeconds <= 0 {
		baseSeconds = 30
	}
	maxSeconds := common.GetEnvOrDefault("SETTLEMENT_FAILURE_RECONCILER_MAX_BACKOFF_SECONDS", 3600)
	if maxSeconds < baseSeconds {
		maxSeconds = baseSeconds
	}
	shift := attempts - 1
	if shift > 6 {
		shift = 6
	}
	delaySeconds := baseSeconds * (1 << shift)
	if delaySeconds > maxSeconds {
		delaySeconds = maxSeconds
	}
	return time.Duration(delaySeconds) * time.Second
}

func isSettlementFailureManualReviewError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "quota is not enough") ||
		isSettlementFailureMissingTokenError(err)
}

func isSettlementFailureMissingTokenError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "token not found") ||
		strings.Contains(msg, "token id or token key is required")
}

func isWalletLedgerDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	hasDuplicateMarker := strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicated key") ||
		strings.Contains(msg, "error 1062")
	hasWalletLedgerMarker := strings.Contains(msg, "wallet_ledgers") ||
		strings.Contains(msg, "wallet_ledger") ||
		strings.Contains(msg, "idx_wallet_ledger")
	return hasDuplicateMarker && hasWalletLedgerMarker
}
