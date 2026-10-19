package model

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
)

func TestLedgerApplyRejectsPermanentInputBeforeDatabase(t *testing.T) {
	valid := ledgerBatchEvent("permanent-input", 1, 2, 0, 100, time.Now().UTC())
	badIdentity := valid
	badIdentity.OperationID = "different-operation"
	badKind := valid
	badKind.Kind = "release"
	badKind.Amount = 0
	conflict := valid
	conflict.Amount++
	for _, tc := range []struct {
		name   string
		events []ledger.Event
	}{{"identity", []ledger.Event{badIdentity}}, {"release", []ledger.Event{badKind}}, {"conflict", []ledger.Event{valid, conflict}}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := (redisLedgerStorage{}).Apply(context.Background(), tc.events); !errors.Is(err, ledger.ErrPermanentOperation) {
				t.Fatalf("permanent financial input remains retryable: %v", err)
			}
		})
	}
}

func TestLedgerApplySQLiteMissingIdentitiesPermanentAndDatabaseFailuresRetryable(t *testing.T) {
	db := setupSpendBudgetTestDB(t)
	if err := db.AutoMigrate(&User{}, &Token{}, &RedisLedgerOperation{}, &WalletLedger{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&User{Id: 1, Username: "sqlite-payer", Quota: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Token{Id: 2, UserId: 1, Key: "sqlite-key", RemainQuota: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][2]int{{999, 2}, {1, 999}} {
		event := ledgerBatchEvent("sqlite-missing", ids[0], ids[1], 0, 100, time.Now().UTC())
		if err := (redisLedgerStorage{}).Apply(context.Background(), []ledger.Event{event}); !errors.Is(err, ledger.ErrPermanentOperation) {
			t.Fatalf("missing SQLite identity remains retryable: %v", err)
		}
	}
	if err := db.Migrator().DropTable(&RedisLedgerOperation{}); err != nil {
		t.Fatal(err)
	}
	event := ledgerBatchEvent("sqlite-database-error", 1, 2, 0, 100, time.Now().UTC())
	if err := (redisLedgerStorage{}).Apply(context.Background(), []ledger.Event{event}); err == nil || errors.Is(err, ledger.ErrPermanentOperation) {
		t.Fatalf("database failure must remain retryable: %v", err)
	}
}
