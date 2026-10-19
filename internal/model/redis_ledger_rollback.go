package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type LegacyLedgerRollbackReport struct {
	Operations      int   `json:"operations"`
	DailyRows       int64 `json:"daily_rows"`
	DailyAmount     int64 `json:"daily_amount_microusd"`
	OperationAmount int64 `json:"operation_amount_microusd"`
	LegacyRows      int64 `json:"legacy_rows"`
	BudgetCharges   int64 `json:"budget_charges"`
}

// PrepareLegacyLedgerRollback preserves every financial credential and rewrites
// only derived daily summaries into the per-request shape understood by the
// previous release. All gateway spools must be replayed before calling it.
func PrepareLegacyLedgerRollback(ctx context.Context, writersStopped bool) (LegacyLedgerRollbackReport, error) {
	var report LegacyLedgerRollbackReport
	if !writersStopped {
		return report, errors.New("stop every API, callback and jobs writer and provide -writers-stopped before preparing rollback")
	}
	if common.RDB == nil {
		return report, errors.New("Redis unavailable: cannot prove financial drain")
	}
	nodes, err := ledgerScan(ctx, ledger.Prefix+"nodes:*")
	if err != nil {
		return report, err
	}
	if len(nodes) > 0 {
		return report, fmt.Errorf("%d financial writer leases remain live; stop writers and wait for lease expiry", len(nodes))
	}
	paused, err := common.RDB.Exists(ctx, ledger.Prefix+"maintenance").Result()
	if err != nil {
		return report, err
	}
	if paused != 1 {
		return report, errors.New("pause new ledger admission before rollback")
	}
	pending, err := common.RDB.ZCard(ctx, ledger.Prefix+"uncommitted").Result()
	if err != nil {
		return report, err
	}
	holds, err := common.RDB.HLen(ctx, ledger.Prefix+"reservations").Result()
	if err != nil {
		return report, err
	}
	if pending > 0 || holds > 0 {
		return report, fmt.Errorf("ledger not drained: %d operations, %d reservations", pending, holds)
	}
	health, err := ReadLedgerHealth(ctx)
	if err != nil {
		return report, err
	}
	if health.Exposure != 0 || health.Dead != 0 {
		return report, fmt.Errorf("financial exposure/dead-letter drain incomplete: exposure=%d dead=%d", health.Exposure, health.Dead)
	}
	var refreshes int64
	if err := CoreDB().WithContext(ctx).Model(&LedgerCounterRefresh{}).Where("applied_at IS NULL").Count(&refreshes).Error; err != nil {
		return report, err
	}
	if refreshes > 0 {
		return report, fmt.Errorf("%d administrative counter refreshes still pending", refreshes)
	}
	var credits int64
	if err := CoreDB().WithContext(ctx).Model(&LedgerCreditSync{}).Where("applied_at IS NULL").Count(&credits).Error; err != nil {
		return report, err
	}
	if credits > 0 {
		return report, fmt.Errorf("%d credits still pending", credits)
	}
	err = CoreDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx = SkipIdentityCounterRefresh(tx)
		var daily []ProjectSpend
		if err := tx.Where("aggregated = ?", true).Find(&daily).Error; err != nil {
			return err
		}
		report.DailyRows = int64(len(daily))
		for _, r := range daily {
			var err error
			report.DailyAmount, err = checkedLedgerAdd(report.DailyAmount, int64(r.AmountMicrousd))
			if err != nil {
				return err
			}
		}
		// The retained financial operations are the conversion source. Logs are
		// neither consulted nor required to restore budget-compatible records.
		var definitions []SpendBudget
		if err := tx.Find(&definitions).Error; err != nil {
			return err
		}
		var operations []RedisLedgerOperation
		var materialized int64
		if err := tx.Order("id").FindInBatches(&operations, 500, func(_ *gorm.DB, _ int) error {
			rows := make([]ProjectSpend, 0, len(operations))
			keys := make([]string, 0, len(operations))
			ids := make([]string, 0, len(operations))
			for _, op := range operations {
				ids = append(ids, op.ID)
			}
			var existing []SpendBudgetCharge
			if err := tx.Where("ledger_operation_id IN ?", ids).Find(&existing).Error; err != nil {
				return err
			}
			proofs := map[string]SpendBudgetCharge{}
			for _, row := range existing {
				key := legacyBudgetProofKey(row)
				if _, duplicate := proofs[key]; duplicate {
					return fmt.Errorf("duplicate legacy budget proof %s", key)
				}
				proofs[key] = row
			}
			charges := []SpendBudgetCharge{}
			for _, op := range operations {
				var event ledger.Event
				if err := json.Unmarshal([]byte(op.Input), &event); err != nil {
					return err
				}
				if err := validateRecoveryEvent(event); err != nil {
					return err
				}
				if event.Amount != op.Amount || event.ProjectID != op.ProjectID || event.PayerID != op.PayerID || event.ActorID != op.ActorID || event.TokenID != op.TokenID || ledgerOperationID(event.OperationID, event.Kind) != op.ID {
					return fmt.Errorf("permanent operation payload mismatch %s", op.ID)
				}
				if event.ProjectID > 0 {
					report.Operations++
					input := SpendBudgetChargeInput{ProjectID: event.ProjectID, ActorUserID: event.ActorID, PayerUserID: event.PayerID, TokenID: event.TokenID, RequestID: event.OperationID, OccurredAt: event.OccurredAt}
					key := projectSpendEventKey(input)
					keys = append(keys, key)
					var err error
					report.OperationAmount, err = checkedLedgerAdd(report.OperationAmount, event.Amount)
					if err != nil {
						return err
					}
					rows = append(rows, ProjectSpend{EventKey: key, ProjectID: event.ProjectID, ActorUserID: event.ActorID, PayerUserID: event.PayerID, TokenID: event.TokenID, RequestID: event.OperationID, AmountMicrousd: billing.MicroUSD(event.Amount), OccurredAt: event.OccurredAt})
				}
				// Immutable original hits remain in op.Input. The old release sums
				// charge rows by budget ID and occurrence time, so its projection
				// must contain one row per operation/budget even if the period
				// changed. Prefer the current matching definition for that row.
				budgets := make(map[uint]ledger.Budget, len(event.Budgets))
				for _, b := range event.Budgets {
					budgets[b.ID] = b
				}
				for _, b := range LedgerBudgets(definitions, event.OccurredAt) {
					if b.Matches(event.Input) {
						budgets[b.ID] = b
					}
				}
				budgetIDs := make([]uint, 0, len(budgets))
				for id := range budgets {
					budgetIDs = append(budgetIDs, id)
				}
				sort.Slice(budgetIDs, func(i, j int) bool { return budgetIDs[i] < budgetIDs[j] })
				for _, id := range budgetIDs {
					b := budgets[id]
					row := SpendBudgetCharge{LedgerOperationID: op.ID, BudgetID: b.ID, ProjectID: event.ProjectID, PayerUserID: event.PayerID, ActorUserID: event.ActorID, Scope: b.Scope, UserID: event.ActorID, TokenID: event.TokenID, SessionID: event.SessionID, AgentID: event.AgentID, RequestID: event.OperationID, Period: b.Period, PeriodStart: b.Start, PeriodEnd: b.End, AmountMicrousd: billing.MicroUSD(event.Amount), CreatedAt: event.OccurredAt}
					key := legacyBudgetProofKey(row)
					if prior, ok := proofs[key]; ok {
						if prior.AmountMicrousd != row.AmountMicrousd || prior.ProjectID != row.ProjectID || prior.PayerUserID != row.PayerUserID || prior.ActorUserID != row.ActorUserID || prior.UserID != row.UserID || prior.TokenID != row.TokenID || prior.SessionID != row.SessionID || prior.AgentID != row.AgentID || prior.RequestID != row.RequestID || prior.CreatedAt.UnixMicro() != row.CreatedAt.UnixMicro() {
							return fmt.Errorf("legacy budget proof conflicts with permanent operation %s", key)
						}
						if prior.Scope != row.Scope || prior.Period != row.Period || prior.PeriodStart != row.PeriodStart || prior.PeriodEnd != row.PeriodEnd {
							if err := tx.Model(&SpendBudgetCharge{}).Where("id = ?", prior.ID).Updates(map[string]interface{}{"scope": row.Scope, "period": row.Period, "period_start": row.PeriodStart, "period_end": row.PeriodEnd}).Error; err != nil {
								return err
							}
						}
						continue
					}
					proofs[key] = row
					charges = append(charges, row)
				}
			}
			if len(rows) > 0 {
				if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_key"}}, DoNothing: true}).CreateInBatches(&rows, 500).Error; err != nil {
					return err
				}
				var restored []ProjectSpend
				if err := tx.Where("event_key IN ?", keys).Find(&restored).Error; err != nil {
					return err
				}
				expected := make(map[string]ProjectSpend, len(rows))
				for _, row := range rows {
					expected[row.EventKey] = row
				}
				if len(restored) != len(expected) {
					return errors.New("legacy project spend projection incomplete")
				}
				for _, prior := range restored {
					row := expected[prior.EventKey]
					if prior.Aggregated || prior.AmountMicrousd != row.AmountMicrousd || prior.ProjectID != row.ProjectID || prior.ActorUserID != row.ActorUserID || prior.PayerUserID != row.PayerUserID || prior.TokenID != row.TokenID || prior.RequestID != row.RequestID || prior.OccurredAt.UnixMicro() != row.OccurredAt.UnixMicro() {
						return fmt.Errorf("legacy project spend conflicts with permanent operation %s", prior.EventKey)
					}
					var err error
					materialized, err = checkedLedgerAdd(materialized, int64(prior.AmountMicrousd))
					if err != nil {
						return err
					}
				}
				report.LegacyRows += int64(len(keys))
			}
			if len(charges) > 0 {
				if err := tx.CreateInBatches(&charges, 500).Error; err != nil {
					return err
				}
			}
			report.BudgetCharges += int64(len(proofs))
			return nil
		}).Error; err != nil {
			return err
		}
		if len(daily) > 0 && report.DailyAmount != report.OperationAmount {
			return fmt.Errorf("daily total %d differs from permanent operations %d", report.DailyAmount, report.OperationAmount)
		}
		if materialized != report.OperationAmount {
			return fmt.Errorf("restored total %d differs from operations %d", materialized, report.OperationAmount)
		}
		if len(daily) > 0 {
			if err := tx.Where("aggregated = ?", true).Delete(&ProjectSpend{}).Error; err != nil {
				return err
			}
		}
		// Setters never change authoritative wallet balances, Key quotas or ledger.
		return nil
	})
	return report, err
}

