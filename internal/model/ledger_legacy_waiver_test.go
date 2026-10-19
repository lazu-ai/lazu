package model

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func legacyWaiverFixture(t *testing.T, postgres bool) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	if postgres {
		db, _ = migrationReviewPostgresStores(t)
	} else {
		db, _ = newLedgerTestDBs(t)
	}
	if err := db.AutoMigrate(&User{}, &Token{}, &SettlementFailure{}, &SettlementOutbox{}, &WalletLedger{}, &RedisLedgerOperation{}, &LedgerLegacySettlementWaiver{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func legacyWaiverSource(t *testing.T, db *gorm.DB, id uint) SettlementFailure {
	t.Helper()
	at := time.Date(2026, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	row := SettlementFailure{ID: id, RequestID: fmt.Sprintf("waiver-request-%d", id), UserID: 41, TokenID: 43, ProjectID: 47, ActorUserID: 53,
		TokenKey: "private-test-key", QuotaDelta: 17, PreConsumed: 5, RefType: "relay_settlement", RefID: fmt.Sprintf("waiver-ref-%d", id), PricingVersion: 59,
		Status: SettlementFailureStatusManualReview, Attempts: 3, Error: "original-private-error", LastError: "retained-private-error", Metadata: `{"token_key":"private-metadata-key","private":"retain"}`,
		CreatedAt: at, UpdatedAt: at.Add(time.Minute)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&row, id).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func legacyWaiverBatch(t *testing.T, db *gorm.DB, ids ...uint) LedgerLegacySettlementWaiverBatch {
	t.Helper()
	items, err := InventoryLegacySettlementWaiverItems(context.Background(), db, ids)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, row := range items {
		total += row.AmountMicrousd
	}
	return LedgerLegacySettlementWaiverBatch{Items: items, ExpectedTotalMicrousd: total, MaxTotalMicrousd: total, WritersStopped: true,
		Reason: "platform absorbs reviewed legacy additional debit", Actor: "operator-fixture", EvidenceSHA256: legacyWaiverStringHash("private approval retained separately"),
		At: time.Date(2026, time.January, 3, 4, 5, 6, 123456789, time.UTC)}
}

func runLegacyWaiverPreservation(t *testing.T, postgres bool) {
	db := legacyWaiverFixture(t, postgres)
	user := User{Id: 41, Username: "waiver-fixture", Quota: 1234, CreditSeq: 7}
	token := Token{Id: 43, UserId: 41, Key: "private-test-key", RemainQuota: 5678, UsedQuota: 99}
	for _, value := range []any{&user, &token} {
		if err := db.Session(&gorm.Session{SkipHooks: true}).Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	original := legacyWaiverSource(t, db, 1)
	batch := legacyWaiverBatch(t, db, 1)
	report, err := ApplyLegacySettlementWaivers(context.Background(), db, batch)
	if err != nil || report.Created != 1 || report.WaivedMicrousd != 17 {
		t.Fatalf("apply: %+v %v", report, err)
	}
	report, err = ApplyLegacySettlementWaivers(context.Background(), db, batch)
	if err != nil || report.Created != 0 || report.Existing != 1 {
		t.Fatalf("idempotent replay: %+v %v", report, err)
	}
	var retained SettlementFailure
	if err := db.First(&retained, original.ID).Error; err != nil || !reflect.DeepEqual(retained, original) {
		t.Fatal("waiver changed original failure facts")
	}
	var afterUser User
	var afterToken Token
	if err := db.First(&afterUser, 41).Error; err != nil || afterUser.Quota != 1234 || afterUser.CreditSeq != 7 {
		t.Fatal("waiver changed existing wallet/credit sequence")
	}
	if err := db.First(&afterToken, 43).Error; err != nil || afterToken.RemainQuota != 5678 || afterToken.UsedQuota != 99 || afterToken.Key != "private-test-key" {
		t.Fatal("waiver changed existing key balance or credential")
	}
	for _, model := range []any{&WalletLedger{}, &RedisLedgerOperation{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("waiver created a paid/settled financial fact")
		}
	}
	waivers, err := VerifyLegacySettlementWaivers(context.Background(), db)
	if err != nil || len(waivers) != 1 {
		t.Fatalf("verify: %v", err)
	}
	public, _ := json.Marshal(waivers)
	if strings.Contains(string(public), "private-test-key") || strings.Contains(string(public), "private-metadata-key") || strings.Contains(string(public), "original-private-error") {
		t.Fatal("waiver audit leaked private source data")
	}
	if ids, err := VerifiedUnresolvedSettlementFailureIDs(context.Background(), db); err != nil || len(ids) != 0 {
		t.Fatalf("verified disposition remained unresolved: %v %v", ids, err)
	}
	future := legacyWaiverSource(t, db, 2)
	if ids, err := VerifiedUnresolvedSettlementFailureIDs(context.Background(), db); err != nil || !reflect.DeepEqual(ids, []uint{future.ID}) {
		t.Fatalf("old waiver hid future failure: %v %v", ids, err)
	}
	if count, err := VerifiedUnresolvedSettlementFailureCount(context.Background(), db, 41); err != nil || count != 1 {
		t.Fatalf("unresolved count: %d %v", count, err)
	}
	if blocked, err := LegacySettlementWaiverBlocksOperation(context.Background(), db, original.UserID, original.RefType, original.RefID, original.RequestID); err != nil || !blocked {
		t.Fatalf("waived operation may replay: %t %v", blocked, err)
	}
	if blocked, err := LegacySettlementWaiverBlocksOperation(context.Background(), db, future.UserID, future.RefType, future.RefID, future.RequestID); err != nil || blocked {
		t.Fatalf("new operation was hidden: %t %v", blocked, err)
	}
	if err := db.Model(&LedgerLegacySettlementWaiver{}).Where("failure_id = ?", original.ID).Update("reason", "changed").Error; err == nil {
		t.Fatal("waiver allowed update")
	}
	if err := db.Delete(&waivers[0]).Error; err == nil {
		t.Fatal("waiver allowed delete")
	}
	// Administrative/raw changes cannot silently invalidate an already retained
	// disposition: verification and replay must both fail closed.
	if err := db.Exec("UPDATE settlement_failures SET status = ? WHERE id = ?", SettlementFailureStatusOpen, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifiedUnresolvedSettlementFailureIDs(context.Background(), db); err == nil {
		t.Fatal("reopened source still passed its immutable commitment")
	}
	if _, err := LegacySettlementWaiverBlocksOperation(context.Background(), db, original.UserID, original.RefType, original.RefID, original.RequestID); err == nil {
		t.Fatal("reopened waived operation became chargeable")
	}
}

func TestLegacySettlementWaiverPreservesFactsAndFutureFailures(t *testing.T) {
	runLegacyWaiverPreservation(t, false)
}
func TestPostgresLegacySettlementWaiverPreservesFactsAndFutureFailures(t *testing.T) {
	runLegacyWaiverPreservation(t, true)
}

func TestPostgresLegacySettlementWaiverAtomicConflictAndIdempotentAudit(t *testing.T) {
	db := legacyWaiverFixture(t, true)
	legacyWaiverSource(t, db, 1)
	legacyWaiverSource(t, db, 2)
	batch := legacyWaiverBatch(t, db, 1, 2)
	validHash := batch.Items[1].SourceSHA256
	batch.Items[1].SourceSHA256 = legacyWaiverStringHash("stale reviewed source")
	if _, err := ApplyLegacySettlementWaivers(context.Background(), db, batch); err == nil {
		t.Fatal("stale second source accepted")
	}
	var count int64
	if err := db.Model(&LedgerLegacySettlementWaiver{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("PostgreSQL committed the first item of a failed batch: %d %v", count, err)
	}
	batch.Items[1].SourceSHA256 = validHash
	if report, err := ApplyLegacySettlementWaivers(context.Background(), db, batch); err != nil || report.Created != 2 {
		t.Fatalf("retry after atomic rollback: %+v %v", report, err)
	}
	batch.Reason = "different approval"
	if _, err := ApplyLegacySettlementWaivers(context.Background(), db, batch); err == nil {
		t.Fatal("idempotent replay overwrote original audit reason")
	}
	if rows, err := VerifyLegacySettlementWaivers(context.Background(), db); err != nil || len(rows) != 2 || rows[0].Reason == batch.Reason {
		t.Fatalf("conflicting replay modified retained audit: %v", err)
	}
}

func TestLegacySettlementWaiverRejectsInvalidOrConflictingBatchAtomically(t *testing.T) {
	for _, scenario := range []string{"hash", "amount", "cap", "total", "duplicate-item", "overflow", "writers", "scope", "nonpositive", "negative-preconsume", "resolved", "duplicate-identity", "paid", "permanent", "outbox", "secret-change"} {
		t.Run(scenario, func(t *testing.T) {
			db := legacyWaiverFixture(t, false)
			legacyWaiverSource(t, db, 1)
			second := legacyWaiverSource(t, db, 2)
			batch := legacyWaiverBatch(t, db, 1, 2)
			switch scenario {
			case "hash":
				batch.Items[1].SourceSHA256 = legacyWaiverStringHash("wrong")
			case "amount":
				batch.Items[1].AmountMicrousd++
				batch.ExpectedTotalMicrousd++
				batch.MaxTotalMicrousd++
			case "cap":
				batch.MaxTotalMicrousd--
			case "total":
				batch.ExpectedTotalMicrousd--
			case "duplicate-item":
				batch.Items[1] = batch.Items[0]
			case "overflow":
				batch.Items[0].AmountMicrousd = math.MaxInt64
			case "writers":
				batch.WritersStopped = false
			case "scope":
				db.Model(&second).Update("ref_type", "unrelated")
			case "nonpositive":
				db.Model(&second).Update("quota_delta", 0)
			case "negative-preconsume":
				db.Model(&second).Update("pre_consumed", -1)
			case "resolved":
				db.Model(&second).Update("status", SettlementFailureStatusResolved)
			case "duplicate-identity":
				second.ID = 3
				if err := db.Create(&second).Error; err != nil {
					t.Fatal(err)
				}
			case "paid":
				if err := db.Create(&WalletLedger{UserID: second.UserID, RefType: second.RefType, RefID: second.RefID, DeltaMicrousd: -17}).Error; err != nil {
					t.Fatal(err)
				}
			case "permanent":
				if err := db.Create(&RedisLedgerOperation{ID: "settle:" + second.RequestID, RequestID: second.RequestID}).Error; err != nil {
					t.Fatal(err)
				}
			case "outbox":
				if err := db.Create(&SettlementOutbox{UserID: second.UserID, RequestID: second.RequestID, RefType: second.RefType, RefID: second.RefID, QuotaDelta: billing.MicroUSD(17)}).Error; err != nil {
					t.Fatal(err)
				}
			case "secret-change":
				db.Model(&second).Update("token_key", "changed-private-key")
			}
			if _, err := ApplyLegacySettlementWaivers(context.Background(), db, batch); err == nil {
				t.Fatal("invalid batch was accepted")
			}
			var count int64
			if err := db.Model(&LedgerLegacySettlementWaiver{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("failed batch left partial records: %d %v", count, err)
			}
		})
	}
}

func TestLegacySettlementWaiverRejectsSourceAndAuditTampering(t *testing.T) {
	for _, column := range []string{"metadata", "token_key", "project_id", "actor_user_id", "quota_delta", "attempts", "error", "last_error", "reason"} {
		t.Run(column, func(t *testing.T) {
			db := legacyWaiverFixture(t, false)
			legacyWaiverSource(t, db, 1)
			batch := legacyWaiverBatch(t, db, 1)
			if _, err := ApplyLegacySettlementWaivers(context.Background(), db, batch); err != nil {
				t.Fatal(err)
			}
			table, idColumn := "settlement_failures", "id"
			var value any = "tampered"
			if column == "project_id" || column == "actor_user_id" || column == "quota_delta" || column == "attempts" {
				value = 101
			}
			if column == "reason" {
				table, idColumn = "ledger_legacy_settlement_waivers", "failure_id"
			}
			if err := db.Exec("UPDATE "+table+" SET "+column+" = ? WHERE "+idColumn+" = ?", value, 1).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyLegacySettlementWaivers(context.Background(), db); err == nil {
				t.Fatal("tampering was accepted")
			}
		})
	}
}

func TestLegacySettlementWaiverCannotBeEvadedByChangingBothIdentities(t *testing.T) {
	db := legacyWaiverFixture(t, false)
	original := legacyWaiverSource(t, db, 1)
	batch := legacyWaiverBatch(t, db, 1)
	if _, err := ApplyLegacySettlementWaivers(context.Background(), db, batch); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE settlement_failures SET request_id = ?, ref_id = ?, status = ? WHERE id = ?", "changed-request", "changed-ref", SettlementFailureStatusOpen, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	var changed SettlementFailure
	if err := db.First(&changed, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LegacySettlementFailureWaiverBlocksReplay(context.Background(), db, changed); err == nil {
		t.Fatal("changing both identities evaded the permanent source-ID guard")
	}
	if claimed, err := ClaimSettlementFailureForRetry(original.ID, time.Now().UTC()); err == nil || claimed {
		t.Fatalf("changed waived source was claimed: %t %v", claimed, err)
	}
}
