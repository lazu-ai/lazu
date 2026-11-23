package model

import (
	"fmt"
	"testing"

	"github.com/lazu-ai/lazu/pkg/billing"
)

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
