package model

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func isolatedMigrationInit(t *testing.T, initialize func() error) *gorm.DB {
	t.Helper()
	previousCore := coreDB
	previousMaster, previousPricing := common.IsMasterNode, common.PricingSnapshotEnabled
	previousSQLitePath := common.SQLitePath
	previousPG, previousSQLite := common.UsingPostgreSQL, common.UsingSQLite
	previousGroup, previousKey, previousTrue, previousFalse := commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal
	previousLogGroup, previousLogKey := logGroupCol, logKeyCol
	common.IsMasterNode, common.PricingSnapshotEnabled = false, false
	common.SQLitePath = filepath.Join(t.TempDir(), "offline-init.db")
	t.Setenv("DATABASE_URL", "local")
	t.Setenv("LAZU_MIGRATION_MODE", "auto")
	t.Cleanup(func() {
		coreDB = previousCore
		common.IsMasterNode, common.PricingSnapshotEnabled = previousMaster, previousPricing
		common.SQLitePath = previousSQLitePath
		common.UsingPostgreSQL, common.UsingSQLite = previousPG, previousSQLite
		commonGroupCol, commonKeyCol, commonTrueVal, commonFalseVal = previousGroup, previousKey, previousTrue, previousFalse
		logGroupCol, logKeyCol = previousLogGroup, previousLogKey
	})
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	db := coreDB
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return db
}

func TestInitDBMigrationPreservesCallerTransactionDeadline(t *testing.T) {
	for _, test := range []struct {
		name       string
		initialize func() error
		runtime    bool
	}{
		{"runtime", InitDB, true},
		{"offline-migration", InitDBForMigration, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := isolatedMigrationInit(t, test.initialize)
			if installed := db.Callback().Update().Get("identity:begin") != nil; installed != test.runtime {
				t.Fatalf("identity mutation callbacks installed=%t, runtime=%t", installed, test.runtime)
			}
			underlying := &identityDeadlinePool{}
			if fenced, ok := db.ConnPool.(*identityConnPool); ok {
				fenced.ConnPool = underlying
			} else {
				db.ConnPool, db.Statement.ConnPool = underlying, underlying
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			tx := db.WithContext(ctx).Begin()
			if tx.Error != nil {
				t.Fatal(tx.Error)
			}
			defer tx.Rollback()
			deadline, ok := underlying.ctx.Deadline()
			if !ok {
				t.Fatal("caller transaction deadline was lost")
			}
			callerDeadline, _ := ctx.Deadline()
			if test.runtime {
				if _, ok := tx.Statement.ConnPool.(*identityTransaction); !ok {
					t.Fatal("runtime lost its identity transaction fence")
				}
				if remaining := time.Until(deadline); remaining <= 0 || remaining > identityTransactionTimeout || remaining >= identityPendingTTL {
					t.Fatalf("runtime transaction can outlive its identity lease: %s", remaining)
				}
			} else if !deadline.Equal(callerDeadline) {
				t.Fatalf("offline migration inherited a runtime transaction deadline: got %s, want caller deadline %s", time.Until(deadline), time.Until(callerDeadline))
			}
		})
	}
}
