package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func ledgerOpeningTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	ledgerOpeningTestSchema(t, db)
	return db
}

func ledgerOpeningTestSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&User{}, &Token{}, &WalletLedger{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetReservation{}, &ProjectSpend{}, &RedisLedgerOperation{}, &SettlementFailure{}, &SettlementOutbox{}, &LedgerLegacySettlementWaiver{}, &LedgerLegacyPrechargeRecord{}, &LedgerOpeningManifest{}, &LedgerOpeningChunk{}); err != nil {
		t.Fatal(err)
	}
}

func ledgerOpeningTestRequest() LedgerOpeningRequest {
	return LedgerOpeningRequest{Epoch: "initial-ledger-epoch", OperatorEvidence: "all writers stopped; source backup retained", WritersStopped: true, CapturedAt: time.Date(2026, 10, 6, 12, 30, 0, 123456789, time.UTC)}
}

func ledgerOpeningTestSeed(t *testing.T, db *gorm.DB) {
	t.Helper()
	at := ledgerOpeningTestRequest().CapturedAt.Truncate(time.Microsecond)
	rows := []any{
		&User{Id: 1, Username: "opening-wallet", PublicID: "opening-user", Quota: 9007199254740993, CreditSeq: 7},
		&Token{Id: 1, UserId: 1, ProjectID: 3, PublicID: "opening-key", Key: "credential-must-not-be-copied", RemainQuota: 880, UsedQuota: 12, UnlimitedQuota: true, DeletedAt: gorm.DeletedAt{Time: at.Add(-time.Hour), Valid: true}},
		&WalletLedger{ID: 1, UserID: 1, DeltaMicrousd: -100, Kind: "precharge", RefType: "relay_preconsume", RefID: "unknown-old-request", EventID: "hidden-json-event", Metadata: `{"token_key":"metadata-credential","unknown":"remain unknown"}`, CreatedAt: at.Add(-time.Hour)},
		&WalletLedger{ID: 7, UserID: 1, DeltaMicrousd: 1000, Kind: "credit", RefType: "topup", RefID: "retained-topup", Metadata: "full-old-credit-metadata", CreatedAt: at.Add(-2 * time.Hour)},
		&SpendBudget{ID: 1, Scope: SpendBudgetScopeUser, UserID: 1, Period: SpendBudgetPeriodMonth, LimitMicrousd: 1000, Enforcement: "block", Enabled: true, CreatedAt: at, UpdatedAt: at},
		&SpendBudgetCharge{ID: 1, BudgetID: 1, Scope: SpendBudgetScopeUser, UserID: 1, AmountMicrousd: 21, CreatedAt: at.Add(-time.Hour)},
		&SpendBudgetReservation{ID: 1, BudgetID: 1, RequestID: "expired-hold", ReservedMicrousd: 11, ExpiresAt: at.Unix() - 1, CreatedAt: at.Add(-time.Hour)},
	}
	for _, row := range rows {
		if err := db.Session(&gorm.Session{SkipHooks: true}).Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func ledgerOpeningTestBuild(t *testing.T, db *gorm.DB) (LedgerOpeningManifest, LedgerOpeningBinding) {
	t.Helper()
	manifest, err := BuildLedgerOpening(context.Background(), db, ledgerOpeningTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	return manifest, LedgerOpeningBinding{manifest.ID, manifest.ManifestSHA256}
}

func TestLedgerOpeningEmptyAndIdempotent(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	manifest, binding := ledgerOpeningTestBuild(t, db)
	if manifest.WalletLedgerCount != 0 || manifest.LegacyPrechargeCount != 0 || manifest.WalletLedgerThroughID != 0 || manifest.CapturedAt.Nanosecond() != 123456000 {
		t.Fatalf("unexpected empty opening: %+v", manifest)
	}
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLedgerOpeningBaselines(context.Background(), db, binding); err != nil {
		t.Fatal(err)
	}
	second, err := BuildLedgerOpening(context.Background(), db, ledgerOpeningTestRequest())
	if err != nil || second.ManifestSHA256 != manifest.ManifestSHA256 {
		t.Fatalf("idempotent opening: %+v, %v", second, err)
	}
	request := ledgerOpeningTestRequest()
	request.Epoch = "different-cutover"
	if _, err := BuildLedgerOpening(context.Background(), db, request); err == nil {
		t.Fatal("replacing permanent opening was accepted")
	}
	var chunks int64
	if err := db.Model(&LedgerOpeningChunk{}).Count(&chunks).Error; err != nil || chunks != 0 {
		t.Fatalf("empty chunks=%d, %v", chunks, err)
	}
}

func TestLedgerOpeningPreservesUnknownHistoryAndAllowedBaselines(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	ledgerOpeningTestSeed(t, db)
	var before WalletLedger
	if err := db.First(&before, 1).Error; err != nil {
		t.Fatal(err)
	}
	manifest, binding := ledgerOpeningTestBuild(t, db)
	if manifest.LegacyPrechargeCount != 1 || manifest.WalletLedgerCount != 2 || manifest.WalletLedgerThroughID != 7 {
		t.Fatalf("wrong opening history: %+v", manifest)
	}
	if _, err := LoadLedgerOpening(context.Background(), db, binding); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLedgerOpeningBaselines(context.Background(), db, binding); err != nil {
		t.Fatal(err)
	}
	var after WalletLedger
	if err := db.First(&after, 1).Error; err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(struct {
		Row   WalletLedger
		Event string
	}{before, before.EventID})
	right, _ := json.Marshal(struct {
		Row   WalletLedger
		Event string
	}{after, after.EventID})
	if string(left) != string(right) {
		t.Fatal("opening changed historical precharge")
	}
	var classifications int64
	if err := db.Model(&LedgerLegacyPrechargeRecord{}).Count(&classifications).Error; err != nil || classifications != 0 {
		t.Fatalf("unknown precharge was classified: %d, %v", classifications, err)
	}
	var chunks []LedgerOpeningChunk
	if err := db.Order("section,sequence").Find(&chunks).Error; err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks=%d", len(chunks))
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk.Payload, "credential") || strings.Contains(chunk.Payload, "unknown-old-request") || strings.Contains(chunk.Payload, "full-old-credit-metadata") {
			t.Fatal("opening snapshot copied credential-bearing history")
		}
		if chunk.Section == "wallets" && (!strings.Contains(chunk.Payload, "9007199254740993") || !strings.Contains(chunk.Payload, `"credit_seq":7`)) {
			t.Fatal("wallet authority lost integer precision or credit sequence")
		}
		if chunk.Section == "keys" && (!strings.Contains(chunk.Payload, `"project_id":3`) || !strings.Contains(chunk.Payload, `"remaining_microusd":880`) || !strings.Contains(chunk.Payload, `"used_microusd":12`) || !strings.Contains(chunk.Payload, `"unlimited_quota":true`)) {
			t.Fatalf("incomplete key baseline: %s", chunk.Payload)
		}
		if chunk.Section == "budgets" && !strings.Contains(chunk.Payload, `"used_microusd":21`) {
			t.Fatalf("wrong budget spend: %s", chunk.Payload)
		}
	}
}

func TestLedgerOpeningDetectsHistoricalSourceChanges(t *testing.T) {
	cases := map[string]func(*gorm.DB) error{
		"precharge metadata": func(db *gorm.DB) error {
			return db.Exec("UPDATE wallet_ledgers SET metadata = ? WHERE id = 1", "changed-secret-field").Error
		},
		"precharge hidden event ID": func(db *gorm.DB) error {
			return db.Exec("UPDATE wallet_ledgers SET event_id = ? WHERE id = 1", "changed-event").Error
		},
		"precharge amount": func(db *gorm.DB) error {
			return db.Exec("UPDATE wallet_ledgers SET delta_microusd = -101 WHERE id = 1").Error
		},
		"precharge removal": func(db *gorm.DB) error { return db.Exec("DELETE FROM wallet_ledgers WHERE id = 1").Error },
		"old nonprecharge mutation": func(db *gorm.DB) error {
			return db.Exec("UPDATE wallet_ledgers SET metadata = ? WHERE id = 7", "changed-old-credit").Error
		},
		"old gap insertion": func(db *gorm.DB) error {
			return db.Create(&WalletLedger{ID: 3, UserID: 1, DeltaMicrousd: 10, Kind: "credit", RefType: "topup", RefID: "backdated-insertion"}).Error
		},
		"new legacy precharge": func(db *gorm.DB) error {
			return db.Create(&WalletLedger{ID: 8, UserID: 1, DeltaMicrousd: -10, Kind: "precharge", RefType: "relay_preconsume", RefID: "old-writer-resumed"}).Error
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			db := ledgerOpeningTestDB(t)
			ledgerOpeningTestSeed(t, db)
			_, binding := ledgerOpeningTestBuild(t, db)
			if err := mutate(db); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err == nil {
				t.Fatal("changed or newly appended legacy history was accepted")
			}
			if _, err := BuildLedgerOpening(context.Background(), db, ledgerOpeningTestRequest()); err == nil {
				t.Fatal("opening retry silently recaptured changed history")
			}
		})
	}
}

