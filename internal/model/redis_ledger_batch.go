package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Canonicalization permits replay of old numeric JSON and empty Lua arrays,
// while retaining every financial fact, including the original budget proof.
func canonicalLedgerFinancialEvent(event ledger.Event) (ledger.Event, string, error) {
	event.TelemetryEnvelope = ""
	event.StreamID = ""
	event.OccurredAt = event.OccurredAt.UTC()
	// Older callers omitted this derived field; the authoritative event time
	// and original budget windows still have to match exactly on replay.
	if event.BudgetTimestamp == 0 && !event.OccurredAt.IsZero() {
		event.BudgetTimestamp = event.OccurredAt.Unix()
	}
	if len(event.Budgets) == 0 {
		event.Budgets = nil
	}
	if len(event.Generations) == 0 {
		event.Generations = nil
	}
	if len(event.Rates) == 0 {
		event.Rates = nil
	}
	raw, err := json.Marshal(event)
	return event, string(raw), err
}

func verifyLedgerFinancialReplay(stored RedisLedgerOperation, expected RedisLedgerOperation) error {
	if stored.ID != expected.ID || stored.RequestID != expected.RequestID || stored.Kind != expected.Kind || stored.PayerID != expected.PayerID || stored.ProjectID != expected.ProjectID || stored.ActorID != expected.ActorID || stored.TokenID != expected.TokenID || stored.Amount != expected.Amount || !stored.CreatedAt.UTC().Truncate(time.Microsecond).Equal(expected.CreatedAt.UTC().Truncate(time.Microsecond)) {
		return fmt.Errorf("%w: financial operation %s conflicts with its permanent identity", ledger.ErrPermanentOperation, expected.ID)
	}
	var event ledger.Event
	if err := json.Unmarshal([]byte(stored.Input), &event); err != nil {
		return fmt.Errorf("%w: financial operation %s has invalid permanent input: %w", ledger.ErrPermanentOperation, expected.ID, err)
	}
	if err := validateRecoveryEvent(event); err != nil {
		return fmt.Errorf("%w: financial operation %s has invalid permanent input: %w", ledger.ErrPermanentOperation, expected.ID, err)
	}
	_, canonical, err := canonicalLedgerFinancialEvent(event)
	if err != nil {
		return err
	}
	if canonical != expected.Input {
		return fmt.Errorf("%w: financial operation %s conflicts with its immutable financial facts", ledger.ErrPermanentOperation, expected.ID)
	}
	return nil
}

