package model

import (
	"fmt"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func TestRetiredMigrationRetriesAtomicallyAndAcrossLogDatabase(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&SettlementOutbox{}, &SettlementFailure{}); err != nil {
		t.Fatal(err)
	}
	owner := User{Username: "migration-retry"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	db.Where("owner_user_id = ?", owner.Id).First(&project)
	addLegacyTokenIsServiceColumn(t, db)
	token := Token{UserId: owner.Id, ProjectID: project.ID, Key: "retired-retry", Name: "retired"}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&token).Updates(map[string]interface{}{"user_id": 0, "is_service": true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []interface{}{
		&SpendBudgetReservation{ProjectID: project.ID, TokenID: token.Id, RequestID: "migration-hold"},
		&SettlementOutbox{ProjectID: project.ID, TokenID: token.Id, UserID: owner.Id, RefID: "retry", PricingVersion: 1},
		&SettlementFailure{ProjectID: project.ID, TokenID: token.Id, UserID: owner.Id, PricingVersion: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("CREATE TRIGGER reject_migration BEFORE UPDATE ON settlement_failures BEGIN SELECT RAISE(ABORT, 'injected'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := normalizeRetiredServiceKeyOwners(db); err == nil {
		t.Fatal("expected injected failure")
	}
	var got Token
	db.First(&got, token.Id)
	if got.UserId != 0 {
		t.Fatal("failed migration committed owner")
	}
	if err := db.Exec("DROP TRIGGER reject_migration").Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := normalizeRetiredServiceKeyOwners(db); err != nil {
			t.Fatal(err)
		}
	}
	for _, probe := range []struct{ table, column string }{{"spend_budget_reservations", "user_id"}, {"spend_budget_reservations", "actor_user_id"}, {"settlement_outboxes", "actor_user_id"}, {"settlement_failures", "actor_user_id"}} {
		var count int64
		if err := db.Table(probe.table).Where("token_id = ? AND "+probe.column+" = ?", token.Id, owner.Id).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("%+v count=%d err=%v", probe, count, err)
		}
	}
	logs, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := logs.DB()
	defer sqlDB.Close()
	if err := logs.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	if err := logs.Create(&Log{ProjectID: project.ID, TokenId: token.Id}).Error; err != nil {
		t.Fatal(err)
	}
	// Already-owned retired rows must still be visited after the primary phase.
	for i := 0; i < 2; i++ {
		if err := reattributeRetiredServiceKeyLogs(db, logs); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	logs.Model(&Log{}).Where("user_id = ?", owner.Id).Count(&count)
	if count != 1 {
		t.Fatal("separate logs not reattributed")
	}
}

func TestProjectPromotionPersistsCashAndBonus(t *testing.T) {
	for _, paid := range []billing.MicroUSD{0, 5_000_000} {
		t.Run(fmt.Sprint(paid), func(t *testing.T) {
			db := setupProjectTestDB(t, true)
			if err := db.AutoMigrate(&WalletLedger{}, &Log{}); err != nil {
				t.Fatal(err)
			}
			oldLog := telemetryDB
			telemetryDB = db
			t.Cleanup(func() { telemetryDB = oldLog })
			owner := User{Username: "promotion", Email: "promotion@example.com"}
			if err := db.Create(&owner).Error; err != nil {
				t.Fatal(err)
			}
			var project Project
			db.Where("owner_user_id = ?", owner.Id).First(&project)
			setTestProjectStripeCustomer(t, db, project.ID, "cus_promo")
			order, err := BeginProjectTopUp(project.ID, owner.Id, "promo", 10)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&order).Update("stripe_promotion_codes", true).Error; err != nil {
				t.Fatal(err)
			}
			receipt := ProjectPaymentReceipt{Reference: order.TradeNo, CustomerID: "cus_promo", SessionID: "cs_promo", PaidMicrousd: paid, Currency: "USD"}
			if _, changed, err := CompleteProjectTopUp(receipt); err != nil || !changed {
				t.Fatalf("%v %v", changed, err)
			}
			if _, changed, err := CompleteProjectTopUp(receipt); err != nil || changed {
				t.Fatalf("replay %v %v", changed, err)
			}
			if err := db.First(&order, order.Id).Error; err != nil {
				t.Fatal(err)
			}
			if order.PaidAmount != paid.Dollars() || order.BonusMicrousd != 10_000_000-paid {
				t.Fatalf("cash=%v bonus=%v", order.PaidAmount, order.BonusMicrousd)
			}
			var updated User
			db.First(&updated, owner.Id)
			if updated.Quota != 10_000_000 {
				t.Fatalf("quota=%v", updated.Quota)
			}
		})
	}
}
