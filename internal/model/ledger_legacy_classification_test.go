package model

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func legacyClassificationFixture(t *testing.T, db *gorm.DB) *gorm.DB {
	t.Helper()
	if db == nil {
		var err error
		db, err = gorm.Open(sqlite.Open(fmt.Sprintf("file:legacy-classification-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
	}
	if err := db.AutoMigrate(&WalletLedger{}, &SettlementFailure{}, &SettlementOutbox{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func legacyPrechargeFixture(t *testing.T, db *gorm.DB, id uint, metadata string) WalletLedger {
	t.Helper()
	row := WalletLedger{ID: id, UserID: 17, DeltaMicrousd: -100, Kind: WalletLedgerKindSpend, RefType: "relay_preconsume", RefID: fmt.Sprintf("relay_preconsume:17:5:%d", id), Metadata: metadata, CreatedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func legacyFinalFixture(t *testing.T, db *gorm.DB, id uint, delta int64, metadata string) WalletLedger {
	t.Helper()
	row := WalletLedger{ID: id, UserID: 17, DeltaMicrousd: billing.MicroUSD(delta), Kind: WalletLedgerKindSpend, RefType: "relay_settlement", RefID: fmt.Sprintf("relay_settlement:17:5:%d", id), Metadata: metadata, CreatedAt: time.Date(2026, 7, 1, 12, 0, 1, 0, time.UTC)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func TestLegacyPrechargeClassificationUsesStableCompletedFacts(t *testing.T) {
	db := legacyClassificationFixture(t, nil)
	legacyPrechargeFixture(t, db, 1, `{"request_id":"known-request"}`)
	legacyFinalFixture(t, db, 21, -20, `{"request_id":"known-request","pre_consumed_quota":100,"token_key":"fixture-secret-never-save"}`)
	legacyPrechargeFixture(t, db, 2, `{}`)
	legacyFinalFixture(t, db, 22, 100, `{"precharge_ref_id":"relay_preconsume:17:5:2","pre_consumed_quota":100}`)
	legacyPrechargeFixture(t, db, 3, `{}`)
	final := legacyFinalFixture(t, db, 23, -20, `{}`)
	if err := db.Create(&SettlementOutbox{ID: 31, UserID: 17, RequestID: "outbox-request", RefType: final.RefType, RefID: final.RefID, QuotaDelta: 20, PreConsumed: 100, Status: SettlementFailureStatusResolved, Metadata: `{"precharge_ledger_id":3}`}).Error; err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		report, err := ClassifyLegacyPrecharges(context.Background(), db, 2)
		if err != nil || report.Settled != 2 || report.Refunded != 1 || report.ManualReview != 0 || report.Processed != 3 {
			t.Fatalf("stable facts were not classified exactly once: %+v %v", report, err)
		}
	}
	proof, err := LoadLegacyPrechargeClassifications(context.Background(), db)
	if err != nil || len(proof) != 3 {
		t.Fatalf("durable classified proof unavailable: %+v %v", proof, err)
	}
	for _, item := range proof {
		if strings.Contains(item.Evidence, "fixture-secret") || item.AmountMicrousd != 100 {
			t.Fatalf("proof exposed credentials or changed money: %+v", item)
		}
	}
	if err := verifyLegacyPrechargeClassifications(context.Background(), db, nil); err != nil {
		t.Fatalf("bootstrap cannot consume stored classifications: %v", err)
	}
	var count int64
	if err := db.Model(&WalletLedger{}).Count(&count).Error; err != nil || count != 6 {
		t.Fatalf("classifier mutated the financial journal: rows=%d err=%v", count, err)
	}
}

func TestLegacyPrechargeClassifierLeavesAmbiguousHistoryForManualReview(t *testing.T) {
	cases := []struct {
		name string
		seed func(*testing.T, *gorm.DB)
	}{
		{"time_amount_token_without_link", func(t *testing.T, db *gorm.DB) {
			legacyFinalFixture(t, db, 21, -20, `{"pre_consumed_quota":100,"token_id":5,"session_id":"same"}`)
		}},
		{"mismatched_amount", func(t *testing.T, db *gorm.DB) {
			legacyFinalFixture(t, db, 21, -20, `{"precharge_ledger_id":1,"pre_consumed_quota":99}`)
		}},
		{"multiple_terminal_facts", func(t *testing.T, db *gorm.DB) {
			for _, id := range []uint{21, 22} {
				legacyFinalFixture(t, db, id, -20, `{"precharge_ledger_id":1,"pre_consumed_quota":100}`)
			}
		}},
		{"unresolved_failure", func(t *testing.T, db *gorm.DB) {
			final := legacyFinalFixture(t, db, 21, -20, `{"precharge_ledger_id":1,"pre_consumed_quota":100}`)
			if err := db.Create(&SettlementFailure{UserID: 17, RequestID: "failed", RefType: final.RefType, RefID: final.RefID, QuotaDelta: 20, PreConsumed: 100, Status: SettlementFailureStatusOpen}).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"resolved_outbox_without_wallet_fact", func(t *testing.T, db *gorm.DB) {
			if err := db.Create(&SettlementOutbox{UserID: 17, RefType: "relay_settlement", RefID: "missing-wallet", QuotaDelta: 20, PreConsumed: 100, Status: SettlementFailureStatusResolved, Metadata: `{"precharge_ledger_id":1}`}).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"refund_exceeds_precharge", func(t *testing.T, db *gorm.DB) {
			legacyFinalFixture(t, db, 21, 101, `{"precharge_ledger_id":1,"pre_consumed_quota":100}`)
		}},
		{"different_user", func(t *testing.T, db *gorm.DB) {
			final := legacyFinalFixture(t, db, 21, -20, `{"precharge_ledger_id":1,"pre_consumed_quota":100}`)
			if err := db.Model(&final).Update("user_id", 18).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{"conflicting_explicit_link", func(t *testing.T, db *gorm.DB) {
			legacyFinalFixture(t, db, 21, -20, `{"precharge_ledger_id":1,"precharge_ref_id":"a-different-precharge","pre_consumed_quota":100}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := legacyClassificationFixture(t, nil)
			legacyPrechargeFixture(t, db, 1, `{"token_id":5,"session_id":"same","pre_consumed_quota":0}`)
			tc.seed(t, db)
			report, err := ClassifyLegacyPrecharges(context.Background(), db, 1)
			if err != nil || report.ManualReview != 1 || report.Settled != 0 || report.Refunded != 0 {
				t.Fatalf("ambiguous financial history was classified: %+v %v", report, err)
			}
			if _, err := LoadLegacyPrechargeClassifications(context.Background(), db); err == nil {
				t.Fatal("ambiguous classification satisfied cutover")
			}
		})
	}
}

func TestLegacyPrechargeClassifierRejectsReusedRequestIdentity(t *testing.T) {
	db := legacyClassificationFixture(t, nil)
	for _, id := range []uint{1, 2} {
		legacyPrechargeFixture(t, db, id, `{"request_id":"reused-request"}`)
	}
	legacyFinalFixture(t, db, 21, -20, `{"request_id":"reused-request","pre_consumed_quota":100}`)
	report, err := ClassifyLegacyPrecharges(context.Background(), db, 2)
	if err != nil || report.ManualReview != 2 || report.Settled != 0 {
		t.Fatalf("one final wallet fact classified two distinct precharges: %+v %v", report, err)
	}
}

func TestLegacyPrechargeAutomaticEvidenceRejectsChangedTerminalFacts(t *testing.T) {
	db := legacyClassificationFixture(t, nil)
	legacyPrechargeFixture(t, db, 1, `{}`)
	final := legacyFinalFixture(t, db, 21, -20, `{"precharge_ledger_id":1,"pre_consumed_quota":100}`)
	if _, err := ClassifyLegacyPrecharges(context.Background(), db, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&final).Update("delta_microusd", -99).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacyPrechargeClassifications(context.Background(), db); err == nil {
		t.Fatal("retained automatic evidence accepted changed terminal financial facts")
	}
}

func TestLegacyPrechargeOperatorImportIsExactDurableAndPreserved(t *testing.T) {
	db := legacyClassificationFixture(t, nil)
	precharge := legacyPrechargeFixture(t, db, 1, `{}`)
	if _, err := ClassifyLegacyPrecharges(context.Background(), db, 1); err != nil {
		t.Fatal(err)
	}
	valid := LedgerLegacyPrechargeClassification{LedgerID: 1, AmountMicrousd: 100, Status: "settled", Evidence: "operator verified archived request and issuer reconciliation fixture"}
	wrong := valid
	wrong.AmountMicrousd = 99
	if err := ImportLegacyPrechargeClassifications(context.Background(), db, []LedgerLegacyPrechargeClassification{wrong}); err == nil {
		t.Fatal("operator import accepted the wrong debit amount")
	}
	if err := ImportLegacyPrechargeClassifications(context.Background(), db, []LedgerLegacyPrechargeClassification{valid, valid}); err == nil {
		t.Fatal("operator import accepted duplicate ledger classifications")
	}
	for repeat := 0; repeat < 2; repeat++ {
		if err := ImportLegacyPrechargeClassifications(context.Background(), db, []LedgerLegacyPrechargeClassification{valid}); err != nil {
			t.Fatal(err)
		}
		if _, err := ClassifyLegacyPrecharges(context.Background(), db, 1); err != nil {
			t.Fatal(err)
		}
		proof, err := LoadLegacyPrechargeClassifications(context.Background(), db)
		if err != nil || len(proof) != 1 || !strings.Contains(proof[0].Evidence, valid.Evidence) {
			t.Fatalf("automatic run lost durable operator evidence: %+v %v", proof, err)
		}
	}
	if err := db.Model(&precharge).Update("delta_microusd", -101).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLegacyPrechargeClassifications(context.Background(), db); err == nil {
		t.Fatal("stored evidence accepted changed source debit")
	}
}

func TestPostgresLegacyPrechargeClassificationScansKeysetBatches(t *testing.T) {
	db, _ := migrationReviewPostgresStores(t)
	legacyClassificationFixture(t, db)
	rows := make([]WalletLedger, 0, 507)
	for id := uint(1); id <= 507; id++ {
		rows = append(rows, WalletLedger{ID: id * 3, UserID: 17, DeltaMicrousd: -100, Kind: WalletLedgerKindSpend, RefType: "relay_preconsume", RefID: fmt.Sprintf("old:%d", id), Metadata: "{}", CreatedAt: time.Now().UTC()})
	}
	if err := db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	legacyFinalFixture(t, db, 2000, 100, `{"precharge_ledger_id":1521,"pre_consumed_quota":100}`)
	report, err := ClassifyLegacyPrecharges(context.Background(), db, 37)
	if err != nil || report.Processed != 507 || report.ManualReview != 506 || report.Refunded != 1 {
		t.Fatalf("keyset batching lost final/gapped entries: %+v %v", report, err)
	}
}
