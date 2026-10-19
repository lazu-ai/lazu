package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func withWaivedSettlementWorkerTestDB(t *testing.T) (*gorm.DB, model.SettlementFailure) {
	t.Helper()
	db := withSettlementFailureWorkerTestDB(t, strings.ReplaceAll(t.Name(), "/", "_"))
	if err := db.AutoMigrate(&model.SpendBudgetCharge{}, &model.ProjectSpend{}, &model.SpendBudgetReservation{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "waiver-payer", Quota: 1_000}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Token{Id: 2, UserId: 1, ProjectID: 3, Key: "waiver-fixture-key", RemainQuota: 1_000}).Error; err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	row := model.SettlementFailure{
		ProjectID: 3, ActorUserID: 1, RequestID: "waived-request", UserID: 1,
		TokenID: 2, TokenKey: "waiver-fixture-key", QuotaDelta: 100, PreConsumed: 20,
		RefType: "relay_settlement", RefID: "waived-ref", PricingVersion: 12,
		Error: "token quota is not enough", LastError: "token quota is not enough",
		Status: model.SettlementFailureStatusManualReview, Attempts: 2,
		Metadata: "{}", CreatedAt: at, UpdatedAt: at,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&row, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	items, err := model.InventoryLegacySettlementWaiverItems(t.Context(), db, []uint{row.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.ApplyLegacySettlementWaivers(t.Context(), db, model.LedgerLegacySettlementWaiverBatch{
		Items: items, ExpectedTotalMicrousd: 100, MaxTotalMicrousd: 100,
		Reason: "platform absorbs this reviewed legacy additional charge", Actor: "fixture-operator",
		EvidenceSHA256: strings.Repeat("a", 64), At: at.Add(time.Hour), WritersStopped: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, row
}

func assertWaivedSettlementWorkerUnchanged(t *testing.T, db *gorm.DB, source model.SettlementFailure) {
	t.Helper()
	var user model.User
	var token model.Token
	var retained model.SettlementFailure
	for _, read := range []*gorm.DB{db.First(&user, 1), db.First(&token, 2), db.First(&retained, source.ID)} {
		if read.Error != nil {
			t.Fatal(read.Error)
		}
	}
	if user.Quota != 1_000 || token.RemainQuota != 1_000 || token.UsedQuota != 0 {
		t.Fatalf("waiver replay changed balances: wallet=%d key=%d used=%d", user.Quota, token.RemainQuota, token.UsedQuota)
	}
	want, err := model.LedgerLegacySettlementFailureHash(source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := model.LedgerLegacySettlementFailureHash(retained)
	if err != nil || got != want {
		t.Fatalf("worker changed retained failure: err=%v", err)
	}
	for _, table := range []any{&model.WalletLedger{}, &model.RedisLedgerOperation{}, &model.ProjectSpend{}, &model.SpendBudgetCharge{}, &model.SpendBudgetReservation{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("worker wrote %T: rows=%d err=%v", table, count, err)
		}
	}
}

func TestSettlementFailureExplicitRetryPreservesPlatformWaiver(t *testing.T) {
	db, source := withWaivedSettlementWorkerTestDB(t)
	for range 2 {
		outcome, err := reconcileSettlementFailure(source, time.Now().UTC())
		if err != nil || outcome != model.SettlementFailureStatusManualReview {
			t.Fatalf("explicit retry outcome=%q err=%v", outcome, err)
		}
		stats, err := ReconcileSettlementFailuresOnce(t.Context(), time.Now().UTC())
		if err != nil || stats.Scanned != 0 || stats.Claimed != 0 {
			t.Fatalf("background retry stats=%+v err=%v", stats, err)
		}
		assertWaivedSettlementWorkerUnchanged(t, db, source)
	}
}

func TestSettlementFailureForcedRetryCannotEvadePlatformWaiver(t *testing.T) {
	for _, change := range []string{"reopen", "replace-request-and-ref", "stale-claim"} {
		t.Run(change, func(t *testing.T) {
			db, source := withWaivedSettlementWorkerTestDB(t)
			updates := map[string]any{"status": model.SettlementFailureStatusOpen, "updated_at": source.UpdatedAt}
			if change == "replace-request-and-ref" {
				updates["request_id"], updates["ref_id"] = "replacement-request", "replacement-ref"
			}
			if change == "stale-claim" {
				updates["status"] = model.SettlementFailureStatusRetrying
			}
			if err := db.Model(&model.SettlementFailure{}).Where("id = ?", source.ID).Updates(updates).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&source, source.ID).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := ReconcileSettlementFailuresOnce(t.Context(), time.Now().UTC()); err == nil {
				t.Fatal("changed waived source was accepted for retry")
			}
			if _, err := reconcileSettlementFailure(source, time.Now().UTC()); err == nil {
				t.Fatal("explicit retry accepted changed waived source")
			}
			assertWaivedSettlementWorkerUnchanged(t, db, source)
		})
	}
}

func TestSettlementOutboxExplicitRetryCannotReplayPlatformWaiver(t *testing.T) {
	for _, identity := range []string{"original", "same-request-new-ref", "same-ref-new-request"} {
		t.Run(identity, func(t *testing.T) {
			db, source := withWaivedSettlementWorkerTestDB(t)
			outbox := model.SettlementOutbox{
				ProjectID: source.ProjectID, ActorUserID: source.ActorUserID,
				RequestID: source.RequestID, UserID: source.UserID, TokenID: source.TokenID,
				TokenKey: source.TokenKey, QuotaDelta: source.QuotaDelta, PreConsumed: source.PreConsumed,
				RefType: source.RefType, RefID: source.RefID, Status: model.SettlementFailureStatusOpen,
			}
			if identity == "same-request-new-ref" {
				outbox.RefID = "replacement-ref"
			}
			if identity == "same-ref-new-request" {
				outbox.RequestID = "replacement-request"
			}
			for range 2 {
				outcome, err := reconcileSettlementOutbox(outbox, time.Now().UTC())
				if err != nil || outcome != model.SettlementFailureStatusManualReview {
					t.Fatalf("explicit outbox replay outcome=%q err=%v", outcome, err)
				}
				assertWaivedSettlementWorkerUnchanged(t, db, source)
			}
			// A later durable duplicate invalidates the retained waiver evidence;
			// the worker must stop before claiming it, rather than silently pay it.
			if err := db.Create(&outbox).Error; err != nil {
				t.Fatal(err)
			}
			stats, err := ReconcileSettlementOutboxOnce(context.Background(), time.Now().UTC())
			if err == nil || stats.Claimed != 0 {
				t.Fatalf("duplicate outbox was claimed: stats=%+v err=%v", stats, err)
			}
			var retained model.SettlementOutbox
			if err := db.First(&retained, outbox.ID).Error; err != nil {
				t.Fatal(err)
			}
			if retained.Status != model.SettlementFailureStatusOpen || retained.Attempts != 0 {
				t.Fatal("worker mutated conflicting outbox before rejecting it")
			}
			assertWaivedSettlementWorkerUnchanged(t, db, source)
		})
	}
}

func TestSettlementFailurePlatformWaiverAllowsUnrelatedFutureSettlement(t *testing.T) {
	db, source := withWaivedSettlementWorkerTestDB(t)
	future := model.SettlementFailure{
		RequestID: "future-request", UserID: source.UserID, TokenID: source.TokenID,
		TokenKey: source.TokenKey, QuotaDelta: 100, RefType: "relay_settlement", RefID: "future-ref",
		Status: model.SettlementFailureStatusOpen, Metadata: "{}",
	}
	if err := db.Create(&future).Error; err != nil {
		t.Fatal(err)
	}
	stats, err := ReconcileSettlementFailuresOnce(t.Context(), time.Now().UTC())
	if err != nil || stats.Claimed != 1 || stats.Resolved != 1 {
		t.Fatalf("future settlement stats=%+v err=%v", stats, err)
	}
	stats, err = ReconcileSettlementFailuresOnce(t.Context(), time.Now().UTC())
	if err != nil || stats.Claimed != 0 {
		t.Fatalf("repeated future settlement stats=%+v err=%v", stats, err)
	}
	var user model.User
	var token model.Token
	if err := db.First(&user, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&token, 2).Error; err != nil {
		t.Fatal(err)
	}
	if user.Quota != 900 || token.RemainQuota != 900 || token.UsedQuota != 100 {
		t.Fatal("unrelated future request did not settle exactly once")
	}
	if _, err := model.VerifyLegacySettlementWaivers(t.Context(), db); err != nil {
		t.Fatalf("future settlement invalidated original waiver: %v", err)
	}
}
