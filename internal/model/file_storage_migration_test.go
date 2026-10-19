package model

import (
	"testing"
)

func TestMigrateFileStorageAccountingDropsBudgetAndRebuildsCounters(t *testing.T) {
	db := setupFileModelTestDB(t)
	if err := db.Exec("ALTER TABLE users ADD COLUMN storage_quota_microusd bigint DEFAULT 0").Error; err != nil {
		t.Fatalf("add retired column: %v", err)
	}
	if err := db.Create(&User{Id: 2, Username: "owner", Password: "password123"}).Error; err != nil {
		t.Fatal(err)
	}
	// Counters drifted: user 1 claims bytes it no longer holds, the project
	// owner was never counted (a seed import wrote files directly).
	if err := db.Model(&User{}).Where("id = ?", 1).Update("storage_bytes_used", 999).Error; err != nil {
		t.Fatal(err)
	}
	files := []File{
		{ID: "own-a", UserID: 1, Bytes: 10, CreatedAt: 1},
		{ID: "own-b", UserID: 1, Bytes: 5, CreatedAt: 2},
		{ID: "project", ProjectID: 9, PayerUserID: 2, ActorUserID: 1, UserID: 1, Bytes: 70, CreatedAt: 3},
	}
	if err := db.Create(&files).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.AutoMigrate(&SpendBudgetCharge{}, &ProjectSpend{}, &WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	budgetRows := []SpendBudgetCharge{
		{BudgetID: 90, TokenID: 3, RequestID: "file_storage_upload:file-a", AmountMicrousd: 5},
		{BudgetID: 90, TokenID: 3, RequestID: "req_lazu_relay", AmountMicrousd: 37},
	}
	if err := db.Create(&budgetRows).Error; err != nil {
		t.Fatal(err)
	}
	spends := []ProjectSpend{
		{EventKey: "storage", ProjectID: 1, TokenID: 3, RequestID: "file_storage_upload:file-a", AmountMicrousd: 5},
		{EventKey: "relay", ProjectID: 1, TokenID: 3, RequestID: "req_lazu_relay", AmountMicrousd: 37},
	}
	if err := db.Create(&spends).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&WalletLedger{UserID: 1, DeltaMicrousd: -5, Kind: WalletLedgerKindSpend, RefType: "file_storage_upload", RefID: "file-a", EventID: "storage-charge"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := migrateFileStorageAccounting(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []interface{}{&SpendBudgetCharge{}, &ProjectSpend{}} {
		var storage, relay int64
		db.Model(table).Where("request_id = ?", "file_storage_upload:file-a").Select("amount_microusd").Scan(&storage)
		db.Model(table).Where("request_id = ?", "req_lazu_relay").Select("amount_microusd").Scan(&relay)
		if storage != 0 || relay != 37 {
			t.Fatalf("%T storage=%d relay=%d, want storage zeroed and relay kept", table, storage, relay)
		}
	}
	var wallet WalletLedger
	if err := db.First(&wallet, "event_id = ?", "storage-charge").Error; err != nil || wallet.DeltaMicrousd != -5 {
		t.Fatalf("wallet history was rewritten: %+v %v", wallet, err)
	}
	if db.Migrator().HasColumn(&User{}, retiredStorageQuotaColumn) {
		t.Fatal("retired storage budget column survived")
	}
	for id, want := range map[int]int64{1: 15, 2: 70} {
		var user User
		if err := db.Select("storage_bytes_used").First(&user, id).Error; err != nil {
			t.Fatal(err)
		}
		if user.StorageBytesUsed != want {
			t.Fatalf("user %d storage bytes = %d, want %d", id, user.StorageBytesUsed, want)
		}
	}
	// Re-running is harmless.
	if err := migrateFileStorageAccounting(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
}
