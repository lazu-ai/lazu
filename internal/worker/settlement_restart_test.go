package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestSettlementRefundSurvivesProcessRestartAndReplay(t *testing.T) {
	if path := os.Getenv("LAZU_TEST_RECOVERY_DB"); path != "" {
		db, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		model.SetCoreDB(db)
		common.RedisEnabled = false
		model.EnsureColumnNamesInitialized()
		defer func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() }()
		if _, err := ReconcileSettlementFailuresOnce(context.Background(), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "recovery.sqlite")
	db, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.WalletLedger{}, &model.SettlementFailure{}, &model.SettlementOutbox{}, &model.LedgerLegacySettlementWaiver{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "restart-refund", Quota: 900}).Error; err != nil {
		t.Fatal(err)
	}
	// Simulate a worker lost after claiming a refund. The old key has already
	// been removed; all facts needed for the account refund remain on disk.
	if err := db.Create(&model.SettlementFailure{RequestID: "restart-refund", UserID: 1, TokenID: 404, TokenKey: "deleted-fixture-key", QuotaDelta: -100,
		RefType: "relay_settlement", RefID: "restart-refund-event", Status: model.SettlementFailureStatusRetrying, UpdatedAt: time.Now().UTC().Add(-time.Hour), Metadata: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSettlementRefundSurvivesProcessRestartAndReplay$", "-test.count=1")
		cmd.Env = append(os.Environ(), "LAZU_TEST_RECOVERY_DB="+path)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("fresh worker process %d failed: %v\n%s", i, err, out)
		}
	}
	db, err = gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() }()
	var user model.User
	var failure model.SettlementFailure
	var count int64
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&failure).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.WalletLedger{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if user.Quota != 1000 || count != 1 || failure.Status != model.SettlementFailureStatusResolved {
		t.Fatalf("refund did not survive restart exactly once: balance=%d ledger=%d status=%s", user.Quota, count, failure.Status)
	}
}
