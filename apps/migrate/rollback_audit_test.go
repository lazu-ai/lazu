package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func rollbackAuditRedis(t *testing.T) (*redis.Client, string, string) {
	t.Helper()
	redisURL := os.Getenv("TEST_LEDGER_ROLLBACK_REDIS_URL")
	if redisURL == "" {
		t.Skip("isolated TEST_LEDGER_ROLLBACK_REDIS_URL required")
	}
	parsed, err := url.Parse(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	// CI reserves databases 1-7 for other packages. Keep CLI subprocesses in
	// their own logical database even when packages run concurrently.
	query := parsed.Query()
	query.Set("db", "8")
	parsed.RawQuery = query.Encode()
	redisURL = parsed.String()
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if count, err := client.DBSize(ctx).Result(); err != nil || count != 0 {
		_ = client.Close()
		t.Fatalf("rollback CLI test requires an empty isolated Redis database: keys=%d err=%v", count, err)
	}
	stream := "lazu:test:rollback-cli:" + common.GenerateRequestID()
	t.Cleanup(func() {
		_ = client.Del(ctx, ledger.ContinuityKey, ledger.Prefix+"maintenance", ledger.Prefix+"wallet:100000001", ledger.Prefix+"token:100000001", ledger.Prefix+"nodes:rollback-cli-fixture", stream, stream+":dead").Err()
		_ = client.Close()
	})
	if err := client.Set(ctx, ledger.Prefix+"maintenance", "rollback test", 0).Err(); err != nil {
		t.Fatal(err)
	}
	return client, redisURL, stream
}

func rollbackAuditSource(t *testing.T) (string, *gorm.DB, model.LedgerContinuityState) {
	t.Helper()
	dsn := "sqlite://" + filepath.Join(t.TempDir(), "rollback-source.sqlite")
	db, err := model.OpenMigrationDatabase(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
	if err := db.AutoMigrate(&model.SchemaVersion{}, &model.User{}, &model.Token{}, &model.WalletLedger{}, &model.LedgerContinuityState{}, &model.LedgerCreditSync{}, &model.LedgerCounterRefresh{}, &model.RedisLedgerOperation{}, &model.ProjectSpend{}, &model.SpendBudget{}, &model.SpendBudgetCharge{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.October, 8, 1, 2, 3, 0, time.UTC)
	state := model.LedgerContinuityState{ID: 1, Epoch: "rollback-cli-fixture", AdmissionGeneration: 1, Status: "active", ActivatedAt: at}
	event := ledger.Event{Input: ledger.Input{RequestID: "rollback-cli-request", PayerID: 100000001, ActorID: 100000001, TokenID: 100000001, ProjectID: 7, Amount: 11, OccurredAt: at}, OperationID: "rollback-cli-request", Kind: "settle"}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []interface{}{
		&model.SchemaVersion{Scope: "primary", Version: 26, AppliedAt: at},
		&state,
		&model.RedisLedgerOperation{ID: "settle:" + event.OperationID, RequestID: event.RequestID, Kind: event.Kind, PayerID: event.PayerID, ActorID: event.ActorID, TokenID: event.TokenID, ProjectID: event.ProjectID, Amount: event.Amount, Input: string(payload), CreatedAt: at},
		&model.ProjectSpend{EventKey: "daily:rollback-cli-fixture", Aggregated: true, ProjectID: 7, AmountMicrousd: 11, OccurredAt: at},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO users(id,username,password,quota,credit_seq) VALUES (100000001,'rollback_fixture','unused-fixture',101,2)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tokens(id,remain_quota) VALUES (100000001,73)").Error; err != nil {
		t.Fatal(err)
	}
	return dsn, db, state
}

func TestRollbackCommandRequiresBackupAuditBeforeWrites(t *testing.T) {
	if os.Getenv("LAZU_ROLLBACK_AUDIT_CHILD") == "1" {
		os.Args = []string{os.Args[0], "-command", "prepare-legacy-rollback", "-writers-stopped", "-legacy-schema-version", "25", "-spool-dirs", os.Getenv("LAZU_ROLLBACK_AUDIT_SPOOLS")}
		main()
		return
	}
	for _, scenario := range []struct{ name, want string }{
		{"missing-core-continuity", "record not found"},
		{"inactive-core-continuity", "active, fully recovered financial continuity"},
		{"missing-redis-continuity", "financial continuity is missing or changed"},
		{"mismatched-redis-continuity", "financial continuity is missing or changed"},
		{"missing-spool-inventory", "provide the spool directory"},
		{"nonempty-spool", "replay or classify all entries"},
		{"wallet-mismatch", "wallet 100000001 differs"},
		{"key-mismatch", "key 100000001 differs"},
		{"live-writer", "writer leases remain live"},
		{"pending-telemetry", "telemetry not drained"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client, redisURL, stream := rollbackAuditRedis(t)
			dsn, db, state := rollbackAuditSource(t)
			ctx := context.Background()
			if err := client.Set(ctx, ledger.ContinuityKey, state.HotValue(), 0).Err(); err != nil {
				t.Fatal(err)
			}
			spool := t.TempDir()
			switch scenario.name {
			case "missing-core-continuity":
				if err := db.Delete(&state).Error; err != nil {
					t.Fatal(err)
				}
			case "inactive-core-continuity":
				if err := db.Model(&state).Update("status", "recovering").Error; err != nil {
					t.Fatal(err)
				}
			case "missing-redis-continuity":
				if err := client.Del(ctx, ledger.ContinuityKey).Err(); err != nil {
					t.Fatal(err)
				}
			case "mismatched-redis-continuity":
				if err := client.Set(ctx, ledger.ContinuityKey, "different-epoch:1", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "missing-spool-inventory":
				spool = " , "
			case "nonempty-spool":
				if err := os.WriteFile(filepath.Join(spool, "pending.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wallet-mismatch":
				if err := client.HSet(ctx, ledger.Prefix+"wallet:100000001", "avail", "100", "credit_seq", "2").Err(); err != nil {
					t.Fatal(err)
				}
			case "key-mismatch":
				if err := client.Set(ctx, ledger.Prefix+"token:100000001", "72", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "live-writer":
				if err := client.Set(ctx, ledger.Prefix+"nodes:rollback-cli-fixture", "active", time.Minute).Err(); err != nil {
					t.Fatal(err)
				}
			case "pending-telemetry":
				if err := client.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"payload": "pending"}}).Err(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := model.BuildMigrationProof(ctx, db, "core")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRollbackCommandRequiresBackupAuditBeforeWrites$")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "LAZU_ROLLBACK_AUDIT_CHILD=1", "LAZU_ROLLBACK_AUDIT_SPOOLS="+spool, "LAZU_MIGRATION_SOURCE_URL="+dsn, "LAZU_MIGRATION_TARGET_URL=", "REDIS_CONN_STRING="+redisURL, "RELAY_EVENT_STREAM="+stream, "APP_ENV=development")
			output, runErr := cmd.CombinedOutput()
			if runErr == nil || !strings.Contains(string(output), scenario.want) {
				t.Errorf("rollback did not reject %s at backup audit: %v\n%s", scenario.name, runErr, output)
			}
			after, err := model.BuildMigrationProof(ctx, db, "core")
			if err != nil {
				t.Fatal(err)
			}
			if err := model.CompareMigrationProofs(before, after); err != nil {
				t.Errorf("rejected rollback changed source financial rows or schema markers: %v", err)
			}
		})
	}
}

type rollbackDeadlinePool struct {
	gorm.ConnPool
	deadlines []time.Time
}

func (pool *rollbackDeadlinePool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	deadline, _ := ctx.Deadline()
	pool.deadlines = append(pool.deadlines, deadline)
	return pool.ConnPool.(gorm.TxBeginner).BeginTx(ctx, options)
}

func (pool *rollbackDeadlinePool) GetDBConn() (*sql.DB, error) {
	return pool.ConnPool.(*sql.DB), nil
}

func TestRollbackSourcePreservesOfflineTransactionDeadline(t *testing.T) {
	for _, runtime := range []bool{false, true} {
		name := "offline"
		if runtime {
			name = "runtime-control"
		}
		t.Run(name, func(t *testing.T) {
			client, _, stream := rollbackAuditRedis(t)
			_, db, state := rollbackAuditSource(t)
			oldRedis, oldCore, oldStream := common.RDB, model.CoreDB(), common.RelayEventStream
			common.RDB, common.RelayEventStream = client, stream
			t.Cleanup(func() { common.RDB, common.RelayEventStream = oldRedis, oldStream; model.SetCoreDB(oldCore) })
			if err := client.Set(context.Background(), ledger.ContinuityKey, state.HotValue(), 0).Err(); err != nil {
				t.Fatal(err)
			}
			pool := &rollbackDeadlinePool{ConnPool: db.ConnPool}
			db.ConnPool, db.Statement.ConnPool = pool, pool
			if runtime {
				if err := model.InstallIdentityMutationFence(db); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			var permanent model.RedisLedgerOperation
			if err := db.First(&permanent, "id = ?", "settle:rollback-cli-request").Error; err != nil {
				t.Fatal(err)
			}
			report, err := prepareLegacyRollbackSource(ctx, db, []string{t.TempDir()}, true)
			if err != nil || report.Operations != 1 || report.LegacyRows != 1 || report.OperationAmount != 11 {
				t.Fatalf("rollback financial projection failed: report=%+v err=%v", report, err)
			}
			if len(pool.deadlines) != 1 {
				t.Fatalf("expected one atomic financial transaction, got deadlines %v", pool.deadlines)
			}
			callerDeadline, _ := ctx.Deadline()
			if runtime {
				if remaining := time.Until(pool.deadlines[0]); remaining <= 0 || remaining > 10*time.Second {
					t.Fatalf("runtime identity transaction lost its 10-second limit: %s", remaining)
				}
			} else if !pool.deadlines[0].Equal(callerDeadline) {
				t.Fatalf("offline rollback inherited runtime deadline: got %s, want %s", pool.deadlines[0], callerDeadline)
			}
			if installed := db.Callback().Update().Get("identity:begin") != nil; installed != runtime {
				t.Fatalf("runtime identity callbacks installed=%t, want %t", installed, runtime)
			}
			var retained model.RedisLedgerOperation
			if err := db.First(&retained, "id = ?", permanent.ID).Error; err != nil || retained != permanent {
				t.Fatalf("rollback changed permanent operation: %+v %v", retained, err)
			}
			var quota, remaining int64
			if err := db.Table("users").Where("id = ?", permanent.PayerID).Pluck("quota", &quota).Error; err != nil || quota != 101 {
				t.Fatalf("rollback changed wallet quota: %d %v", quota, err)
			}
			if err := db.Table("tokens").Where("id = ?", permanent.TokenID).Pluck("remain_quota", &remaining).Error; err != nil || remaining != 73 {
				t.Fatalf("rollback changed Key quota: %d %v", remaining, err)
			}
		})
	}
}
