package service

import (
	"context"
	"strings"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestLegacyPaymentCannotReconcileMissingInvoice(t *testing.T) {
	oldDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:legacy_invoice_reconciliation?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	t.Cleanup(func() {
		model.SetCoreDB(oldDB)
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&model.TopUp{}); err != nil {
		t.Fatal(err)
	}
	order := model.TopUp{PublicID: "legacy-no-invoice", TradeNo: "ref-legacy-no-invoice", ProjectID: 1,
		Status: common.TopUpStatusSuccess, InvoiceStatus: "legacy"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := ReconcileProjectPaymentDocumentByID(context.Background(), order.PublicID); err == nil || !strings.Contains(err.Error(), "legacy payment") {
		t.Fatalf("legacy payment reconciliation should be unavailable: %v", err)
	}
}
