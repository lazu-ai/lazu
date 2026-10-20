package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MediaJobBillingResult struct {
	UsageFact               usagepkg.Fact
	CustomerQuote           billing.QuoteResult
	ProviderQuote           billing.QuoteResult
	CostSnapshot            JSONValue
	ChannelGroup            string
	CustomerPriceConfigured bool
	CustomerCharge          billing.MicroUSD
	ProviderCost            billing.MicroUSD
	AlreadySettled          bool
	// RequoteCapped is set when the provider's report priced the job above
	// mediaJobRequoteMaxFactor times its hold; the hold was settled instead.
	RequoteCapped         bool
	RequoteQuotedMicrousd billing.MicroUSD
}

// mediaJobRequoteMaxFactor bounds how far a provider's report may move a
// settlement above its hold. A report beyond it (a cost in the wrong unit, a
// runaway token count) settles at the hold and the operator is told.
const mediaJobRequoteMaxFactor = 2

var ErrMediaJobPriceNotConfigured = errors.New("media job price not configured")
var ErrMediaJobDurationRequired = errors.New("media job duration required")
var ErrMediaJobBillingReservationUnverified = errors.New("media job billing reservation is not backed by a ledger entry")

const (
	mediaJobBillingReservationAmountKey = "_billing_reserved_quota_microusd"
	mediaJobBillingReservationIDKey     = "_billing_reservation_id"
	mediaJobBillingReservationRefType   = "media_job_reservation"
	mediaJobBillingRefundRefType        = "media_job_reservation_refund"
)

func SettleMediaJobBilling(job MediaJob, now time.Time) (MediaJobBillingResult, error) {
	now = normalizeMediaJobTime(now)
	if modernMediaBilling() {
		if job.LedgerInputJSON == "" {
			return MediaJobBillingResult{}, ErrMediaJobLedgerInputMissing
		}
		if GatewayLedger() == nil {
			return MediaJobBillingResult{}, ledger.ErrUnavailable
		}
		return settleRedisMediaJob(job, now)
	}
	source := mediaJobSource(job)
	reservedQuota := MediaJobBillingReservationAmount(job)
	var result MediaJobBillingResult
	var quoteErr error
	if reservedQuota > 0 {
		verified, verificationErr := mediaJobBillingReservationActive(job, reservedQuota)
		if verificationErr != nil {
			return mediaJobBillingResultFromReservation(job, reservedQuota), verificationErr
		}
		if !verified {
			return mediaJobBillingResultFromReservation(job, reservedQuota), ErrMediaJobBillingReservationUnverified
		}
		// The delta between this result and the hold settles below.
		reserved := mediaJobBillingResultFromReservation(job, reservedQuota)
		result = settlementMediaJobBilling(job, reserved, reservedQuota, reserved.CustomerQuote.PricingVersion, mediaJobBudgetOccurredAt(job, now))
	} else {
		result, quoteErr = quoteMediaJobBilling(job)
	}
	result.CostSnapshot = buildMediaJobCostSnapshot(result)
	if quoteErr != nil {
		return result, quoteErr
	}
	if CoreDB() == nil {
		if !result.CustomerPriceConfigured {
			return result, fmt.Errorf("%w: model=%s group=%s", ErrMediaJobPriceNotConfigured, job.ModelName, result.ChannelGroup)
		}
		return result, nil
	}
	tokenID, tokenKey := mediaJobTokenContext(job)
	chargeInput := SpendBudgetChargeInput{
		ProjectID: job.ProjectID, PayerUserID: job.BillingUserID(), ActorUserID: job.ActorID(),
		UserID:        job.BillingUserID(),
		TokenID:       tokenID,
		SessionID:     job.SessionID,
		AgentID:       job.AgentID,
		RequestID:     mediaJobSpendBudgetRequestID(job),
		LongRunning:   true,
		DeltaMicrousd: result.CustomerCharge,
		OccurredAt:    mediaJobBudgetOccurredAt(job, now),
	}
	ledgerInput := AdjustRelayQuotaWithLedgerInput{
		AcceptedSettlement: true,
		UserID:             job.BillingUserID(),
		TokenID:            tokenID,
		TokenKey:           tokenKey,
		QuotaDelta:         result.CustomerCharge,
		RefType:            MediaJobRefType,
		RefID:              job.PublicID,
		PricingVersion:     result.CustomerQuote.PricingVersion,
		Metadata: map[string]any{
			"source":              source,
			"media_job_id":        job.PublicID,
			"kind":                job.Kind,
			"model":               job.ModelName,
			"session_id":          job.SessionID,
			"agent_id":            job.AgentID,
			"request_id":          job.RequestID,
			"usage_fact":          result.UsageFact,
			"line_items":          result.CustomerQuote.LineItems,
			"provider_line_items": result.ProviderQuote.LineItems,
			"provider_cost_usd":   result.UsageFact.ProviderCostUSD,
		},
	}
	if reservedQuota > 0 {
		ledgerInput.QuotaDelta -= reservedQuota
		ledgerInput.Metadata["reserved_quota_microusd"] = reservedQuota
	}
	metadata := cloneLedgerMetadata(ledgerInput.Metadata)
	metadata["quota_delta"] = ledgerInput.QuotaDelta
	userCacheSnapshot := captureUserQuotaCacheSnapshot(ledgerInput.UserID)
	tokenCacheSnapshot := captureTokenQuotaCacheSnapshot(ledgerInput.TokenKey)
	tokenCacheKey := ledgerInput.TokenKey
	charged := false
	alreadySettled := false
	settledNow := false

	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var locked MediaJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", job.ID).
			First(&locked).Error; err != nil {
			return err
		}
		if locked.BillingSettledAt > 0 {
			alreadySettled = true
			result.AlreadySettled = true
			return nil
		}
		ledgerExists, err := mediaJobLedgerExistsTx(tx, locked.PublicID)
		if err != nil {
			return err
		}
		if ledgerExists {
			alreadySettled = true
			result.AlreadySettled = true
			if err := markMediaJobBillingSettledTx(tx, locked.ID, result, now); err != nil {
				common.SysLog("failed to mark media job billing settled after existing ledger: " + err.Error())
			}
			if result.CustomerCharge > 0 {
				if err := recordSpendBudgetChargeTx(tx, chargeInput); err != nil {
					return err
				}
				if err := releaseSpendBudgetReservationTx(tx, chargeInput); err != nil {
					return err
				}
			}
			return nil
		}

		if !result.CustomerPriceConfigured {
			return fmt.Errorf("%w: model=%s group=%s", ErrMediaJobPriceNotConfigured, job.ModelName, result.ChannelGroup)
		}
		if result.CustomerCharge <= 0 && ledgerInput.QuotaDelta == 0 {
			if err := markMediaJobBillingSettledTx(tx, locked.ID, result, now); err != nil {
				return err
			}
			settledNow = true
			return nil
		}

		// A verified reservation admitted this job before provider execution.
		// Budget changes must not prevent accounting for that accepted work.
		if result.CustomerCharge > 0 && reservedQuota <= 0 {
			var blockErr error
			if err := checkSpendBudgetBeforeChargeTx(tx, chargeInput, &blockErr); err != nil {
				return err
			}
			if blockErr != nil {
				return blockErr
			}
		}

		if ledgerInput.QuotaDelta != 0 {
			kind := WalletLedgerKindSpend
			if ledgerInput.QuotaDelta < 0 {
				kind = WalletLedgerKindRefund
			}
			resolvedTokenCacheKey, err := adjustRelayQuotaWithLedgerTx(tx, ledgerInput, kind, metadata)
			if err != nil {
				return err
			}
			tokenCacheKey = resolvedTokenCacheKey
			charged = true
		}
		if err := markMediaJobBillingSettledTx(tx, locked.ID, result, now); err != nil {
			return err
		}
		if result.CustomerCharge > 0 {
			if err := recordSpendBudgetChargeTx(tx, chargeInput); err != nil {
				return err
			}
			if err := releaseSpendBudgetReservationTx(tx, chargeInput); err != nil {
				return err
			}
		}
		settledNow = true
		return nil
	})
	if err != nil {
		return result, err
	}
	if charged {
		updateRelayQuotaCachesAfterCommit(ledgerInput.UserID, tokenCacheKey, ledgerInput.QuotaDelta, userCacheSnapshot, tokenCacheSnapshot)
	}
	if settledNow {
		updateMediaJobUsageCounters(job, result)
	}
	if alreadySettled && result.CustomerCharge > 0 {
		if err := RecordSpendBudgetCharge(chargeInput); err != nil {
			return result, err
		}
	}
	if settledNow || alreadySettled {
		recordMediaJobConsumeLog(job, result, now)
		runMediaJobChargedHooks(job, result.CustomerCharge)
	}
	return result, nil
}

