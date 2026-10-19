package model

import "testing"

func TestMigrateFileStorageAccountingOnPostgres(t *testing.T) {
	core, _ := postgresTelemetryFixture(t)
	if err := core.AutoMigrate(&File{}, &SpendBudgetCharge{}, &ProjectSpend{}); err != nil {
		t.Fatal(err)
	}
	if err := core.Exec("ALTER TABLE users ADD COLUMN storage_quota_microusd bigint DEFAULT 0").Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&[]User{{Id: 71, Username: "pg-files-one", StorageBytesUsed: 999}, {Id: 72, Username: "pg-files-two"}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&[]File{
		{ID: "pg-own", UserID: 71, Bytes: 12, CreatedAt: 1},
		{ID: "pg-project", ProjectID: 5, PayerUserID: 72, ActorUserID: 71, UserID: 71, Bytes: 30, CreatedAt: 2},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := core.Create(&SpendBudgetCharge{BudgetID: 90, RequestID: "file_storage_upload:pg-own", AmountMicrousd: 5}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateFileStorageAccounting(core); err != nil {
		t.Fatal(err)
	}
	if core.Migrator().HasColumn(&User{}, retiredStorageQuotaColumn) {
		t.Fatal("retired column survived on PostgreSQL")
	}
	for id, want := range map[int]int64{71: 12, 72: 30} {
		var user User
		if err := core.Select("storage_bytes_used").First(&user, id).Error; err != nil || user.StorageBytesUsed != want {
			t.Fatalf("user %d bytes=%d want %d err=%v", id, user.StorageBytesUsed, want, err)
		}
	}
	var fee int64
	if err := core.Model(&SpendBudgetCharge{}).Where("request_id = ?", "file_storage_upload:pg-own").Select("amount_microusd").Scan(&fee).Error; err != nil || fee != 0 {
		t.Fatalf("storage fee=%d err=%v", fee, err)
	}
}
