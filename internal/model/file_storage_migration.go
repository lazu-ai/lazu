package model

import (
	"fmt"

	"gorm.io/gorm"
)

// retiredStorageQuotaColumn held a per-account storage budget in microUSD,
// the cap for a storage fee charged at upload. Storage is free since schema
// 29 and capped in bytes by FileStorageQuotaBytesOption; no account ever had
// a budget set.
const retiredStorageQuotaColumn = "storage_quota_microusd"

// retiredStorageChargePrefix starts the request id of every storage fee row
// the retired file accounting wrote into the budget tables.
const retiredStorageChargePrefix = "file_storage_upload:"

// migrateFileStorageAccounting retires the storage fee:
//   - it drops the per-account storage budget column;
//   - it zeroes storage fees in the spend-budget tables, since budgets no
//     longer count storage. Those rows were written to SQL alone, outside the
//     Redis ledger, which is why budget 90 drifted from its hot counter. The
//     wallet ledger keeps the original charge and refund rows untouched;
//   - it rebuilds every account's storage counter from the files it pays for.
//     Files written outside accounting (a seed import) left counters at zero,
//     and the byte cap is only meaningful once they agree with the files table.
func migrateFileStorageAccounting(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if err := dropLegacyColumn(db, "users", retiredStorageQuotaColumn); err != nil {
		return fmt.Errorf("drop retired column users.%s: %w", retiredStorageQuotaColumn, err)
	}
	for _, table := range []interface{}{&SpendBudgetCharge{}, &ProjectSpend{}} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := db.Model(table).Where("request_id LIKE ? AND amount_microusd <> 0", retiredStorageChargePrefix+"%").Update("amount_microusd", 0).Error; err != nil {
			return fmt.Errorf("zero retired storage fees: %w", err)
		}
	}
	if !db.Migrator().HasTable(&File{}) || !db.Migrator().HasColumn(&User{}, "storage_bytes_used") {
		return nil
	}
	err := db.Exec(`UPDATE users SET storage_bytes_used = COALESCE((
		SELECT SUM(files.bytes) FROM files
		WHERE (CASE WHEN files.project_id > 0 THEN files.payer_user_id ELSE files.user_id END) = users.id
	), 0)`).Error
	if err != nil {
		return fmt.Errorf("rebuild users.storage_bytes_used: %w", err)
	}
	return nil
}
