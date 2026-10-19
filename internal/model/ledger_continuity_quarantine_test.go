package model

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
)

func continuityQuarantineRedis(t *testing.T) *redis.Client {
	t.Helper()
	if os.Getenv("TEST_REDIS_URL") == "" {
		t.Skip("isolated TEST_REDIS_URL required")
	}
	options, err := redis.ParseURL(os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	old := common.RDB
	common.RDB = client
	clear := func() {
		keys, err := client.Keys(context.Background(), ledger.Prefix+"*").Result()
		if err == nil && len(keys) > 0 {
			client.Del(context.Background(), keys...)
		}
	}
	clear()
	t.Cleanup(func() { clear(); common.RDB = old; _ = client.Close() })
	return client
}

func continuityQuarantineFixture(t *testing.T, directory string, payload bool) {
	t.Helper()
	quarantine := filepath.Join(directory, "quarantine")
	if err := os.MkdirAll(quarantine, 0700); err != nil {
		t.Fatal(err)
	}
	if payload {
		engine := ledger.New(nil, nil)
		engine.SpoolDir = directory
		if err := engine.PersistSettlement(ledger.Input{RequestID: "quarantined-request", PayerID: 17, Unlimited: true, Amount: 37, OccurredAt: time.Now().UTC()}, 37, "settle"); err != nil {
			t.Fatal(err)
		}
		files, err := filepath.Glob(filepath.Join(directory, "*.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("create quarantine fixture: %v %v", files, err)
		}
		if err := os.Rename(files[0], filepath.Join(quarantine, filepath.Base(files[0]))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(quarantine, "unresolved.reason"), []byte(`{"reason":"retained permanent error; operator review required"}`), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerContinuityRejectsQuarantinedEvidenceBeforeResume(t *testing.T) {
	for _, payload := range []bool{true, false} {
		name := "manifest_only"
		if payload {
			name = "payload_and_manifest"
		}
		t.Run(name, func(t *testing.T) {
			client := continuityQuarantineRedis(t)
			db, _ := newLedgerTestDBs(t)
			if err := db.AutoMigrate(RedisLedgerModels()...); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := BootstrapLedgerContinuity(ctx, db); err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			continuityQuarantineFixture(t, directory, payload)
			if err := client.Set(ctx, ledger.Prefix+"maintenance", "operator-review", 0).Err(); err != nil {
				t.Fatal(err)
			}
			report, err := ReportLedgerContinuity(ctx, db, []string{directory})
			if err == nil || !strings.Contains(err.Error(), "quarantin") {
				t.Errorf("inventory silently accepted quarantined financial evidence: report=%+v err=%v", report, err)
			}
			if payload && report.SpoolFiles[directory] == 0 {
				t.Error("inventory omitted quarantined payload exposure")
			}
			if err := ResumeLedgerContinuity(ctx, db, []string{directory}, true); err == nil {
				t.Error("resume cleared maintenance despite unresolved quarantine")
			}
			if exists, _ := client.Exists(ctx, ledger.Prefix+"maintenance").Result(); exists != 1 {
				t.Error("failed resume removed maintenance")
			}
			if _, _, err := collectLedgerRecoveryEvents(ctx, []string{directory}, nil); err == nil {
				t.Error("recovery collection omitted unresolved quarantine")
			}
		})
	}
}

func TestLedgerContinuityRejectsQuarantineBeforeFirstBootstrap(t *testing.T) {
	continuityQuarantineRedis(t)
	db, _ := newLedgerTestDBs(t)
	if err := db.AutoMigrate(RedisLedgerModels()...); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	continuityQuarantineFixture(t, directory, true)
	if _, err := BootstrapLedgerContinuityWithProof(context.Background(), db, []string{directory}, true, nil); err == nil {
		t.Fatal("bootstrap admitted with unresolved quarantine")
	}
}

func TestLedgerRecoveryRejectsNegativeFinancialOwnerIDs(t *testing.T) {
	base := ledger.Event{Input: ledger.Input{RequestID: "recover-id", PayerID: 17, Amount: 37, Unlimited: true}, OperationID: "recover-id", Kind: "settle"}
	if err := validateRecoveryEvent(base); err != nil {
		t.Fatalf("zero legacy actor/project/token IDs must remain valid: %v", err)
	}
	for _, field := range []string{"actor", "project", "token"} {
		t.Run(field, func(t *testing.T) {
			event := base
			switch field {
			case "actor":
				event.ActorID = -1
			case "project":
				event.ProjectID = -1
			case "token":
				event.TokenID = -1
			}
			if err := validateRecoveryEvent(event); err == nil {
				t.Fatalf("recovery accepted negative %s ID", field)
			}
		})
	}
}
