package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func withAccountDeletionTestDB(t *testing.T, name string) {
	t.Helper()
	db := withPricingTestDB(t, name)
	if err := db.AutoMigrate(&UserIdentity{}, &AccountDeletion{}, &TokenCredential{}, &AgentSession{}, &AgentSessionMessage{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func seedPurgeCandidate(t *testing.T, username, email string, balance int64) User {
	t.Helper()
	user := User{Username: username, Password: "password123", Email: email, VerifiedEmail: email, Status: common.UserStatusEnabled}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := CoreDB().Model(&User{}).Where("id = ?", user.Id).Update("quota", balance).Error; err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	if err := CoreDB().Create(&UserIdentity{UserID: user.Id, Provider: "google", Subject: "sub-" + username, Email: email}).Error; err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	if err := CoreDB().Create(&Token{UserId: user.Id, Name: "agent", Key: "purge-key-" + username, Status: common.TokenStatusEnabled}).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return user
}

func backdateDeletion(t *testing.T, userID int, age time.Duration) {
	t.Helper()
	if err := CoreDB().Unscoped().Model(&User{}).Where("id = ?", userID).Update("deleted_at", time.Now().Add(-age)).Error; err != nil {
		t.Fatalf("backdate: %v", err)
	}
}

func TestDeletionWritesARecordAndRestoreClosesIt(t *testing.T) {
	withAccountDeletionTestDB(t, "account_deletion_record")
	user := seedPurgeCandidate(t, "recorded", "recorded@example.com", 2_000_000)
	if _, err := DeleteSelfUser(user.Id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	record, err := LatestAccountDeletion(user.Id)
	if err != nil || record == nil {
		t.Fatalf("no deletion record: %v", err)
	}
	if record.Username != "recorded" || record.Email != "recorded@example.com" || record.DeletedBy != AccountDeletedBySelf || record.BalanceMicrousd != 2_000_000 || record.Identities == "" || record.Identities == "[]" {
		t.Fatalf("record = %+v", record)
	}
	if _, _, err := RestoreDeletedUser(user.Id, RestoreBySignIn); err != nil {
		t.Fatalf("restore: %v", err)
	}
	record, _ = LatestAccountDeletion(user.Id)
	if record.RestoredAt == 0 || record.RestoredBy != "sign_in" {
		t.Fatalf("restore left the record open: %+v", record)
	}
}

func TestPurgeClearsAnExpiredDeletionAndKeepsItsHistory(t *testing.T) {
	withAccountDeletionTestDB(t, "account_deletion_purge")
	expired := seedPurgeCandidate(t, "expired", "expired@example.com", 3_500_000)
	recent := seedPurgeCandidate(t, "recent", "recent@example.com", 1_000_000)
	for _, user := range []User{expired, recent} {
		if _, err := DeleteSelfUser(user.Id); err != nil {
			t.Fatalf("delete %s: %v", user.Username, err)
		}
	}
	backdateDeletion(t, expired.Id, AccountRestoreWindow+time.Hour)
	backdateDeletion(t, recent.Id, 24*time.Hour)

	purged, err := PurgeExpiredDeletedAccounts(time.Now())
	if err != nil || purged != 1 {
		t.Fatalf("purge = %d, %v; want 1", purged, err)
	}

	var cleared User
	CoreDB().Unscoped().First(&cleared, expired.Id)
	if !cleared.DeletedAt.Valid || cleared.Username != purgedUsername(expired.Id) || cleared.Email != "" || cleared.VerifiedEmail != "" || cleared.Quota != 0 {
		t.Fatalf("cleared row = %+v", cleared)
	}
	var keys int64
	CoreDB().Unscoped().Model(&Token{}).Where("user_id = ?", expired.Id).Count(&keys)
	if keys != 0 {
		t.Fatalf("%d keys survived the purge", keys)
	}
	var identity UserIdentity
	CoreDB().Where("user_id = ?", expired.Id).First(&identity)
	if identity.ReleasedAt == nil || identity.Subject == "sub-expired" || identity.Email != "" {
		t.Fatalf("identity not released: %+v", identity)
	}
	var writeOff WalletLedger
	if err := CoreDB().Where("user_id = ? AND ref_type = ?", expired.Id, "account_purge").First(&writeOff).Error; err != nil || writeOff.DeltaMicrousd != -3_500_000 {
		t.Fatalf("write-off ledger = %+v, %v", writeOff, err)
	}
	record, _ := LatestAccountDeletion(expired.Id)
	if record == nil || record.PurgedAt == 0 || record.WrittenOffMicrousd != 3_500_000 || record.Username != "expired" || record.Email != "expired@example.com" {
		t.Fatalf("record = %+v", record)
	}

	var waiting User
	CoreDB().Unscoped().First(&waiting, recent.Id)
	if waiting.Username != "recent" || waiting.Email != "recent@example.com" {
		t.Fatalf("an account inside its window was cleared: %+v", waiting)
	}

	// The username, address and Google account are free again.
	if IsEmailAlreadyTaken("expired@example.com") {
		t.Fatal("the cleared address still counts as taken")
	}
	again := User{Username: "expired", Password: "password123", Email: "expired@example.com", Status: common.UserStatusEnabled}
	if err := again.InsertWithIdentity(&UserIdentity{Provider: "google", Subject: "sub-expired", Email: "expired@example.com"}); err != nil {
		t.Fatalf("sign-up with the released name, address and identity: %v", err)
	}

	// A second run finds nothing, and a cleared account cannot be restored.
	if purged, err := PurgeExpiredDeletedAccounts(time.Now()); err != nil || purged != 0 {
		t.Fatalf("second purge = %d, %v", purged, err)
	}
	if _, _, err := RestoreDeletedUser(expired.Id, RestoreByAdmin); err == nil {
		t.Fatal("a cleared account was restored")
	}
}

func TestPurgeRecordsAnAccountDeletedBeforeRecordsWereKept(t *testing.T) {
	withAccountDeletionTestDB(t, "account_deletion_legacy")
	legacy := seedPurgeCandidate(t, "legacy", "legacy@example.com", 0)
	if err := CoreDB().Delete(&legacy).Error; err != nil {
		t.Fatalf("legacy soft delete: %v", err)
	}
	backdateDeletion(t, legacy.Id, 90*24*time.Hour)
	if purged, err := PurgeExpiredDeletedAccounts(time.Now()); err != nil || purged != 1 {
		t.Fatalf("purge = %d, %v", purged, err)
	}
	record, _ := LatestAccountDeletion(legacy.Id)
	if record == nil || record.DeletedBy != AccountDeletedByUnknown || record.Username != "legacy" || record.PurgedAt == 0 {
		t.Fatalf("record = %+v", record)
	}
	var ledger int64
	CoreDB().Model(&WalletLedger{}).Where("user_id = ? AND ref_type = ?", legacy.Id, "account_purge").Count(&ledger)
	if ledger != 0 {
		t.Fatal("a zero balance wrote a ledger entry")
	}
}