// settlementMediaJobBilling decides what a held job settles at, on both
// ledgers. The hold is final unless the provider reported a billing
// dimension of its own (Ark tokens, an aggregator's cost, the delivered
// length). Then the job is re-quoted on the sheet as it stood at admission,
// and the fresh quote replaces the hold only when it prices every dimension,
// comes from the same price version as the hold, is not zero against a real
// hold, and stays within mediaJobRequoteMaxFactor of it.
func settlementMediaJobBilling(job MediaJob, reserved MediaJobBillingResult, hold billing.MicroUSD, holdVersion int64, admittedAt time.Time) MediaJobBillingResult {
	if !MediaJobHasReportedFacts(job) {
		return reserved
	}
	quoted, err := quoteMediaJobBillingAt(job, admittedAt)
	switch {
	case err != nil || !quoted.CustomerPriceConfigured:
		return reserved
	case len(quoted.CustomerQuote.MissingPriceDimensions) > 0:
		return reserved
	case holdVersion == 0 || quoted.CustomerQuote.PricingVersion != holdVersion:
		// The lane was repriced while the job ran; it pays what it was quoted.
		return reserved
	case hold > 0 && quoted.CustomerCharge <= 0:
		return reserved
	case hold > 0 && quoted.CustomerCharge > hold*mediaJobRequoteMaxFactor:
		common.SysError(fmt.Sprintf("media job settled at its hold: job=%s model=%s hold_microusd=%d reported_microusd=%d", job.PublicID, job.ModelName, hold, quoted.CustomerCharge))
		reserved.RequoteCapped = true
		reserved.RequoteQuotedMicrousd = quoted.CustomerCharge
		return reserved
	}
	if quoted.ChannelGroup == "" {
		quoted.ChannelGroup = reserved.ChannelGroup
	}
	return quoted
}