func TestLedgerOpeningAllowsLaterFinancialActivity(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	ledgerOpeningTestSeed(t, db)
	manifest, binding := ledgerOpeningTestBuild(t, db)
	for _, statement := range []string{
		"UPDATE users SET quota = quota - 10, credit_seq = credit_seq + 1 WHERE id = 1",
		"UPDATE tokens SET remain_quota = 777, used_quota = 115 WHERE id = 1",
		"UPDATE spend_budgets SET limit_microusd = 2000 WHERE id = 1",
		"UPDATE spend_budget_charges SET amount_microusd = 22 WHERE id = 1",
		"DELETE FROM spend_budget_reservations WHERE id = 1",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&WalletLedger{ID: 8, UserID: 1, DeltaMicrousd: -10, Kind: "settle", RefType: "redis_ledger", RefID: "new-durable-operation"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err != nil {
		t.Fatalf("legitimate later financial activity was rejected: %v", err)
	}
	if err := VerifyLedgerOpeningBaselines(context.Background(), db, binding); err == nil {
		t.Fatal("initial-only baseline check failed to detect changed financial authority")
	}
	retry, err := BuildLedgerOpening(context.Background(), db, ledgerOpeningTestRequest())
	if err != nil || retry.ManifestSHA256 != manifest.ManifestSHA256 {
		t.Fatalf("retry recaptured mutable baselines: %v, %+v", err, retry)
	}
}

func TestLedgerOpeningRejectsManifestAndChunkTampering(t *testing.T) {
	cases := map[string]func(*gorm.DB) error{
		"manifest evidence": func(db *gorm.DB) error {
			return db.Exec("UPDATE ledger_opening_manifests SET operator_evidence = ?", "forged evidence").Error
		},
		"manifest epoch": func(db *gorm.DB) error {
			return db.Exec("UPDATE ledger_opening_manifests SET epoch = ?", "another-epoch").Error
		},
		"manifest source proof": func(db *gorm.DB) error {
			return db.Exec("UPDATE ledger_opening_manifests SET source_proofs_json = ?", "[]").Error
		},
		"chunk payload": func(db *gorm.DB) error {
			return db.Exec("UPDATE ledger_opening_chunks SET payload = ? WHERE section = ?", "forged\n", "wallets").Error
		},
		"missing chunk": func(db *gorm.DB) error {
			return db.Exec("DELETE FROM ledger_opening_chunks WHERE section = ?", "keys").Error
		},
		"chunk count": func(db *gorm.DB) error {
			return db.Exec("UPDATE ledger_opening_chunks SET row_count = 20 WHERE section = ?", "keys").Error
		},
		"extra chunk": func(db *gorm.DB) error {
			return db.Create(&LedgerOpeningChunk{OpeningID: 1, Section: "forged", Sequence: 1, RowCount: 1, Payload: "{}\n", SHA256: ledgerOpeningDigest([]byte("{}\n"))}).Error
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			db := ledgerOpeningTestDB(t)
			ledgerOpeningTestSeed(t, db)
			_, binding := ledgerOpeningTestBuild(t, db)
			if err := mutate(db); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err == nil {
				t.Fatal("manifest or snapshot tampering was accepted")
			}
		})
	}
	t.Run("wrong binding", func(t *testing.T) {
		db := ledgerOpeningTestDB(t)
		_, binding := ledgerOpeningTestBuild(t, db)
		binding.OpeningSHA256 = strings.Repeat("0", 64)
		if _, err := LoadLedgerOpening(context.Background(), db, binding); err == nil {
			t.Fatal("wrong permanent Core binding was accepted")
		}
	})
}

func TestLedgerOpeningRejectsEffectiveLegacyReservationsAtomically(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	ledgerOpeningTestSeed(t, db)
	at := ledgerOpeningTestRequest().CapturedAt
	if err := db.Create(&SpendBudgetReservation{ID: 2, BudgetID: 1, RequestID: "still-active", ReservedMicrousd: 20, ExpiresAt: at.Add(time.Hour).Unix()}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLedgerOpening(context.Background(), db, ledgerOpeningTestRequest()); err == nil || !strings.Contains(err.Error(), "effective legacy SQL budget reservations") {
		t.Fatalf("active hold accepted: %v", err)
	}
	for _, value := range []any{&LedgerOpeningManifest{}, &LedgerOpeningChunk{}} {
		var count int64
		if err := db.Model(value).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("partial opening persisted: %d, %v", count, err)
		}
	}
	if err := db.Exec("UPDATE spend_budget_reservations SET reserved_microusd = 0 WHERE id = 2").Error; err != nil {
		t.Fatal(err)
	}
	_, binding := ledgerOpeningTestBuild(t, db)
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerOpeningPaginatedHistoryAndSnapshots(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	const count = ledgerOpeningPageSize + 3
	users := make([]User, 0, count)
	ledgers := make([]WalletLedger, 0, count)
	for id := 1; id <= count; id++ {
		users = append(users, User{Id: id, PublicID: fmt.Sprintf("paged-%d", id), Username: fmt.Sprintf("paged-%d", id), Quota: billing.MicroUSD(id), CreditSeq: int64(id)})
		ledgers = append(ledgers, WalletLedger{ID: uint(id), UserID: id, DeltaMicrousd: -billing.MicroUSD(id), Kind: "precharge", RefType: "relay_preconsume", RefID: fmt.Sprintf("paged-%d", id), EventID: fmt.Sprintf("event-%d", id)})
	}
	if err := db.Session(&gorm.Session{SkipHooks: true}).CreateInBatches(users, 100).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(ledgers, 100).Error; err != nil {
		t.Fatal(err)
	}
	manifest, binding := ledgerOpeningTestBuild(t, db)
	if manifest.WalletLedgerCount != count || manifest.LegacyPrechargeCount != count {
		t.Fatalf("pagination lost history: %+v", manifest)
	}
	var sections []ledgerOpeningSection
	if err := json.Unmarshal([]byte(manifest.SectionsJSON), &sections); err != nil {
		t.Fatal(err)
	}
	if sections[0].Rows != count || sections[0].Chunks != 2 {
		t.Fatalf("pagination lost wallets: %+v", sections)
	}
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE wallet_ledgers SET event_id = ? WHERE id = ?", "last-page-tamper", count).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err == nil {
		t.Fatal("last page mutation escaped history verification")
	}
}

func TestLedgerOpeningFastLoadAndImmutableModels(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	ledgerOpeningTestSeed(t, db)
	manifest, binding := ledgerOpeningTestBuild(t, db)
	if err := db.Model(&manifest).Update("epoch", "changed").Error; err == nil {
		t.Fatal("manifest update hook permitted mutation")
	}
	if err := db.Delete(&manifest).Error; err == nil {
		t.Fatal("manifest deletion hook permitted removal")
	}
	var chunk LedgerOpeningChunk
	if err := db.First(&chunk).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&chunk).Update("payload", "changed").Error; err == nil {
		t.Fatal("chunk update hook permitted mutation")
	}
	if err := db.Delete(&chunk).Error; err == nil {
		t.Fatal("chunk deletion hook permitted removal")
	}
	// A startup metadata load has a deliberately narrower proof boundary. It
	// must neither rescan a multi-million-row ledger nor claim full integrity.
	for _, table := range []string{"wallet_ledgers", "ledger_opening_chunks"} {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadLedgerOpening(context.Background(), db, binding); err != nil {
		t.Fatalf("bounded startup load unexpectedly scanned historical tables: %v", err)
	}
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err == nil {
		t.Fatal("full audit accepted missing historical storage")
	}
}

func TestLedgerOpeningRequiresStoppedWritersAndCallerContext(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	request := ledgerOpeningTestRequest()
	request.WritersStopped = false
	if _, err := BuildLedgerOpening(context.Background(), db, request); err == nil {
		t.Fatal("opening without stopped writers accepted")
	}
	request = ledgerOpeningTestRequest()
	request.OperatorEvidence = " "
	if _, err := BuildLedgerOpening(context.Background(), db, request); err == nil {
		t.Fatal("opening without operator evidence accepted")
	}
	request = ledgerOpeningTestRequest()
	request.CapturedAt = time.Now().Add(time.Hour)
	if _, err := BuildLedgerOpening(context.Background(), db, request); err == nil {
		t.Fatal("future cutover time hid currently effective budget holds")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildLedgerOpening(ctx, db, ledgerOpeningTestRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
}

func TestLedgerOpeningRejectsLegacyHoldsWithoutExpiry(t *testing.T) {
	for _, expiry := range []int64{0, -1} {
		t.Run(fmt.Sprint(expiry), func(t *testing.T) {
			db := ledgerOpeningTestDB(t)
			if err := db.Create(&SpendBudgetReservation{BudgetID: 1, RequestID: "no-expiry", ReservedMicrousd: 1, ExpiresAt: expiry}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := BuildLedgerOpening(context.Background(), db, ledgerOpeningTestRequest()); err == nil {
				t.Fatal("unexpired legacy SQL budget hold was silently discarded")
			}
		})
	}
}

func TestLedgerOpeningPermanentlyBindsWaiverSetAndFullSources(t *testing.T) {
	for _, scenario := range []string{"retained", "source secret", "source status", "waiver reason", "new waiver", "new unwaived failure"} {
		t.Run(scenario, func(t *testing.T) {
			db := ledgerOpeningTestDB(t)
			ledgerOpeningTestSeed(t, db)
			legacyWaiverSource(t, db, 1)
			if _, err := ApplyLegacySettlementWaivers(context.Background(), db, legacyWaiverBatch(t, db, 1)); err != nil {
				t.Fatal(err)
			}
			_, binding := ledgerOpeningTestBuild(t, db)
			switch scenario {
			case "source secret":
				if err := db.Exec("UPDATE settlement_failures SET token_key = ? WHERE id = 1", "different-secret").Error; err != nil {
					t.Fatal(err)
				}
			case "source status":
				if err := db.Exec("UPDATE settlement_failures SET status = ? WHERE id = 1", SettlementFailureStatusResolved).Error; err != nil {
					t.Fatal(err)
				}
			case "waiver reason":
				if err := db.Exec("UPDATE ledger_legacy_settlement_waivers SET reason = ? WHERE failure_id = 1", "different-disposition").Error; err != nil {
					t.Fatal(err)
				}
			case "new waiver":
				legacyWaiverSource(t, db, 2)
				if _, err := ApplyLegacySettlementWaivers(context.Background(), db, legacyWaiverBatch(t, db, 2)); err != nil {
					t.Fatal(err)
				}
			case "new unwaived failure":
				legacyWaiverSource(t, db, 2)
			}
			_, err := VerifyLedgerOpeningHistory(context.Background(), db, binding)
			if scenario == "retained" || scenario == "new unwaived failure" {
				if err != nil {
					t.Fatalf("opening incorrectly bound unrelated future failure activity: %v", err)
				}
			} else if err == nil {
				t.Fatal("opening accepted changed historical waiver scope or its source")
			}
		})
	}
}

func TestLedgerOpeningRejectsInvalidWaiverBeforePersistence(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	ledgerOpeningTestSeed(t, db)
	legacyWaiverSource(t, db, 1)
	if _, err := ApplyLegacySettlementWaivers(context.Background(), db, legacyWaiverBatch(t, db, 1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE settlement_failures SET token_key = ? WHERE id = 1", "source-changed-before-cutover").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLedgerOpening(context.Background(), db, ledgerOpeningTestRequest()); err == nil {
		t.Fatal("opening committed an already invalid legacy waiver")
	}
	var count int64
	if err := db.Model(&LedgerOpeningManifest{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid waiver left opening state: %d, %v", count, err)
	}
}

func TestLedgerOpeningHashesPersistedNullAndAdditionalColumns(t *testing.T) {
	db := ledgerOpeningTestDB(t)
	ledgerOpeningTestSeed(t, db)
	if err := db.Exec("ALTER TABLE wallet_ledgers ADD COLUMN historical_extra TEXT").Error; err != nil {
		t.Fatal(err)
	}
	_, binding := ledgerOpeningTestBuild(t, db)
	if err := db.Exec("UPDATE wallet_ledgers SET historical_extra = '' WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err == nil {
		t.Fatal("full-column proof conflated NULL with empty or omitted an unknown historical column")
	}
}

func TestPostgresLedgerOpeningRetainsSQLAuthorityAndHistory(t *testing.T) {
	db, _ := migrationReviewPostgresStores(t)
	ledgerOpeningTestSchema(t, db)
	ledgerOpeningTestSeed(t, db)
	legacyWaiverSource(t, db, 1)
	if _, err := ApplyLegacySettlementWaivers(context.Background(), db, legacyWaiverBatch(t, db, 1)); err != nil {
		t.Fatal(err)
	}
	manifest, binding := ledgerOpeningTestBuild(t, db)
	if err := VerifyLedgerOpeningBaselines(context.Background(), db, binding); err != nil {
		t.Fatal(err)
	}
	if manifest.CapturedAt.Nanosecond() != 123456000 {
		t.Fatal("manifest timestamp did not survive PostgreSQL precision")
	}
	if err := db.Exec("UPDATE users SET quota = quota - 1 WHERE id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err != nil {
		t.Fatalf("current wallet was incorrectly treated as historical authority: %v", err)
	}
	if err := VerifyLedgerOpeningBaselines(context.Background(), db, binding); err == nil {
		t.Fatal("initial activation accepted a changed SQL balance")
	}
	if err := db.Exec("UPDATE wallet_ledgers SET metadata = ? WHERE id = 1", "postgres-source-change").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyLedgerOpeningHistory(context.Background(), db, binding); err == nil {
		t.Fatal("PostgreSQL history mutation escaped full-column proof")
	}
	var classifications int64
	if err := db.Model(&LedgerLegacyPrechargeRecord{}).Count(&classifications).Error; err != nil || classifications != 0 {
		t.Fatal("opening classified unknown historical precharges")
	}
}
