package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

type SettlementOutboxReconcilerTask struct{}

type SettlementOutboxReconcileStats struct {
	Scanned      int
	Claimed      int
	Resolved     int
	Retried      int
	ManualReview int
}

func NewSettlementOutboxReconcilerTask() *SettlementOutboxReconcilerTask {
	return &SettlementOutboxReconcilerTask{}
}

func (t *SettlementOutboxReconcilerTask) Name() string {
	return "settlement_outbox_reconciler"
}

func (t *SettlementOutboxReconcilerTask) Interval() time.Duration {
	seconds := common.GetEnvOrDefault("SETTLEMENT_OUTBOX_RECONCILER_INTERVAL_SECONDS", 15)
	if seconds <= 0 {
		seconds = 15
	}
	return time.Duration(seconds) * time.Second
}

func (t *SettlementOutboxReconcilerTask) Run(ctx context.Context) error {
	if err := model.ReconcileDecisionGenerationRefunds(); err != nil {
		return err
	}
	if !common.GetEnvOrDefaultBool("SETTLEMENT_OUTBOX_ENABLED", false) {
		return nil
	}
	stats, err := ReconcileSettlementOutboxOnce(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	if stats.Resolved > 0 || stats.Retried > 0 || stats.ManualReview > 0 {
		common.SysLog(fmt.Sprintf("settlement outbox reconciler: scanned=%d claimed=%d resolved=%d retried=%d manual_review=%d",
			stats.Scanned, stats.Claimed, stats.Resolved, stats.Retried, stats.ManualReview))
	}
	return nil
}

func ReconcileSettlementOutboxOnce(ctx context.Context, now time.Time) (SettlementOutboxReconcileStats, error) {
	stats := SettlementOutboxReconcileStats{}
	if model.CoreDB() == nil {
		return stats, nil
	}
	if _, err := model.ReopenStaleSettlementOutboxClaims(now.Add(-settlementFailureClaimTimeout()), now); err != nil {
		return stats, err
	}
	rows, err := model.ListSettlementOutboxForRetry(settlementFailureReconcileLimit(), now)
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
		if waived, err := model.LegacySettlementWaiverBlocksOperation(ctx, model.CoreDB(), row.UserID, row.RefType, row.RefID, row.RequestID); err != nil {
			return stats, err
		} else if waived {
			continue
		}

		claimed, err := model.ClaimSettlementOutboxForRetry(row.ID, now)
		if err != nil {
			return stats, err
		}
		if !claimed {
			continue
		}
		stats.Claimed++
		row.Attempts++

		outcome, err := reconcileSettlementOutbox(row, now)
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

func reconcileSettlementOutbox(row model.SettlementOutbox, now time.Time) (string, error) {
	if waived, err := model.LegacySettlementWaiverBlocksOperation(context.Background(), model.CoreDB(), row.UserID, row.RefType, row.RefID, row.RequestID); err != nil {
		return "", err
	} else if waived {
		return model.SettlementFailureStatusManualReview, nil
	}
	resolve := func() (string, error) {
		if err := finalizeProjectSettlement(row.ProjectID, row.UserID, row.ActorUserID, row.TokenID, row.RequestID, row.QuotaDelta+row.PreConsumed, row.CreatedAt, row.LedgerMetadata()); err != nil {
			return scheduleSettlementOutboxRetry(row, now, err)
		}
		return model.SettlementFailureStatusResolved, model.ResolveSettlementOutbox(row.ID, now)
	}

	exists, err := model.WalletLedgerEventExists(row.UserID, row.RefType, row.RefID)
	if err != nil {
		return scheduleSettlementOutboxRetry(row, now, err)
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
		return model.SettlementFailureStatusManualReview, model.MarkSettlementOutboxManualReview(row.ID, err.Error(), now)
	}
	return scheduleSettlementOutboxRetry(row, now, err)
}

func scheduleSettlementOutboxRetry(row model.SettlementOutbox, now time.Time, err error) (string, error) {
	nextRetryAt := now.Add(settlementFailureBackoff(row.Attempts))
	return model.SettlementFailureStatusOpen, model.ScheduleSettlementOutboxRetry(row.ID, err.Error(), nextRetryAt, now)
}
