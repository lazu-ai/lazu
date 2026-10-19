package model

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupFileModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	previousDB := coreDB
	coreDB = db
	EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&User{}, &Token{}, &WalletLedger{}, &File{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	user := User{Id: 1, Username: "alice", Password: "password123", Quota: billing.USD(1), Status: common.UserStatusEnabled}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		coreDB = previousDB
	})
	return db
}

func setFileStorageQuotaForTest(t *testing.T, bytes int64) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, existed := common.OptionMap[FileStorageQuotaBytesOption]
	common.OptionMap[FileStorageQuotaBytesOption] = fmt.Sprint(bytes)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap[FileStorageQuotaBytesOption] = previous
		} else {
			delete(common.OptionMap, FileStorageQuotaBytesOption)
		}
	})
}

func TestCreateAndDeleteFileCountsBytesWithoutBilling(t *testing.T) {
	db := setupFileModelTestDB(t)
	expiresAt := int64(2000)
	file := &File{
		ID:         "file-test",
		UserID:     1,
		Filename:   "paper.pdf",
		Purpose:    "user_data",
		MimeType:   "application/pdf",
		Bytes:      100,
		StorageKey: "files/1/key",
		Status:     FileStatusProcessed,
		ExpiresAt:  &expiresAt,
		CreatedAt:  1000,
	}

	if err := CreateFileWithAccounting(CreateFileAccountingInput{File: file, EnforceQuota: true}); err != nil {
		t.Fatalf("create file: %v", err)
	}
	var user User
	if err := db.Select("quota", "storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.Quota != billing.USD(1) {
		t.Fatalf("storage changed the wallet: %d", user.Quota)
	}
	if user.StorageBytesUsed != 100 {
		t.Fatalf("unexpected storage bytes: %d", user.StorageBytesUsed)
	}

	if err := DeleteFileWithAccounting(file); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if err := db.Select("quota", "storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.Quota != billing.USD(1) || user.StorageBytesUsed != 0 {
		t.Fatalf("delete left quota=%d bytes=%d", user.Quota, user.StorageBytesUsed)
	}
	var count int64
	if err := db.Model(&WalletLedger{}).Count(&count).Error; err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if count != 0 {
		t.Fatalf("storage wrote %d wallet ledger rows", count)
	}
	if err := DeleteFileWithAccounting(file); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second delete = %v, want not found", err)
	}
}

func TestCreateFileWithAccountingEnforcesStorageQuota(t *testing.T) {
	db := setupFileModelTestDB(t)
	setFileStorageQuotaForTest(t, 50)
	if err := db.Model(&User{}).Where("id = ?", 1).Update("storage_bytes_used", 40).Error; err != nil {
		t.Fatalf("seed storage bytes: %v", err)
	}
	upload := &File{ID: "file-quota", UserID: 1, Filename: "paper.pdf", Purpose: "user_data", MimeType: "application/pdf", Bytes: 11, StorageKey: "files/1/key", Status: FileStatusProcessed, CreatedAt: 1000}
	err := CreateFileWithAccounting(CreateFileAccountingInput{File: upload, EnforceQuota: true})
	if types.ErrCode(err) != types.ErrorCodeStorageQuotaExceeded {
		t.Fatalf("upload over the cap = %v, want storage_quota_exceeded", err)
	}
	var files int64
	if err := db.Model(&File{}).Count(&files).Error; err != nil || files != 0 {
		t.Fatalf("refused upload left %d file rows: %v", files, err)
	}

	exact := &File{ID: "file-exact", UserID: 1, Filename: "fits.pdf", Purpose: "user_data", MimeType: "application/pdf", Bytes: 10, StorageKey: "files/1/fits", Status: FileStatusProcessed, CreatedAt: 1001}
	if err := CreateFileWithAccounting(CreateFileAccountingInput{File: exact, EnforceQuota: true}); err != nil {
		t.Fatalf("upload filling the cap exactly: %v", err)
	}

	// A paid generation's artifact is counted but never refused.
	artifact := &File{ID: "file-artifact", UserID: 1, Filename: "art.png", Purpose: "vision", MimeType: "image/png", Bytes: 30, StorageKey: "files/1/art", Status: FileStatusProcessed, CreatedAt: 1002}
	if err := CreateFileWithAccounting(CreateFileAccountingInput{File: artifact}); err != nil {
		t.Fatalf("artifact over the cap was refused: %v", err)
	}
	var user User
	if err := db.Select("storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if user.StorageBytesUsed != 80 {
		t.Fatalf("storage bytes = %d, want 80", user.StorageBytesUsed)
	}
}