func ReserveMediaJobBillingBeforeSubmit(job *MediaJob, now time.Time) (MediaJobBillingResult, error) {
	if job == nil {
		return MediaJobBillingResult{}, errors.New("media job is required")
	}
	now = normalizeMediaJobTime(now)
	// Settlement re-quotes on the sheet as it stood at this admission.
	result, quoteErr := quoteMediaJobBillingAt(*job, now)
	result.CostSnapshot = buildMediaJobCostSnapshot(result)
	if quoteErr != nil {
		return result, quoteErr
	}
	if !result.CustomerPriceConfigured {
		return result, fmt.Errorf("%w: model=%s group=%s", ErrMediaJobPriceNotConfigured, job.ModelName, result.ChannelGroup)
	}
	if job.Kind == MediaJobKindVideo && result.CustomerCharge <= 0 && len(result.CustomerQuote.MissingPriceDimensions) > 0 {
		// The lane prices nothing of this estimate (a provider-cost lane
		// without a per-second price, say), so the hold would be zero and the
		// clip free whenever the provider leaves its cost out. A lane that is
		// free on purpose has no missing dimension and still passes.
		return result, fmt.Errorf("%w: model=%s group=%s missing=%s", ErrMediaJobPriceNotConfigured, job.ModelName, result.ChannelGroup, strings.Join(result.CustomerQuote.MissingPriceDimensions, ","))
	}
	if modernMediaBilling() {
		return reserveRedisMediaJob(job, result, now)
	}
	if result.CustomerCharge <= 0 {
		if err := CheckProjectPaymentAdmission(job.ProjectID); err != nil {
			return result, err
		}
		job.CostSnapshot = result.CostSnapshot
		return result, nil
	}
	if CoreDB() == nil {
		job.CostSnapshot = result.CostSnapshot
		return result, nil
	}
	reservationID, err := setMediaJobBillingReservationWithID(job, result.CustomerCharge, common.GeneratePublicID("mres"), now)
	if err != nil {
		return result, err
	}
	tokenID, tokenKey := mediaJobTokenContext(*job)
	chargeInput := SpendBudgetChargeInput{
		ProjectID: job.ProjectID, PayerUserID: job.BillingUserID(), ActorUserID: job.ActorID(),
		UserID:        job.BillingUserID(),
		TokenID:       tokenID,
		SessionID:     job.SessionID,
		AgentID:       job.AgentID,
		RequestID:     mediaJobSpendBudgetRequestID(*job),
		LongRunning:   true,
		DeltaMicrousd: result.CustomerCharge,
		OccurredAt:    mediaJobBudgetOccurredAt(*job, now),
	}
	metadata := map[string]any{
		"source":       mediaJobSource(*job),
		"media_job":    job.PublicID,
		"model":        job.ModelName,
		"kind":         job.Kind,
		"request_id":   job.RequestID,
		"reservation":  reservationID,
		"quota_amount": result.CustomerCharge,
	}
	var tokenCacheKey string
	var blockErr error
	checkSpendBudget := spendBudgetTablesReady(CoreDB(), true, true, true)
	userCacheSnapshot := captureUserQuotaCacheSnapshot(job.BillingUserID())
	tokenCacheSnapshot := captureTokenQuotaCacheSnapshot(tokenKey)
	err = CoreDB().Transaction(func(tx *gorm.DB) error {
		if err := checkProjectPaymentHoldTx(tx, job.ProjectID); err != nil {
			return err
		}
		if checkSpendBudget {
			if err := checkSpendBudgetBeforeChargeTx(tx, chargeInput, &blockErr); err != nil {
				return err
			}
			if blockErr != nil {
				// Commit the blocked-attempt alert without reserving quota or
				// mutating the durable media job.
				return nil
			}
		}
		resolvedTokenCacheKey, err := adjustRelayQuotaWithLedgerTx(tx, AdjustRelayQuotaWithLedgerInput{
			UserID:         job.BillingUserID(),
			TokenID:        tokenID,
			TokenKey:       tokenKey,
			QuotaDelta:     result.CustomerCharge,
			RefType:        mediaJobBillingReservationRefType,
			RefID:          reservationID,
			PricingVersion: result.CustomerQuote.PricingVersion,
			Metadata:       metadata,
		}, WalletLedgerKindSpend, metadata)
		if err != nil {
			return err
		}
		tokenCacheKey = resolvedTokenCacheKey
		if job.ID > 0 {
			update := tx.Model(&MediaJob{}).
				Where("id = ? AND user_id = ? AND status IN ?", job.ID, job.UserID, []string{MediaJobStatusPending, MediaJobStatusRunning}).
				Updates(map[string]any{
					"request_snapshot": job.RequestSnapshot,
					"cost_snapshot":    result.CostSnapshot,
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected == 0 {
				return ErrMediaJobAlreadyCompleted
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if blockErr != nil {
		return result, blockErr
	}
	if tokenCacheKey == "" {
		tokenCacheKey = tokenKey
	}
	updateRelayQuotaCachesAfterCommit(job.BillingUserID(), tokenCacheKey, result.CustomerCharge, userCacheSnapshot, tokenCacheSnapshot)
	job.CostSnapshot = result.CostSnapshot
	return result, nil
}

func CarryMediaJobBillingReservation(target *MediaJob, source *MediaJob) error {
	if target == nil || source == nil {
		return errors.New("media job is required")
	}
	if target.admission == nil {
		target.admission = source.admission
	}
	target.LedgerInputJSON = source.LedgerInputJSON
	if target.ID > 0 && source.LedgerInputJSON != "" {
		if err := CoreDB().Model(&MediaJob{}).Where("id = ?", target.ID).Update("ledger_input_json", source.LedgerInputJSON).Error; err != nil {
			return err
		}
	}
	amount := MediaJobBillingReservationAmount(*source)
	reservationID := mediaJobBillingReservationID(*source)
	if amount > 0 {
		if reservationID == "" {
			return errors.New("media job billing reservation is missing")
		}
		if _, err := setMediaJobBillingReservationWithID(target, amount, reservationID, mediaJobBudgetOccurredAt(*source, source.CreatedAt)); err != nil {
			return err
		}
	}

	reserved := mediaJobBillingResultFromReservation(*source, amount)
	targetQuote, err := quoteMediaJobBilling(*target)
	if err != nil {
		return err
	}
	reserved.ProviderQuote = targetQuote.ProviderQuote
	reserved.ProviderCost = targetQuote.ProviderCost
	reserved.UsageFact.ChannelID = targetQuote.UsageFact.ChannelID
	reserved.UsageFact.ProviderFamily = targetQuote.UsageFact.ProviderFamily
	reserved.UsageFact.ProviderCostUSD = targetQuote.UsageFact.ProviderCostUSD
	reserved.UsageFact.GrossMarginUSD = nil
	if reserved.UsageFact.CustomerCostUSD != nil && reserved.UsageFact.ProviderCostUSD != nil {
		margin := *reserved.UsageFact.CustomerCostUSD - *reserved.UsageFact.ProviderCostUSD
		reserved.UsageFact.GrossMarginUSD = &margin
	}
	target.CostSnapshot = buildMediaJobCostSnapshot(reserved)
	return nil
}

func MediaJobBillingReservationAmount(job MediaJob) billing.MicroUSD {
	fields := mediaJobRequestFields(job)
	return billing.MicroUSD(firstPositiveInt64Field(fields, mediaJobBillingReservationAmountKey))
}

func RefundMediaJobBillingReservation(job MediaJob, now time.Time) error {
	if modernMediaBilling() {
		// A submission rejected before Reserve owns no financial hold.
		if job.LedgerInputJSON == "" {
			return nil
		}
		if GatewayLedger() == nil {
			return ledger.ErrUnavailable
		}
		return releaseRedisMediaJob(job)
	}
	recovered, err := recoverMediaJobBillingReservationFromLedger(job)
	if err != nil {
		return err
	}
	job = recovered
	defer ReleaseMediaJobSpendBudgetReservation(job)
	amount := MediaJobBillingReservationAmount(job)
	reservationID := mediaJobBillingReservationID(job)
	if amount <= 0 || reservationID == "" || CoreDB() == nil {
		return nil
	}
	verified, err := mediaJobBillingReservationVerified(job, amount)
	if err != nil {
		return err
	}
	if !verified {
		return ErrMediaJobBillingReservationUnverified
	}
	refundID := reservationID + ":refund"
	exists, err := mediaJobLedgerReferenceExists(mediaJobBillingRefundRefType, refundID, job.BillingUserID())
	if err != nil {
		return err
	}
	if !exists {
		tokenID, tokenKey := mediaJobTokenContext(job)
		if err := AdjustRelayQuotaWithLedger(AdjustRelayQuotaWithLedgerInput{
			AcceptedSettlement: true,
			UserID:             job.BillingUserID(),
			TokenID:            tokenID,
			TokenKey:           tokenKey,
			QuotaDelta:         -amount,
			RefType:            mediaJobBillingRefundRefType,
			RefID:              refundID,
			Metadata: map[string]any{
				"source":      mediaJobSource(job),
				"media_job":   job.PublicID,
				"model":       job.ModelName,
				"kind":        job.Kind,
				"request_id":  job.RequestID,
				"reservation": reservationID,
				"refunded_at": normalizeMediaJobTime(now).Unix(),
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func RefundMediaJobBillingReservationDurably(job MediaJob, now time.Time) error {
	if modernMediaBilling() {
		return RefundMediaJobBillingReservation(job, now)
	}
	recovered, recoverErr := recoverMediaJobBillingReservationFromLedger(job)
	if recoverErr != nil {
		return recoverErr
	}
	job = recovered
	refundErr := RefundMediaJobBillingReservation(job, now)
	if refundErr == nil {
		return nil
	}
	if errors.Is(refundErr, ErrMediaJobBillingReservationUnverified) {
		return refundErr
	}
	amount := MediaJobBillingReservationAmount(job)
	reservationID := mediaJobBillingReservationID(job)
	if amount <= 0 || reservationID == "" || CoreDB() == nil {
		return refundErr
	}
	tokenID, tokenKey := mediaJobTokenContext(job)
	tokenUnlimited := false
	if tokenID > 0 {
		if token, err := GetTokenById(tokenID); err == nil && token != nil {
			tokenUnlimited = token.UnlimitedQuota
			if tokenKey == "" {
				tokenKey = token.Key
			}
		}
	}
	costFields := map[string]any{}
	_ = common.Unmarshal(job.CostSnapshot, &costFields)
	pricingVersion, _ := int64FromAny(costFields["pricing_version"])
	refundID := reservationID + ":refund"
	metadata := common.MapToJsonStr(map[string]any{
		"source":      mediaJobSource(job),
		"media_job":   job.PublicID,
		"model":       job.ModelName,
		"kind":        job.Kind,
		"request_id":  job.RequestID,
		"reservation": reservationID,
		"refund_id":   refundID,
	})
	recordErr := RecordSettlementFailure(SettlementFailure{
		ProjectID: job.ProjectID, ActorUserID: job.ActorID(),
		RequestID:      mediaJobSpendBudgetRequestID(job),
		UserID:         job.BillingUserID(),
		TokenID:        tokenID,
		TokenKey:       tokenKey,
		TokenUnlimited: tokenUnlimited,
		QuotaDelta:     -amount,
		PreConsumed:    amount,
		RefType:        mediaJobBillingRefundRefType,
		RefID:          refundID,
		PricingVersion: pricingVersion,
		Error:          refundErr.Error(),
		LastError:      refundErr.Error(),
		Status:         SettlementFailureStatusOpen,
		Metadata:       metadata,
	})
	if recordErr != nil {
		return fmt.Errorf("refund media job billing reservation: %v; persist retry: %w", refundErr, recordErr)
	}
	return refundErr
}

func CheckMediaJobBillingBeforeArtifactFetch(job MediaJob, now time.Time) error {
	if modernMediaBilling() {
		if GatewayLedger() == nil {
			return ledger.ErrUnavailable
		}
		input, err := mediaRedisInput(job)
		if err != nil {
			return err
		}
		return GatewayLedger().Extend(context.Background(), input.RequestID, time.Now().Add(45*time.Minute))
	}
	now = normalizeMediaJobTime(now)
	result, quoteErr := quoteMediaJobBilling(job)
	if quoteErr != nil {
		return quoteErr
	}
	if CoreDB() == nil {
		if !result.CustomerPriceConfigured {
			return fmt.Errorf("%w: model=%s group=%s", ErrMediaJobPriceNotConfigured, job.ModelName, result.ChannelGroup)
		}
		return nil
	}
	if !result.CustomerPriceConfigured {
		return fmt.Errorf("%w: model=%s group=%s", ErrMediaJobPriceNotConfigured, job.ModelName, result.ChannelGroup)
	}
	if result.CustomerCharge <= 0 {
		return nil
	}
	if err := checkMediaJobQuotaBeforeCharge(job, result.CustomerCharge); err != nil {
		return err
	}
	tokenID, _ := mediaJobTokenContext(job)
	return CheckSpendBudgetBeforeCharge(SpendBudgetChargeInput{
		ProjectID: job.ProjectID, PayerUserID: job.BillingUserID(), ActorUserID: job.ActorID(),
		UserID:        job.BillingUserID(),
		TokenID:       tokenID,
		SessionID:     job.SessionID,
		AgentID:       job.AgentID,
		RequestID:     mediaJobSpendBudgetRequestID(job),
		LongRunning:   true,
		DeltaMicrousd: result.CustomerCharge,
		OccurredAt:    mediaJobBudgetOccurredAt(job, now),
	})
}

func checkMediaJobQuotaBeforeCharge(job MediaJob, charge billing.MicroUSD) error {
	if charge <= 0 || CoreDB() == nil {
		return nil
	}
	var userCount int64
	if err := CoreDB().Model(&User{}).Where("id = ? AND quota >= ?", job.BillingUserID(), charge).Count(&userCount).Error; err != nil {
		return err
	}
	if userCount == 0 {
		return fmt.Errorf("user quota is not enough, need quota: %d", charge)
	}
	if job.TokenID == nil || *job.TokenID <= 0 {
		return nil
	}
	var token Token
	if err := CoreDB().Select("id", "remain_quota", "unlimited_quota").First(&token, *job.TokenID).Error; err != nil {
		return err
	}
	if !token.UnlimitedQuota && token.RemainQuota < charge {
		return fmt.Errorf("token quota is not enough, need quota: %d", charge)
	}
	return nil
}

func setMediaJobBillingReservation(job *MediaJob, amount billing.MicroUSD) (string, error) {
	return setMediaJobBillingReservationWithID(job, amount, common.GeneratePublicID("mres"))
}

func setMediaJobBillingReservationWithID(job *MediaJob, amount billing.MicroUSD, reservationID string, admittedAt ...time.Time) (string, error) {
	if job == nil || amount < 0 || strings.TrimSpace(reservationID) == "" {
		return "", errors.New("invalid media job billing reservation")
	}
	fields := map[string]any{}
	if len(job.RequestSnapshot) > 0 {
		if err := common.Unmarshal(job.RequestSnapshot, &fields); err != nil {
			return "", err
		}
	}
	if len(admittedAt) > 0 && !admittedAt[0].IsZero() {
		// Always overwrite request-supplied values with the trusted admission.
		fields["_billing_budget_occurred_at"] = admittedAt[0].UTC().Format(time.RFC3339Nano)
	}
	fields[mediaJobBillingReservationAmountKey] = amount
	fields[mediaJobBillingReservationIDKey] = strings.TrimSpace(reservationID)
	body, err := common.Marshal(fields)
	if err != nil {
		return "", err
	}
	job.RequestSnapshot = JSONValue(body)
	return strings.TrimSpace(reservationID), nil
}

// Reservation admission time survives process retries and media job handoff.
func mediaJobBudgetOccurredAt(job MediaJob, fallback time.Time) time.Time {
	if MediaJobBillingReservationAmount(job) <= 0 {
		return fallback
	}
	raw := firstStringField(mediaJobRequestFields(job), "_billing_budget_occurred_at")
	if at, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return at.UTC()
	}
	return fallback
}

func mediaJobBillingReservationID(job MediaJob) string {
	return firstStringField(mediaJobRequestFields(job), mediaJobBillingReservationIDKey)
}

func mediaJobLedgerReferenceExists(refType string, refID string, userID int) (bool, error) {
	if CoreDB() == nil || strings.TrimSpace(refType) == "" || strings.TrimSpace(refID) == "" {
		return false, nil
	}
	var count int64
	err := CoreDB().Model(&WalletLedger{}).
		Where("user_id = ? AND ref_type = ? AND ref_id = ?", userID, refType, refID).
		Count(&count).Error
	return count > 0, err
}

func mediaJobBillingReservationVerified(job MediaJob, amount billing.MicroUSD) (bool, error) {
	if CoreDB() == nil || amount <= 0 {
		return false, nil
	}
	reservationID := mediaJobBillingReservationID(job)
	if reservationID == "" {
		return false, nil
	}
	var ledger WalletLedger
	result := CoreDB().Model(&WalletLedger{}).
		Select("delta_microusd").
		Where(
			"user_id = ? AND ref_type = ? AND ref_id = ?",
			job.BillingUserID(),
			mediaJobBillingReservationRefType,
			reservationID,
		).
		First(&ledger)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if result.Error != nil {
		return false, result.Error
	}
	return ledger.DeltaMicrousd == -amount, nil
}

func mediaJobBillingReservationActive(job MediaJob, amount billing.MicroUSD) (bool, error) {
	verified, err := mediaJobBillingReservationVerified(job, amount)
	if err != nil || !verified {
		return verified, err
	}
	reservationID := mediaJobBillingReservationID(job)
	if reservationID == "" {
		return false, nil
	}
	refunded, err := mediaJobLedgerReferenceExists(
		mediaJobBillingRefundRefType,
		reservationID+":refund",
		job.BillingUserID(),
	)
	if err != nil {
		return false, err
	}
	return !refunded, nil
}

func recoverMediaJobBillingReservationFromLedger(job MediaJob) (MediaJob, error) {
	if CoreDB() == nil || job.ID == 0 || job.BillingUserID() <= 0 || strings.TrimSpace(job.PublicID) == "" ||
		MediaJobBillingReservationAmount(job) > 0 {
		return job, nil
	}
	var ledgers []WalletLedger
	err := CoreDB().Model(&WalletLedger{}).
		Select("ref_id", "delta_microusd", "metadata").
		Where(
			"user_id = ? AND ref_type = ? AND metadata LIKE ?",
			job.BillingUserID(),
			mediaJobBillingReservationRefType,
			"%\"media_job\":\""+strings.ReplaceAll(job.PublicID, `"`, `\"`)+"\"%",
		).
		Order("id DESC").
		Find(&ledgers).Error
	if err != nil {
		return job, err
	}
	for _, ledger := range ledgers {
		var fields map[string]any
		if err := json.Unmarshal([]byte(ledger.Metadata), &fields); err != nil {
			continue
		}
		if firstStringField(fields, "media_job") != job.PublicID {
			continue
		}
		amount := billing.MicroUSD(firstPositiveInt64Field(fields, "quota_amount"))
		if amount <= 0 && ledger.DeltaMicrousd < 0 {
			amount = -ledger.DeltaMicrousd
		}
		reservationID := firstStringField(fields, "reservation")
		if reservationID == "" {
			reservationID = ledger.RefID
		}
		if amount <= 0 || strings.TrimSpace(reservationID) == "" {
			continue
		}
		if _, err := setMediaJobBillingReservationWithID(&job, amount, reservationID); err != nil {
			return job, err
		}
		update := CoreDB().Model(&MediaJob{}).
			Where("id = ? AND user_id = ? AND status IN ?", job.ID, job.UserID, []string{MediaJobStatusPending, MediaJobStatusRunning}).
			Update("request_snapshot", job.RequestSnapshot)
		if update.Error != nil {
			return job, update.Error
		}
		return job, nil
	}
	return job, nil
}

func updateMediaJobUsageCounters(job MediaJob, result MediaJobBillingResult) {
	if job.BillingUserID() > 0 {
		UpdateUserUsedQuota(job.BillingUserID(), result.CustomerCharge)
	}
}

func mediaJobLedgerExistsTx(tx *gorm.DB, publicID string) (bool, error) {
	if tx == nil || publicID == "" {
		return false, nil
	}
	var count int64
	if err := tx.Model(&WalletLedger{}).
		Where("ref_type = ? AND ref_id = ?", MediaJobRefType, publicID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func markMediaJobBillingSettledTx(tx *gorm.DB, jobID uint, result MediaJobBillingResult, now time.Time) error {
	if tx == nil || jobID == 0 {
		return nil
	}
	return tx.Model(&MediaJob{}).
		Where("id = ?", jobID).
		Updates(map[string]any{
			"billing_settled_at": now.Unix(),
			"cost_snapshot":      jsonValueOrDefault(result.CostSnapshot, "{}"),
		}).Error
}

func quoteMediaJobBilling(job MediaJob) (MediaJobBillingResult, error) {
	return quoteMediaJobBillingAt(job, time.Now())
}

// quoteMediaJobBillingAt prices the job on the sell price sheet as it stood
// at `at` (peak-hour adjustments are time-of-day dependent).
func quoteMediaJobBillingAt(job MediaJob, at time.Time) (MediaJobBillingResult, error) {
	channelID, channelGroup := mediaJobChannelContext(job)
	usageFact := mediaJobUsageFact(job, channelID)
	result := MediaJobBillingResult{
		UsageFact:    usageFact,
		ChannelGroup: channelGroup,
	}
	customerQuote := billing.QuoteResult{}
	customerPriceConfigured := false
	if sellPrice, ok := GetActiveModelSellPrice(job.ModelName, channelGroup); ok && mediaJobSellPriceSupportsJob(job, sellPrice) {
		customerPriceConfigured = true
		customerFact, err := mediaJobFactForPrice(job, usageFact, videoPriceModesOfSellPrice(sellPrice), true)
		if err != nil {
			result.CustomerPriceConfigured = true
			return result, err
		}
		usageFact = customerFact
		customerQuote = billing.Quote(billingUsageFromMediaFact(customerFact), sellPrice.ToBillingPriceSheetAt(at), nil)
	}
	providerQuote := billing.QuoteResult{}
	if channelID > 0 {
		if providerPrice, ok := GetActiveProviderCostPrice(job.ModelName, channelID); ok && mediaJobProviderCostSupportsJob(job, providerPrice) {
			providerFact, err := mediaJobFactForPrice(job, result.UsageFact, videoPriceModesOfProviderPrice(providerPrice), false)
			if err != nil {
				return result, err
			}
			providerQuote = billing.Quote(billingUsageFromMediaFact(providerFact), providerPrice.ToBillingPriceSheet(), nil)
		}
	}
	customerCharge := customerQuote.TotalMicrousd
	providerCost := providerQuote.TotalMicrousd
	if customerCharge > 0 {
		value := customerCharge.Dollars()
		usageFact.CustomerCostUSD = &value
	}
	if providerCost > 0 {
		value := providerCost.Dollars()
		usageFact.ProviderCostUSD = &value
	}
	if usageFact.CustomerCostUSD != nil && usageFact.ProviderCostUSD != nil {
		value := *usageFact.CustomerCostUSD - *usageFact.ProviderCostUSD
		usageFact.GrossMarginUSD = &value
	}
	usageFact = usageFact.Normalize()
	result.UsageFact = usageFact
	result.CustomerQuote = customerQuote
	result.ProviderQuote = providerQuote
	result.CustomerPriceConfigured = customerPriceConfigured
	result.CustomerCharge = customerCharge
	result.ProviderCost = providerCost
	return result, nil
}

// videoPriceModes says which video dimensions a price row can turn into money.
type videoPriceModes struct {
	seconds bool
	tokens  bool
	cost    bool
	request bool
}

func (m videoPriceModes) any() bool { return m.seconds || m.tokens || m.cost || m.request }

func videoPriceModesOfSellPrice(price ModelSellPrice) videoPriceModes {
	video := price.VideoPricing
	return videoPriceModes{
		seconds: price.SecondUSD != nil || (video != nil && (video.SecondUSD != nil || len(video.ResolutionSecondUSD) > 0)),
		tokens:  price.OutputPerMtok != nil,
		cost:    video != nil && (video.ProviderCostAuthoritative || video.ProviderCostMultiplier != nil),
		request: price.RequestUSD != nil,
	}
}

func videoPriceModesOfProviderPrice(price ProviderCostPrice) videoPriceModes {
	video := price.VideoPricing
	return videoPriceModes{
		seconds: price.SecondUSD != nil || (video != nil && (video.SecondUSD != nil || len(video.ResolutionSecondUSD) > 0)),
		tokens:  price.OutputPerMtok != nil,
		cost:    video != nil && (video.ProviderCostAuthoritative || video.ProviderCostMultiplier != nil),
		request: price.RequestUSD != nil,
	}
}

func mediaJobSellPriceSupportsJob(job MediaJob, price ModelSellPrice) bool {
	switch job.Kind {
	case MediaJobKindVideo:
		return videoPriceModesOfSellPrice(price).any()
	case MediaJobKindImage:
		return price.RequestUSD != nil
	default:
		return price.SecondUSD != nil || price.RequestUSD != nil
	}
}

func mediaJobProviderCostSupportsJob(job MediaJob, price ProviderCostPrice) bool {
	switch job.Kind {
	case MediaJobKindVideo:
		return videoPriceModesOfProviderPrice(price).any()
	case MediaJobKindImage:
		return price.RequestUSD != nil
	default:
		return price.SecondUSD != nil || price.RequestUSD != nil
	}
}

// mediaJobFactForPrice checks that the fact carries a dimension the price
// row can bill: a reported cost, a token count or a duration, in that order
// of preference (the same order videoLines prices them). A row that only
// prices requests gets a request dimension.
func mediaJobFactForPrice(job MediaJob, fact usagepkg.Fact, modes videoPriceModes, requireDuration bool) (usagepkg.Fact, error) {
	fact = fact.Normalize()
	if job.Kind != MediaJobKindVideo {
		return fact, nil
	}
	units := fact.MediaUnits
	if units == nil {
		units = &usagepkg.MediaUnits{}
	}
	switch {
	case modes.cost && units.ReportedCostUSD != nil && *units.ReportedCostUSD > 0:
		return fact, nil
	case modes.tokens && units.OutputTokens > 0:
		return fact, nil
	case modes.seconds:
		if units.VideoSeconds <= 0 {
			if !requireDuration {
				return fact, nil
			}
			return fact, fmt.Errorf("%w: model=%s", ErrMediaJobDurationRequired, job.ModelName)
		}
		return fact, nil
	case modes.tokens || modes.cost:
		// Priced on a provider figure that has not arrived yet (the estimate
		// carries tokens for Ark; an aggregator's cost comes with the result).
		if !requireDuration {
			return fact, nil
		}
		if units.OutputTokens > 0 || units.VideoSeconds > 0 {
			return fact, nil
		}
		return fact, fmt.Errorf("%w: model=%s", ErrMediaJobDurationRequired, job.ModelName)
	}
	fact.Dimensions = ensureMediaJobDimensionAtLeast(fact.Dimensions, usagepkg.DimensionRequest, 1, usagepkg.UnitRequest)
	return fact.Normalize(), nil
}

func ensureMediaJobDimensionAtLeast(dimensions []usagepkg.DimensionUsage, name string, quantity int64, unit string) []usagepkg.DimensionUsage {
	if name == "" || quantity <= 0 {
		return dimensions
	}
	for idx := range dimensions {
		if dimensions[idx].Name == name {
			if dimensions[idx].Quantity < quantity {
				dimensions[idx].Quantity = quantity
			}
			if dimensions[idx].Unit == "" {
				dimensions[idx].Unit = unit
			}
			return dimensions
		}
	}
	return append(dimensions, usagepkg.DimensionUsage{Name: name, Quantity: quantity, Unit: unit})
}

func mediaJobUsageFact(job MediaJob, channelID int) usagepkg.Fact {
	units := mediaJobMediaUnits(job)
	tokenID := 0
	if job.TokenID != nil {
		tokenID = *job.TokenID
	}
	fact := usagepkg.Fact{
		Version:        usagepkg.FactVersion,
		RequestID:      job.RequestID,
		SessionID:      job.SessionID,
		AgentID:        job.AgentID,
		Source:         mediaJobSource(job),
		UserID:         job.ActorID(),
		TokenID:        tokenID,
		ChannelID:      channelID,
		ModelName:      job.ModelName,
		ProviderFamily: strings.ToLower(strings.TrimSpace(job.Provider)),
		MediaUnits:     &units,
	}
	if job.Kind == MediaJobKindVideo && units.VideoSeconds <= 0 {
		fact.Dimensions = append(fact.Dimensions, usagepkg.DimensionUsage{Name: usagepkg.DimensionRequest, Quantity: 1, Unit: usagepkg.UnitRequest})
	}
	return fact.Normalize()
}

func mediaJobMediaUnits(job MediaJob) usagepkg.MediaUnits {
	fields := mediaJobRequestFields(job)
	units := usagepkg.MediaUnits{
		ResolutionTier: firstStringField(fields, "resolution_tier", "quality", "resolution", "size", "aspect_ratio", "aspectRatio"),
		ProviderUnit:   job.Kind,
	}
	switch job.Kind {
	case MediaJobKindImage:
		units.ImageCount = firstPositiveInt64Field(fields, "image_count", "imageCount", "n", "sample_count", "sampleCount")
		if units.ImageCount <= 0 {
			units.ImageCount = 1
		}
	case MediaJobKindVideo:
		units.VideoSeconds = firstPositiveInt64Field(fields, "video_seconds", "videoSeconds", "seconds", "duration_seconds", "durationSeconds", "duration")
		if facts, ok := MediaJobProviderFacts(job); ok {
			if facts.Seconds > 0 {
				units.VideoSeconds = facts.Seconds
			}
			if facts.Resolution != "" {
				units.ResolutionTier = facts.Resolution
			}
			units.OutputTokens = facts.OutputTokens
			units.ReportedCostUSD = facts.ProviderCostUSD
			units.FPS = facts.FPS
			units.Audio = facts.Audio
			units.Reported = facts.Reported
		}
	}
	return units
}

func mediaJobRequestFields(job MediaJob) map[string]any {
	if len(job.RequestSnapshot) == 0 {
		return nil
	}
	var fields map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(job.RequestSnapshot)))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return nil
	}
	promoteNestedMediaRequestFields(fields)
	return fields
}

func promoteNestedMediaRequestFields(fields map[string]any) {
	if len(fields) == 0 {
		return
	}
	nested, ok := fields["parameters"].(map[string]any)
	if !ok || len(nested) == 0 {
		return
	}
	for key, value := range nested {
		if _, exists := fields[key]; exists {
			continue
		}
		fields[key] = value
	}
}

func mediaJobChannelContext(job MediaJob) (int, string) {
	if job.ChannelID == nil || *job.ChannelID <= 0 {
		return 0, ChannelLaneDirect
	}
	selectedLane := mediaJobSelectedChannelLane(job)
	channel, err := GetChannelById(*job.ChannelID, true)
	if err != nil || channel == nil {
		if selectedLane != "" {
			return *job.ChannelID, selectedLane
		}
		return *job.ChannelID, ChannelLaneDirect
	}
	if selectedLane != "" {
		return channel.Id, selectedLane
	}
	return channel.Id, firstMediaJobChannelLane(channel)
}

func mediaJobSelectedChannelLane(job MediaJob) string {
	fields := mediaJobRequestFields(job)
	return normalizeMediaJobSnapshotLane(firstStringField(fields, "final_group", "channel_group", "using_group"))
}

func normalizeMediaJobSnapshotLane(raw string) string {
	for _, candidate := range strings.Split(raw, ",") {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" || candidate == "default" || candidate == "auto" {
			continue
		}
		lane := NormalizeChannelLane(candidate)
		if IsKnownChannelLane(lane) {
			return lane
		}
	}
	return ""
}

func firstMediaJobChannelLane(channel *Channel) string {
	if channel == nil {
		return ChannelLaneDirect
	}
	for _, group := range channel.GetGroups() {
		lane := NormalizeChannelLane(group)
		if IsKnownChannelLane(lane) {
			return lane
		}
	}
	return ChannelLaneDirect
}

func mediaJobTokenContext(job MediaJob) (int, string) {
	if job.TokenID == nil || *job.TokenID <= 0 {
		return 0, ""
	}
	token, err := GetTokenById(*job.TokenID)
	if err != nil || token == nil {
		return *job.TokenID, ""
	}
	return token.Id, token.Key
}

func mediaJobSpendBudgetRequestID(job MediaJob) string {
	if strings.TrimSpace(job.RequestID) != "" {
		return strings.TrimSpace(job.RequestID)
	}
	return strings.TrimSpace(job.PublicID)
}

func buildMediaJobCostSnapshot(result MediaJobBillingResult) JSONValue {
	body, err := common.Marshal(map[string]any{
		"source":                   result.UsageFact.Source,
		"pricing_version":          result.CustomerQuote.PricingVersion,
		"channel_group":            result.ChannelGroup,
		"usage_fact":               result.UsageFact,
		"customer_charge_microusd": result.CustomerCharge,
		"provider_cost_microusd":   result.ProviderCost,
		"customer_charge_usd":      result.UsageFact.CustomerCostUSD,
		"provider_cost_usd":        result.UsageFact.ProviderCostUSD,
		"gross_margin_usd":         result.UsageFact.GrossMarginUSD,
		"line_items":               result.CustomerQuote.LineItems,
		"provider_line_items":      result.ProviderQuote.LineItems,
	})
	if err != nil {
		return JSONValue([]byte("{}"))
	}
	return JSONValue(body)
}

func rewriteMediaJobCostSnapshotSource(snapshot JSONValue, source string) (JSONValue, error) {
	fields := map[string]any{}
	if len(strings.TrimSpace(string(snapshot))) > 0 {
		if err := common.Unmarshal(snapshot, &fields); err != nil {
			return nil, err
		}
	}
	fields["source"] = source
	if usageFact, ok := fields["usage_fact"].(map[string]any); ok {
		usageFact["source"] = source
	}
	body, err := common.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return JSONValue(body), nil
}

func mediaJobBillingResultFromReservation(job MediaJob, reservedQuota billing.MicroUSD) MediaJobBillingResult {
	type reservationSnapshot struct {
		ChannelGroup           string             `json:"channel_group"`
		PricingVersion         int64              `json:"pricing_version"`
		UsageFact              usagepkg.Fact      `json:"usage_fact"`
		CustomerChargeMicrousd billing.MicroUSD   `json:"customer_charge_microusd"`
		ProviderCostMicrousd   billing.MicroUSD   `json:"provider_cost_microusd"`
		CustomerChargeUSD      *float64           `json:"customer_charge_usd"`
		ProviderCostUSD        *float64           `json:"provider_cost_usd"`
		LineItems              []billing.LineItem `json:"line_items"`
		ProviderLineItems      []billing.LineItem `json:"provider_line_items"`
	}
	snapshot := reservationSnapshot{}
	_ = common.Unmarshal(job.CostSnapshot, &snapshot)

	usageFact := snapshot.UsageFact
	if usageFact.Version == 0 {
		channelID, _ := mediaJobChannelContext(job)
		usageFact = mediaJobUsageFact(job, channelID)
	}
	customerCharge := reservedQuota
	providerCost := snapshot.ProviderCostMicrousd
	if providerCost <= 0 && snapshot.ProviderCostUSD != nil {
		providerCost = billing.USD(*snapshot.ProviderCostUSD)
	}
	customerUSD := customerCharge.Dollars()
	usageFact.CustomerCostUSD = &customerUSD
	if providerCost > 0 {
		providerUSD := providerCost.Dollars()
		usageFact.ProviderCostUSD = &providerUSD
		grossMarginUSD := customerUSD - providerUSD
		usageFact.GrossMarginUSD = &grossMarginUSD
	}
	channelGroup := normalizeMediaJobSnapshotLane(snapshot.ChannelGroup)
	if channelGroup == "" {
		_, channelGroup = mediaJobChannelContext(job)
	}
	return MediaJobBillingResult{
		UsageFact: usageFact.Normalize(),
		CustomerQuote: billing.QuoteResult{
			LineItems:      snapshot.LineItems,
			TotalMicrousd:  customerCharge,
			PricingVersion: snapshot.PricingVersion,
		},
		ProviderQuote: billing.QuoteResult{
			LineItems:     snapshot.ProviderLineItems,
			TotalMicrousd: providerCost,
		},
		ChannelGroup:            channelGroup,
		CustomerPriceConfigured: true,
		CustomerCharge:          customerCharge,
		ProviderCost:            providerCost,
	}
}

func recordMediaJobConsumeLog(job MediaJob, result MediaJobBillingResult, now time.Time) {
	defer releaseMediaJobSpendBudgetReservation(job)
	if !common.LogConsumeEnabled || TelemetryDB() == nil {
		return
	}
	if mediaJobConsumeLogExists(job) {
		return
	}
	// The charge bypasses the batched consumer, so it adds its own usage
	// slice in the same transaction; usage pages read slices for whole hours.
	if err := TelemetryDB().Transaction(func(tx *gorm.DB) error {
		log := buildMediaJobConsumeLog(job, result, now)
		if err := tx.Create(log).Error; err != nil {
			return err
		}
		return recordUsageSlicesForLogTx(tx, log)
	}); err != nil {
		common.SysLog("failed to record media job consume log: " + err.Error())
	}
}

func buildMediaJobConsumeLog(job MediaJob, result MediaJobBillingResult, now time.Time) *Log {
	tokenID := 0
	if job.TokenID != nil {
		tokenID = *job.TokenID
	}
	channelID := 0
	if job.ChannelID != nil {
		channelID = *job.ChannelID
	}
	lineItems := common.GetJsonString(result.CustomerQuote.LineItems)
	return &Log{
		ProjectID: job.ProjectID, PayerUserID: job.BillingUserID(),
		UserId:         job.ActorID(),
		RequestID:      job.RequestID,
		CreatedAt:      now.Unix(),
		Type:           LogTypeConsume,
		Content:        fmt.Sprintf("%s media job %s %s", mediaJobSource(job), job.Kind, job.PublicID),
		ModelName:      job.ModelName,
		AmountMicrousd: result.CustomerCharge,
		PricingVersion: result.CustomerQuote.PricingVersion,
		LineItems:      lineItems,
		ChannelId:      channelID,
		TokenId:        tokenID,
		Group:          result.ChannelGroup,
		SessionID:      job.SessionID,
		AgentID:        job.AgentID,
		Other: common.MapToJsonStr(map[string]any{
			"source":       mediaJobSource(job),
			"media_job_id": job.PublicID,
			"kind":         job.Kind,
		}),
		UsageFact: result.UsageFact.JSONString(),
	}
}

func mediaJobConsumeLogExists(job MediaJob) bool {
	if TelemetryDB() == nil {
		return false
	}
	tx := TelemetryDB().Model(&Log{}).Where("type = ?", LogTypeConsume)
	if job.RequestID != "" {
		tx = tx.Where("request_id = ?", job.RequestID)
	} else if job.PublicID != "" {
		tx = tx.Where("content LIKE ?", "%"+job.PublicID+"%")
	} else {
		return false
	}
	var count int64
	if err := tx.Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

func ReleaseMediaJobSpendBudgetReservation(job MediaJob) {
	if modernMediaBilling() {
		return
	}
	requestID := mediaJobSpendBudgetRequestID(job)
	if requestID == "" {
		return
	}
	tokenID := 0
	if job.TokenID != nil {
		tokenID = *job.TokenID
	}
	if err := ReleaseSpendBudgetReservation(SpendBudgetChargeInput{
		ProjectID: job.ProjectID, PayerUserID: job.BillingUserID(), ActorUserID: job.ActorID(),
		UserID:    job.BillingUserID(),
		TokenID:   tokenID,
		RequestID: requestID,
	}); err != nil {
		common.SysLog("failed to release media job spend budget reservation: " + err.Error())
	}
}

func releaseMediaJobSpendBudgetReservation(job MediaJob) {
	ReleaseMediaJobSpendBudgetReservation(job)
}

func billingUsageFromMediaFact(fact usagepkg.Fact) billing.Usage {
	fact = fact.Normalize()
	out := billing.Usage{}
	for _, dimension := range fact.Dimensions {
		if dimension.Quantity <= 0 {
			continue
		}
		switch dimension.Name {
		case usagepkg.DimensionRequest:
			out.RequestCount += dimension.Quantity
		case usagepkg.DimensionSecond:
			out.Seconds += dimension.Quantity
		}
	}
	if units := fact.MediaUnits; units != nil && units.ProviderUnit == MediaJobKindVideo {
		// A video job prices through its own usage so resolution tiers, token
		// counts and provider-reported costs apply; the flat second line is
		// what videoLines falls back to.
		out.Video = &billing.VideoUsage{
			Seconds:         units.VideoSeconds,
			Resolution:      units.ResolutionTier,
			OutputTokens:    units.OutputTokens,
			ProviderCostUSD: units.ReportedCostUSD,
			Audio:           units.Audio,
		}
		out.Seconds = 0
	}
	return out
}

func firstPositiveInt64Field(fields map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := fields[key]; ok {
			if parsed, ok := int64FromAny(value); ok && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func firstStringField(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := fields[key]; ok {
			if parsed := strings.TrimSpace(fmt.Sprint(value)); parsed != "" && parsed != "<nil>" {
				return parsed
			}
		}
	}
	return ""
}

func int64FromAny(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return parsed, true
		}
		floatValue, err := typed.Float64()
		return int64(floatValue), err == nil
	default:
		return 0, false
	}
}