func legacyBudgetProofKey(row SpendBudgetCharge) string {
	return fmt.Sprintf("%s:%d", row.LedgerOperationID, row.BudgetID)
}

// ProcessLedgerCounterRefresh retires administrative tasks once their SQL
// baseline can be reconciled. Ordinary admission also performs this refresh on
// a generation mismatch, so revocation cannot wait for this worker's timer.
func ProcessLedgerCounterRefresh(ctx context.Context) error {
	engine := GatewayLedger()
	if engine == nil {
		return nil
	}
	if err := ExtendRedisMediaReservations(ctx); err != nil {
		return err
	}
	var rows []LedgerCounterRefresh
	if err := CoreDB().WithContext(ctx).Where("applied_at IS NULL").Order("id").Limit(500).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		users := []int{}
		tokenIDs := map[int][]int{}
		switch row.TableName {
		case "users":
			if row.UserID > 0 {
				users = append(users, row.UserID)
			} else {
				if err := CoreDB().WithContext(ctx).Unscoped().Model(&User{}).Pluck("id", &users).Error; err != nil {
					return err
				}
			}
		case "tokens":
			var tokens []Token
			query := CoreDB().WithContext(ctx).Unscoped()
			if row.TokenID > 0 {
				query = query.Where("id = ?", row.TokenID)
			}
			if err := query.Find(&tokens).Error; err != nil {
				return err
			}
			for _, key := range tokens {
				if key.ProjectID > 0 {
					var project Project
					if err := CoreDB().WithContext(ctx).First(&project, key.ProjectID).Error; err != nil {
						return err
					}
					users = append(users, project.OwnerUserID)
					tokenIDs[project.OwnerUserID] = append(tokenIDs[project.OwnerUserID], key.Id)
				} else {
					users = append(users, key.UserId)
					tokenIDs[key.UserId] = append(tokenIDs[key.UserId], key.Id)
				}
			}
		}
		for _, userID := range users {
			gens, err := readIdentityGenerations(ctx, []string{fmt.Sprintf("user:%d", userID), "policy"}, true)
			if err != nil {
				return err
			}
			input := ledger.Input{RequestID: fmt.Sprintf("refresh:%d:%d", row.ID, userID), PayerID: userID, OccurredAt: time.Now().UTC()}
			for _, g := range gens {
				input.Generations = append(input.Generations, ledger.Generation{Key: g.Key, PendingKey: g.PendingKey, Value: g.Value})
				if g.Key == fmt.Sprintf("gen:user:%d", userID) {
					input.WalletGeneration = g.Value
				}
			}
			for _, g := range gens {
				if g.Key == "gen:policy" {
					input.WalletGeneration += ":" + g.Value
				}
			}
			if err := engine.Initialize(ctx, input); err != nil {
				return err
			}
			for _, tokenID := range tokenIDs[userID] {
				tokenGens, err := readIdentityGenerations(ctx, []string{fmt.Sprintf("token:%d", tokenID)}, true)
				if err != nil {
					return err
				}
				keyInput := input
				keyInput.TokenID = tokenID
				for _, g := range tokenGens {
					keyInput.Generations = append(keyInput.Generations, ledger.Generation{Key: g.Key, PendingKey: g.PendingKey, Value: g.Value})
					keyInput.TokenGeneration = g.Value
				}
				for _, g := range gens {
					if g.Key == "gen:policy" {
						keyInput.TokenGeneration += ":" + g.Value
					}
				}
				if err := engine.Initialize(ctx, keyInput); err != nil {
					return err
				}
			}
		}
		if err := CoreDB().WithContext(ctx).Model(&LedgerCounterRefresh{}).Where("id = ? AND applied_at IS NULL", row.ID).Update("applied_at", time.Now().UTC()).Error; err != nil {
			return err
		}
	}
	return nil
}
