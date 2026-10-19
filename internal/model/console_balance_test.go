package model

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func consoleBalanceRedis(t *testing.T) (*redis.Client, *ledger.Engine) {
	t.Helper()
	path, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server required for isolated console Hot-balance integration")
	}
	dir, err := os.MkdirTemp("", "console-balance-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "redis.sock")
	command := exec.Command(path, "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no", "--logfile", filepath.Join(dir, "redis.log"))
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close(); _ = command.Process.Kill(); _ = command.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for client.Ping(context.Background()).Err() != nil {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(dir, "redis.log"))
			t.Fatalf("owned Redis not ready: %s", log)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := client.Set(context.Background(), ledger.ContinuityKey, "console-test:1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	engine := ledger.New(client, redisLedgerStorage{})
	engine.Continuity = "console-test:1"
	oldEngine, oldRedis := GatewayLedger(), common.RDB
	gatewayLedger.Store(engine)
	common.RDB = client
	t.Cleanup(func() { gatewayLedger.Store(oldEngine); common.RDB = oldRedis })
	return client, engine
}

func seedConsoleHotWallet(t *testing.T, client *redis.Client, id int, amount int64) {
	t.Helper()
	if err := client.HSet(context.Background(), fmt.Sprintf("%swallet:%d", ledger.Prefix, id), "avail", strconv.FormatInt(amount, 10), "credit_seq", "0", "generation", "g1").Err(); err != nil {
		t.Fatal(err)
	}
}

func TestConsoleWalletBalancesExactFallbackAndUnmodifiedCommittedValues(t *testing.T) {
	client, engine := consoleBalanceRedis(t)
	ctx := context.Background()
	committed := map[int]billing.MicroUSD{1: 101, 2: 202, 3: 303, 4: 404, 5: 505, 6: 606}
	seedConsoleHotWallet(t, client, 1, math.MaxInt64)
	seedConsoleHotWallet(t, client, 2, math.MinInt64)
	seedConsoleHotWallet(t, client, 3, 9007199254740993)
	seedConsoleHotWallet(t, client, 4, -9007199254740993)
	// Existing but incomplete and malformed hot rows retain the committed fallback.
	client.HSet(ctx, ledger.Prefix+"wallet:5", "avail", "123", "generation", "g1")
	client.HSet(ctx, ledger.Prefix+"wallet:6", "avail", "9e3", "generation", "g1", "credit_seq", "0")
	want := map[int]billing.MicroUSD{1: math.MaxInt64, 2: math.MinInt64, 3: 9007199254740993, 4: -9007199254740993, 5: 505, 6: 606}
	if got := ReadConsoleWalletBalances(ctx, committed); !reflect.DeepEqual(got, want) {
		t.Fatalf("exact balances=%v want=%v", got, want)
	}
	if committed[1] != 101 || committed[2] != 202 {
		t.Fatal("display overlay mutated input committed balances")
	}
	for _, guard := range []string{"maintenance", "fence:1", "missing_epoch", "changed_epoch", "unbound_engine", "closed_client"} {
		t.Run(guard, func(t *testing.T) {
			client.Del(ctx, ledger.Prefix+"maintenance", ledger.Prefix+"fence:1")
			client.Set(ctx, ledger.ContinuityKey, "console-test:1", 0)
			engine.Continuity = "console-test:1"
			switch guard {
			case "maintenance", "fence:1":
				client.Set(ctx, ledger.Prefix+guard, "held", 0)
			case "missing_epoch":
				client.Del(ctx, ledger.ContinuityKey)
			case "changed_epoch":
				client.Set(ctx, ledger.ContinuityKey, "console-test:2", 0)
			case "unbound_engine":
				engine.Continuity = ""
			case "closed_client":
				client.Close()
			}
			if got := ReadConsoleWalletBalance(ctx, 1, 101); got != 101 {
				t.Fatalf("guard %s exposed %d", guard, got)
			}
		})
	}
}

func TestConsoleProjectBalancesBatchPrivacyAndCoreMoneyAuthority(t *testing.T) {
	testConsoleBatchStores(t, func(t *testing.T, postgres bool) { testConsoleProjectBalances(t, postgres) })
}

func testConsoleProjectBalances(t *testing.T, postgres bool) {
	core, _, trace, _ := consoleBatchFixture(t, postgres)
	client, engine := consoleBalanceRedis(t)
	createConsoleBatchRows(t, core, &[]User{{Id: 1, PublicID: "usr_hot_owner", Username: "hot-owner", Quota: 1000}, {Id: 2, PublicID: "usr_hot_owner2", Username: "hot-owner2", Quota: 2000}, {Id: 3, PublicID: "usr_hot_owner3", Username: "hot-owner3", Quota: 3000}, {Id: 4, PublicID: "usr_hot_viewer", Username: "hot-viewer", Quota: 4000}})
	projects := []Project{{ID: 1, PublicID: "prj_hot_active", OwnerUserID: 1, Name: "Active"}, {ID: 2, PublicID: "prj_hot_paused", OwnerUserID: 2, Name: "Paused"}, {ID: 3, PublicID: "prj_hot_active2", OwnerUserID: 3, Name: "Active second"}}
	createConsoleBatchRows(t, core, &projects)
	member := ProjectMember{ProjectID: 1, UserID: 4, Role: ProjectRoleMember, Status: ProjectMemberActive}
	paused := ProjectMember{ProjectID: 2, UserID: 4, Role: ProjectRoleMember, Status: ProjectMemberSuspended}
	other := ProjectMember{ProjectID: 3, UserID: 4, Role: ProjectRoleMember, Status: ProjectMemberActive}
	createConsoleBatchRows(t, core, &[]ProjectMember{member, paused, other})
	seedConsoleHotWallet(t, client, 1, 777)
	seedConsoleHotWallet(t, client, 2, 666)
	seedConsoleHotWallet(t, client, 3, -9007199254740993)
	oldEngine := GatewayLedger()
	gatewayLedger.Store(nil)
	trace.statements = nil
	baseline, _, err := ListUserProjects(4)
	if err != nil {
		t.Fatal(err)
	}
	queries := len(trace.statements)
	gatewayLedger.Store(oldEngine)
	trace.statements = nil
	got, _, err := ListUserProjects(4)
	if err != nil || len(got) != 3 {
		t.Fatalf("project page=%+v %v", got, err)
	}
	if len(trace.statements) != queries || queries != 4 {
		t.Fatalf("Hot balance added Core queries: before=%d after=%d %v", queries, len(trace.statements), trace.statements)
	}
	if baseline[0].BalanceMicrousd == nil || *baseline[0].BalanceMicrousd != 1000 || got[0].BalanceMicrousd == nil || *got[0].BalanceMicrousd != 777 || got[2].BalanceMicrousd == nil || *got[2].BalanceMicrousd != -9007199254740993 {
		t.Fatalf("Hot project page=%+v baseline=%+v", got, baseline)
	}
	if got[1].BalanceMicrousd != nil || got[1].MemberCount != 0 {
		t.Fatalf("paused member exposed money/count: %+v", got[1])
	}
	row, err := ReadProjectSummary(projects[0], member)
	if err != nil || row.BalanceMicrousd == nil || *row.BalanceMicrousd != 777 {
		t.Fatalf("Hot detail=%+v %v", row, err)
	}
	row, err = ReadProjectSummary(projects[1], paused)
	if err != nil || row.BalanceMicrousd != nil || row.MemberCount != 0 {
		t.Fatalf("paused detail=%+v %v", row, err)
	}
	var saved User
	if err := core.First(&saved, 1).Error; err != nil || saved.Quota != 1000 {
		t.Fatalf("display mutated committed money: %+v %v", saved, err)
	}
	// Real Reserve/Settle, with no writer Drain: console sees Hot money while Core stays committed.
	client.Del(context.Background(), ledger.Prefix+"wallet:1")
	input := ledger.Input{RequestID: "display-writer-paused", PayerID: 1, ActorID: 4, ProjectID: 1, Unlimited: true, Amount: 200, OccurredAt: time.Now().UTC()}
	if err := engine.Reserve(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	row, err = ReadProjectSummary(projects[0], member)
	if err != nil || *row.BalanceMicrousd != 800 {
		t.Fatalf("reserved UI=%+v %v", row, err)
	}
	if err := engine.Settle(context.Background(), input, 120); err != nil {
		t.Fatal(err)
	}
	row, err = ReadProjectSummary(projects[0], member)
	if err != nil || *row.BalanceMicrousd != 880 {
		t.Fatalf("writer-paused settled UI=%+v %v", row, err)
	}
	if err := core.Session(&gorm.Session{}).First(&saved, 1).Error; err != nil || saved.Quota != 1000 {
		t.Fatalf("writer paused Core changed: %+v %v", saved, err)
	}
}

func TestConsoleTokenRemainingPreservesGrantSourcesAndUnlimitedSemantics(t *testing.T) {
	client, engine := consoleBalanceRedis(t)
	ctx := context.Background()
	tokens := []*Token{{Id: 1, RemainQuota: 1000, UsedQuota: 100}, {Id: 2, RemainQuota: 2000, UnlimitedQuota: true}, {Id: 3, RemainQuota: 3000}, {Id: 4, RemainQuota: 4000}}
	for id, amount := range map[int]int64{1: -9007199254740993, 2: 0, 3: 9007199254740993} {
		client.Set(ctx, fmt.Sprintf("%stoken:%d", ledger.Prefix, id), strconv.FormatInt(amount, 10), 0)
		client.Set(ctx, fmt.Sprintf("%stoken_gen:%d", ledger.Prefix, id), "g1", 0)
	}
	want := map[int]billing.MicroUSD{1: -9007199254740993, 2: 2000, 3: 9007199254740993, 4: 4000}
	if got := ReadConsoleTokenRemaining(ctx, append(tokens, nil, tokens[0])); !reflect.DeepEqual(got, want) {
		t.Fatalf("Key displays=%v want=%v", got, want)
	}
	if tokens[0].RemainQuota != 1000 || tokens[0].UsedQuota != 100 || tokens[1].RemainQuota != 2000 {
		t.Fatalf("display changed Key grant or unlimited sources: %+v", tokens)
	}
	core := map[int]billing.MicroUSD{1: 1000, 2: 2000, 3: 3000, 4: 4000}
	for _, guard := range []string{"maintenance", "continuity", "closed"} {
		if guard == "maintenance" {
			client.Set(ctx, ledger.Prefix+"maintenance", "held", 0)
		}
		if guard == "continuity" {
			client.Del(ctx, ledger.Prefix+"maintenance", ledger.ContinuityKey)
		}
		if guard == "closed" {
			client.Close()
		}
		if got := ReadConsoleTokenRemaining(ctx, tokens); !reflect.DeepEqual(got, core) {
			t.Fatalf("Key %s fallback=%v", guard, got)
		}
	}
	gatewayLedger.Store(nil)
	if got := ReadConsoleTokenRemaining(ctx, tokens); !reflect.DeepEqual(got, core) {
		t.Fatalf("no-engine Key fallback=%v", got)
	}
	gatewayLedger.Store(engine)
}
