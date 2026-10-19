package model

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestPostgresOwnershipGuardPreservesIdentityTransaction(t *testing.T) {
	core, telemetry := postgresTelemetryFixture(t)
	if err := InstallIdentityMutationFence(core); err != nil {
		t.Fatal(err)
	}
	if err := InstallStoreOwnershipGuards(); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func() error{
		func() error { var n int64; return core.Model(&Log{}).Count(&n).Error },
		func() error { return core.Exec("DELETE FROM logs").Error },
		func() error { var n int64; return telemetry.Model(&User{}).Count(&n).Error },
		func() error { return telemetry.Exec("DELETE FROM users").Error },
	} {
		if err := operation(); err == nil || !strings.Contains(err.Error(), "ownership violation") {
			t.Fatalf("wrong-store access was not blocked: %v", err)
		}
	}
	committed := false
	if err := core.Transaction(func(tx *gorm.DB) error {
		transaction, ok := tx.Statement.ConnPool.(*identityTransaction)
		if !ok {
			t.Fatalf("identity transaction wrapper lost: %T", tx.Statement.ConnPool)
		}
		transaction.completion = append(transaction.completion, func(success bool) { committed = success })
		return tx.Model(&User{}).Where("id = ?", -1).UpdateColumn("username", "ownership_fixture").Error
	}); err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("identity transaction completion was not called after commit")
	}
	var n int64
	if err := telemetry.Model(&Log{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
}
