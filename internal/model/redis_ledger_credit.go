package model

import (
	"context"
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
)

// A non-relay debit must honor money already reserved by upstream requests.
// The generation fence covers commit (and crash recovery); the payer fence
// prevents settlement from changing the snapshot during this SQL transaction.
func fenceExternalLedgerDebit(db *gorm.DB, payerID int, amount int64, allowNegative bool) error {
	available, err := fenceExternalLedgerAvailable(db, payerID)
	if err != nil {
		return err
	}
	if available < amount && !allowNegative {
		return fmt.Errorf("user quota is not enough, need quota: %d", amount)
	}
	tx := db.Statement.ConnPool.(*identityTransaction)
	tx.externalDebits[payerID] = available - amount
	return nil
}

// Acquire the financial fence before taking a SQL user-row lock. The available
// balance includes accepted settlements not yet in SQL and live reservations,
// matching the amount shown in the console. Absolute administrative targets
// use this same serialized baseline for both credits and debits.
func fenceExternalLedgerAvailable(db *gorm.DB, payerID int) (int64, error) {
	tx, ok := db.Statement.ConnPool.(*identityTransaction)
	if !ok {
		return 0, fmt.Errorf("external ledger change requires a fenced SQL transaction")
	}
	if available, ok := tx.externalDebits[payerID]; ok {
		return available, nil
	}
	ctx, cancel := context.WithTimeout(db.Statement.Context, 2*time.Second)
	defer cancel()
	if err := GatewayLedger().CheckContinuity(ctx); err != nil {
		return 0, err
	}
	fenceKey := fmt.Sprintf("%sfence:%d", ledger.Prefix, payerID)
	owner := common.GenerateRequestID()
	locked, err := common.RDB.SetNX(ctx, fenceKey, owner, 30*time.Second).Result()
	if err != nil {
		return 0, err
	}
	if !locked {
		return 0, ledger.ErrRebuilding
	}
	unlock := func(bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := common.RDB.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{fenceKey}, owner).Err(); err != nil {
			common.SysError("external ledger debit unlock: " + err.Error())
		}
	}
	tx.mu.Lock()
	tx.completion = append(tx.completion, unlock)
	tx.mu.Unlock()
	mutation, err := beginIdentityMutation(ctx, []string{fmt.Sprintf("user:%d", payerID)})
	if err != nil {
		return 0, err
	}
	tx.mu.Lock()
	tx.mutations = append(tx.mutations, mutation)
	tx.mu.Unlock()
	snapshot, err := GatewayLedger().Storage.Snapshot(db.Statement.Context, ledger.Input{PayerID: payerID, Unlimited: true})
	if err != nil {
		return 0, err
	}
	if tx.externalDebits == nil {
		tx.externalDebits = map[int]int64{}
	}
	tx.externalDebits[payerID] = snapshot.Balance
	// This record survives a committed transaction whose process dies before
	// the immediate credit sync, and leaves no task behind on rollback.
	if err := db.Session(&gorm.Session{NewDB: true}).Create(&LedgerCounterRefresh{TableName: "users", ObjectID: payerID, UserID: payerID}).Error; err != nil {
		return 0, err
	}
	return snapshot.Balance, nil
}

// A committed credit is retried in sequence by jobs if immediate sync fails.
func SyncLedgerCreditsForUser(userID int) error {
	engine := GatewayLedger()
	if engine == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var rows []LedgerCreditSync
	if err := CoreDB().WithContext(ctx).Where("user_id = ? AND applied_at IS NULL", userID).Order("sequence").Limit(500).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if exists, err := common.RDB.Exists(ctx, fmt.Sprintf("%swallet:%d", ledger.Prefix, userID)).Result(); err != nil {
		return err
	} else if exists == 0 {
		if err := engine.Initialize(ctx, ledger.Input{PayerID: userID, RequestID: "credit-sync", Unlimited: true}); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if err := engine.Credit(ctx, ledger.Credit{ID: row.ID, PayerID: row.UserID, Sequence: row.Sequence, Amount: row.Amount}); err != nil {
			return err
		}
	}
	return nil
}