// Fresh PostgreSQL batches use INSERT RETURNING directly. Only conflicting
// operation identities need a read, which also rejects same-ID tampering.
func insertVerifiedLedgerBatch(tx *gorm.DB, operations []RedisLedgerOperation) ([]RedisLedgerOperation, error) {
	if len(operations) == 0 {
		return nil, nil
	}
	fresh := make(map[string]bool, len(operations))
	if tx.Dialector.Name() == "postgres" {
		for start := 0; start < len(operations); start += 500 {
			chunk := operations[start:min(start+500, len(operations))]
			values := make([]string, len(chunk))
			args := make([]interface{}, 0, len(chunk)*11)
			for i, op := range chunk {
				values[i] = "(?,?,?,?,?,?,?,?,?,?,?)"
				args = append(args, op.ID, op.RequestID, op.Kind, op.PayerID, op.ProjectID, op.ActorID, op.TokenID, op.Amount, op.BatchID, op.Input, op.CreatedAt)
			}
			var inserted []struct{ ID string }
			query := "INSERT INTO redis_ledger_operations (id,request_id,kind,payer_id,project_id,actor_id,token_id,amount,batch_id,input,created_at) VALUES " + strings.Join(values, ",") + " ON CONFLICT (id) DO NOTHING RETURNING id"
			if err := tx.Raw(query, args...).Scan(&inserted).Error; err != nil {
				return nil, err
			}
			for _, op := range inserted {
				fresh[op.ID] = true
			}
		}
	} else {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&operations, 500).Error; err != nil {
			return nil, err
		}
		var inserted []RedisLedgerOperation
		if err := tx.Where("batch_id = ?", operations[0].BatchID).Find(&inserted).Error; err != nil {
			return nil, err
		}
		for _, op := range inserted {
			fresh[op.ID] = true
		}
	}
	conflicts := make([]string, 0, len(operations)-len(fresh))
	expected := make(map[string]RedisLedgerOperation, len(operations))
	result := make([]RedisLedgerOperation, 0, len(fresh))
	for _, op := range operations {
		expected[op.ID] = op
		if fresh[op.ID] {
			result = append(result, op)
		} else {
			conflicts = append(conflicts, op.ID)
		}
	}
	for start := 0; start < len(conflicts); start += 500 {
		ids := conflicts[start:min(start+500, len(conflicts))]
		var stored []RedisLedgerOperation
		if err := tx.Where("id IN ?", ids).Find(&stored).Error; err != nil {
			return nil, err
		}
		if len(stored) != len(ids) {
			return nil, fmt.Errorf("conflicting financial batch lost a permanent operation identity")
		}
		for _, op := range stored {
			if err := verifyLedgerFinancialReplay(op, expected[op.ID]); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func applyPostgresLedgerUsers(tx *gorm.DB, amounts map[int]int64) error {
	ids := sortedLedgerIDs(amounts)
	if len(ids) == 0 {
		return nil
	}
	values := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)*2)
	for i, id := range ids {
		values[i] = "(?::bigint,?::bigint)"
		args = append(args, id, amounts[id])
	}
	// The materialized lock input acquires each affected row once in ID order.
	// UPDATE reuses those locks in this statement; there is no read/lock trip.
	query := "WITH deltas(id,amount) AS (VALUES " + strings.Join(values, ",") + "), locked AS MATERIALIZED (SELECT u.id FROM users u JOIN deltas d ON d.id=u.id ORDER BY u.id FOR UPDATE OF u) UPDATE users u SET quota=u.quota-d.amount, used_quota=u.used_quota+d.amount FROM deltas d JOIN locked l ON l.id=d.id WHERE u.id=d.id AND u.quota IS NOT NULL AND u.used_quota IS NOT NULL RETURNING u.id"
	return verifyLedgerUpdatedIDs(tx, query, args, ids, "payer")
}

func applyPostgresLedgerTokens(tx *gorm.DB, amounts, remaining, lastUsed map[int]int64) error {
	ids := sortedLedgerIDs(amounts)
	if len(ids) == 0 {
		return nil
	}
	values := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)*4)
	for i, id := range ids {
		values[i] = "(?::bigint,?::bigint,?::bigint,?::bigint)"
		args = append(args, id, amounts[id], remaining[id], lastUsed[id])
	}
	query := "WITH deltas(id,amount,remaining,last_used) AS (VALUES " + strings.Join(values, ",") + "), locked AS MATERIALIZED (SELECT t.id FROM tokens t JOIN deltas d ON d.id=t.id ORDER BY t.id FOR UPDATE OF t) UPDATE tokens t SET remain_quota=t.remain_quota-d.remaining, used_quota=t.used_quota+d.amount, last_used_at=GREATEST(t.last_used_at,d.last_used) FROM deltas d JOIN locked l ON l.id=d.id WHERE t.id=d.id AND t.remain_quota IS NOT NULL AND t.used_quota IS NOT NULL AND t.last_used_at IS NOT NULL RETURNING t.id"
	return verifyLedgerUpdatedIDs(tx, query, args, ids, "key")
}

func verifyLedgerUpdatedIDs(tx *gorm.DB, query string, args []interface{}, expected []int, kind string) error {
	var returned []struct{ ID int }
	if err := tx.Raw(query, args...).Scan(&returned).Error; err != nil {
		return err
	}
	actual := make(map[int]bool, len(returned))
	for _, row := range returned {
		actual[row.ID] = true
	}
	for _, id := range expected {
		if !actual[id] {
			return fmt.Errorf("%w: %s %d missing or has invalid financial counters", ledger.ErrPermanentOperation, kind, id)
		}
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("unexpected %s identities updated by financial batch", kind)
	}
	return nil
}
