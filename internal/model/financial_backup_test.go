package model

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
)

func TestFinancialBackupAuditIntegration(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("isolated TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	old, oldStream := common.RDB, common.RelayEventStream
	common.RDB = client
	common.RelayEventStream = "lazu:test:financial-backup:" + common.GenerateRequestID()
	stream := common.RelayEventStream
	t.Cleanup(func() {
		_ = client.Del(context.Background(), stream, stream+":dead").Err()
		common.RDB, common.RelayEventStream = old, oldStream
		_ = client.Close()
	})
	db := setupTokenKeyStorageTestDB(t, "financial_backup_audit")
	if err := db.AutoMigrate(&User{}, &LedgerCreditSync{}, &LedgerCounterRefresh{}, &LedgerContinuityState{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	spool := t.TempDir()
	if _, err := AuditGatewayBackup(ctx, db, []string{spool}, false); err == nil {
		t.Fatal("missing stopped-writer attestation accepted")
	}
	state := LedgerContinuityState{ID: 1, Epoch: "backup_fixture", AdmissionGeneration: 1, Status: "active"}
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, ledger.ContinuityKey, state.HotValue(), 0).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Del(ctx, ledger.ContinuityKey).Err() })
	if err := client.Set(ctx, ledger.Prefix+"maintenance", "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Del(ctx, ledger.Prefix+"maintenance").Err() })
	if err := os.WriteFile(filepath.Join(spool, "pending.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := AuditGatewayBackup(ctx, db, []string{spool}, true); err == nil {
		t.Fatal("nonempty spool accepted")
	}
	_ = os.Remove(filepath.Join(spool, "pending.json"))
	if err := db.Exec("INSERT INTO users(id,username,password,quota,credit_seq) VALUES (100000001,'backup_fixture','unused-fixture',123,2)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tokens(id,remain_quota) VALUES (100000001,77)").Error; err != nil {
		t.Fatal(err)
	}
	walletKey, tokenKey := ledger.Prefix+"wallet:100000001", ledger.Prefix+"token:100000001"
	t.Cleanup(func() { _ = client.Del(ctx, walletKey, tokenKey).Err() })
	if err := client.HSet(ctx, walletKey, "avail", "123", "credit_seq", "2").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, tokenKey, "76", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := AuditGatewayBackup(ctx, db, []string{spool}, true); err == nil {
		t.Fatal("mismatching committed key balance accepted")
	}
	_ = client.Set(ctx, tokenKey, "77", 0).Err()
	report, err := AuditGatewayBackup(ctx, db, []string{spool}, true)
	if err != nil || report.WalletsVerified != 1 || report.KeysVerified != 1 {
		t.Fatalf("audit=%+v err=%v", report, err)
	}
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"payload": "pending backup fixture"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := AuditGatewayBackup(ctx, db, []string{spool}, true); err == nil {
		t.Fatal("unpersisted telemetry accepted")
	}
	if err := client.Del(ctx, stream).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := AuditGatewayBackup(ctx, db, []string{spool}, true); err != nil {
		t.Fatalf("drained fixture rejected: %v", err)
	}
	credit := LedgerCreditSync{UserID: 100000001, Sequence: 3, Amount: 1}
	if err := db.Create(&credit).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := AuditGatewayBackup(ctx, db, []string{spool}, true); err == nil {
		t.Fatal("pending credit accepted")
	}
}

func TestFinancialBackupOpeningHistoryAndCurrentAuthorityIntegration(t *testing.T) {
	for _, scenario := range []string{"valid waived history", "later matching wallet", "old wallet source changed", "new legacy precharge", "manifest changed", "waiver source changed", "new unresolved failure"} {
		t.Run(scenario, func(t *testing.T) {
			db, client, dirs, proof := ledgerOpeningContinuityFixture(t, true)
			ctx := context.Background()
			ledgerOpeningContinuityBootstrap(t, db, dirs, proof)
			switch scenario {
			case "later matching wallet":
				if err := db.Exec("UPDATE users SET quota = 900, credit_seq = 8 WHERE id = 1").Error; err != nil {
					t.Fatal(err)
				}
				if err := client.HSet(ctx, ledger.Prefix+"wallet:1", "avail", "900", "credit_seq", "8").Err(); err != nil {
					t.Fatal(err)
				}
			case "old wallet source changed":
				if err := db.Exec("UPDATE wallet_ledgers SET metadata = ? WHERE id = 1", "tampered-after-opening").Error; err != nil {
					t.Fatal(err)
				}
			case "new legacy precharge":
				if err := db.Create(&WalletLedger{UserID: 1, DeltaMicrousd: -1, Kind: "precharge", RefType: "relay_preconsume", RefID: "old-writer-after-opening"}).Error; err != nil {
					t.Fatal(err)
				}
			case "manifest changed":
				if err := db.Exec("UPDATE ledger_opening_manifests SET operator_evidence = ?", "tampered-evidence").Error; err != nil {
					t.Fatal(err)
				}
			case "waiver source changed":
				if err := db.Exec("UPDATE settlement_failures SET token_key = ? WHERE id = 1", "changed-waiver-source-secret").Error; err != nil {
					t.Fatal(err)
				}
			case "new unresolved failure":
				legacyWaiverSource(t, db, 2)
			}
			before, err := BuildMigrationProof(ctx, db, "core")
			if err != nil {
				t.Fatal(err)
			}
			report, auditErr := AuditGatewayBackup(ctx, db, dirs, true)
			if scenario == "valid waived history" || scenario == "later matching wallet" {
				if auditErr != nil {
					t.Fatalf("valid current authority rejected: %v", auditErr)
				}
				if scenario == "later matching wallet" && report.WalletsVerified != 1 {
					t.Fatalf("audit did not verify current SQL against Redis: %+v", report)
				}
			} else if auditErr == nil {
				t.Fatal("backup accepted changed historical opening or an unresolved future obligation")
			}
			after, err := BuildMigrationProof(ctx, db, "core")
			if err != nil {
				t.Fatal(err)
			}
			if err := CompareMigrationProofs(before, after); err != nil {
				t.Fatalf("read-only backup audit changed Core state: %v", err)
			}
		})
	}
}

func TestFinancialBackupAndResumeRejectOrphanDeadPayloadIntegration(t *testing.T) {
	db, client, dirs, proof := ledgerOpeningContinuityFixture(t, false)
	ledgerOpeningContinuityBootstrap(t, db, dirs, proof)
	ctx := context.Background()
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: ledger.DeadStream, Values: map[string]interface{}{"event": "unclassified-orphan"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if ids, err := client.HLen(ctx, ledger.Prefix+"dead_ids").Result(); err != nil || ids != 0 {
		t.Fatalf("fixture must lack index: %d %v", ids, err)
	}
	if _, err := AuditGatewayBackup(ctx, db, dirs, true); err == nil {
		t.Fatal("backup accepted orphan financial dead payload")
	}
	if err := ResumeLedgerContinuity(ctx, db, dirs, true); err == nil {
		t.Fatal("resume accepted orphan financial dead payload")
	}
	if exists, _ := client.Exists(ctx, ledger.Prefix+"maintenance").Result(); exists != 1 {
		t.Fatal("failed resume cleared maintenance")
	}
}