func TestCreateFileWithAccountingConcurrentStorageQuota(t *testing.T) {
	db := setupFileModelTestDB(t)
	setFileStorageQuotaForTest(t, 100)

	var successes atomic.Int64
	var unexpectedErrors atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := CreateFileWithAccounting(CreateFileAccountingInput{
				File: &File{
					ID:         fmt.Sprintf("file-concurrent-%d", i),
					UserID:     1,
					Filename:   fmt.Sprintf("paper-%d.pdf", i),
					Purpose:    "user_data",
					MimeType:   "application/pdf",
					Bytes:      30,
					StorageKey: fmt.Sprintf("files/1/concurrent-%d", i),
					Status:     FileStatusProcessed,
					CreatedAt:  int64(1000 + i),
				},
				EnforceQuota: true,
			})
			if err == nil {
				successes.Add(1)
				return
			}
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "storage_quota_exceeded") && !strings.Contains(msg, "locked") {
				t.Errorf("unexpected create file error: %v", err)
				unexpectedErrors.Add(1)
			}
		}()
	}
	wg.Wait()

	if unexpectedErrors.Load() > 0 {
		t.Fatalf("saw %d unexpected create file errors", unexpectedErrors.Load())
	}
	successCount := successes.Load()
	if successCount == 0 || successCount > 3 {
		t.Fatalf("successful creates = %d, want 1..3 under a 100-byte cap", successCount)
	}
	var user User
	if err := db.Select("quota", "storage_bytes_used").First(&user, 1).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.StorageBytesUsed != successCount*30 {
		t.Fatalf("expected storage bytes %d, got %d", successCount*30, user.StorageBytesUsed)
	}
	if user.Quota != billing.USD(1) {
		t.Fatalf("concurrent uploads changed the wallet: %d", user.Quota)
	}
	var fileCount int64
	if err := db.Model(&File{}).Where("user_id = ?", 1).Count(&fileCount).Error; err != nil {
		t.Fatalf("count files: %v", err)
	}
	if fileCount != successCount {
		t.Fatalf("expected %d files, got %d", successCount, fileCount)
	}
}

// Storage is free, so a project file must never touch a spend budget: a
// budget written outside the Redis ledger is what drifted budget 90.
func TestProjectFileStorageNeverTouchesBudgets(t *testing.T) {
	db := setupFileModelTestDB(t)
	if err := db.AutoMigrate(&Project{}, &SpendBudget{}, &SpendBudgetAlert{}, &SpendBudgetReservation{}, &SpendBudgetCharge{}, &ProjectSpend{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Project{ID: 7, OwnerUserID: 1, Name: "Files"}).Error; err != nil {
		t.Fatal(err)
	}
	keyID := 2
	budget := SpendBudget{ProjectID: 7, Scope: SpendBudgetScopeToken, TokenID: keyID, Period: SpendBudgetPeriodMonth, LimitMicrousd: 0, Enabled: true, Enforcement: SpendBudgetEnforcementBlock}
	if err := db.Create(&budget).Error; err != nil {
		t.Fatal(err)
	}
	file := &File{ID: "file-budget", ProjectID: 7, PayerUserID: 1, ActorUserID: 3, UserID: 3, TokenID: &keyID, Bytes: 100, CreatedAt: time.Now().Unix()}
	if err := CreateFileWithAccounting(CreateFileAccountingInput{File: file, EnforceQuota: true}); err != nil {
		t.Fatalf("a zero spend budget refused free storage: %v", err)
	}
	var owner User
	if err := db.First(&owner, 1).Error; err != nil {
		t.Fatal(err)
	}
	if owner.Quota != billing.USD(1) || owner.StorageBytesUsed != 100 {
		t.Fatalf("payer quota=%d bytes=%d, want untouched wallet and 100 bytes", owner.Quota, owner.StorageBytesUsed)
	}
	for _, table := range []any{&WalletLedger{}, &SpendBudgetCharge{}, &ProjectSpend{}, &SpendBudgetReservation{}, &SpendBudgetAlert{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("free storage wrote %d %T rows", count, table)
		}
	}
	if err := DeleteFileWithAccounting(file); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&owner, 1).Error; err != nil || owner.StorageBytesUsed != 0 {
		t.Fatalf("payer bytes after delete=%d %v", owner.StorageBytesUsed, err)
	}
}

func TestFormatStorageBytes(t *testing.T) {
	for bytes, want := range map[int64]string{512: "512 B", 1536: "1.5 KB", 10 << 30: "10 GB", 512 << 20: "512 MB"} {
		if got := FormatStorageBytes(bytes); got != want {
			t.Fatalf("FormatStorageBytes(%d) = %q, want %q", bytes, got, want)
		}
	}
}

func TestCreateFileWithAccountingBlocksHeldProject(t *testing.T) {
	db := setupFileModelTestDB(t)
	if err := db.AutoMigrate(&Project{}); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: 7, OwnerUserID: 1, Name: "Held file project", PaymentRiskHold: true}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	file := &File{ID: "held-file", ProjectID: project.ID, PayerUserID: 1, ActorUserID: 1, UserID: 1, Bytes: 100, CreatedAt: time.Now().Unix()}
	err := CreateFileWithAccounting(CreateFileAccountingInput{File: file, EnforceQuota: true})
	if !errors.Is(err, ErrProjectAccessSuspended) {
		t.Fatalf("held project admitted file charge: %v", err)
	}
	var fileCount, ledgerCount int64
	if err := db.Model(&File{}).Count(&fileCount).Error; err != nil || fileCount != 0 {
		t.Fatalf("held file was persisted: %d %v", fileCount, err)
	}
	if err := db.Model(&WalletLedger{}).Count(&ledgerCount).Error; err != nil || ledgerCount != 0 {
		t.Fatalf("held file changed wallet: %d %v", ledgerCount, err)
	}
}
