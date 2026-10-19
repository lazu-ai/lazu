package ledger

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

func TestReadBalancesExactInitializedReadOnlySnapshot(t *testing.T) {
	client := dedicatedScriptRedis(t)
	ctx := context.Background()
	engine := testEngine(client, nil) // A display read must never initialize via Storage.
	want := map[int]int64{1: math.MaxInt64, 2: math.MinInt64, 3: 9007199254740993, 4: -9007199254740993, 5: 0}
	for id, amount := range want {
		if err := client.HSet(ctx, wallet(id), "avail", strconv.FormatInt(amount, 10), "credit_seq", "0", "generation", "g1").Err(); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []map[string]interface{}{
		{"avail": "123", "credit_seq": "0"},
		{"avail": "123", "generation": "g1"},
		{"avail": "9223372036854775808", "credit_seq": "0", "generation": "g1"},
		{"avail": "9e3", "credit_seq": "0", "generation": "g1"},
		{"avail": " 12", "credit_seq": "0", "generation": "g1"},
		{"avail": "+12", "credit_seq": "0", "generation": "g1"},
		{"avail": "012", "credit_seq": "0", "generation": "g1"},
		{"avail": "12", "credit_seq": "-1", "generation": "g1"},
		{"avail": "12", "credit_seq": "9223372036854775808", "generation": "g1"},
		{"avail": "12", "credit_seq": "0.0", "generation": "g1"},
	}
	ids := []int{1, 2, 3, 4, 5, 1, 0, -1, 99}
	for i, fields := range invalid {
		id := i + 10
		ids = append(ids, id)
		if err := client.HSet(ctx, wallet(id), fields).Err(); err != nil {
			t.Fatal(err)
		}
	}
	before, err := client.Keys(ctx, Prefix+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	got, err := engine.ReadBalances(ctx, ids)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot=%v want=%v err=%v", got, want, err)
	}
	after, err := client.Keys(ctx, Prefix+"*").Result()
	slices.Sort(before)
	slices.Sort(after)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read initialized or mutated keys: before=%v after=%v err=%v", before, after, err)
	}
	for id, amount := range want {
		if got, err := client.HGet(ctx, wallet(id), "avail").Result(); err != nil || got != strconv.FormatInt(amount, 10) {
			t.Fatalf("read changed wallet %d: %s %v", id, got, err)
		}
	}
	if err := client.Set(ctx, fence(1), "rebuilding", 0).Err(); err != nil {
		t.Fatal(err)
	}
	got, err = engine.ReadBalances(ctx, []int{1, 2})
	if err != nil || len(got) != 1 || got[2] != math.MinInt64 {
		t.Fatalf("payer fence snapshot=%v err=%v", got, err)
	}
	if err := client.Set(ctx, Prefix+"maintenance", "recovering", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ReadBalances(ctx, []int{2}); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("maintenance admitted display: %v", err)
	}
}

func TestReadBalancesBoundedBatchesAndContinuityFallback(t *testing.T) {
	client := dedicatedScriptRedis(t)
	ctx := context.Background()
	engine := testEngine(client, nil)
	ids := make([]int, 0, 515)
	for id := 1; id <= 513; id++ {
		ids = append(ids, id)
		if err := client.HSet(ctx, wallet(id), "avail", fmt.Sprint(id), "credit_seq", "0", "generation", "g1").Err(); err != nil {
			t.Fatal(err)
		}
	}
	ids = append(ids, 1, 513)
	hook := &scriptCommandHook{}
	client.AddHook(hook)
	got, err := engine.ReadBalances(ctx, ids)
	if err != nil || len(got) != 513 {
		t.Fatalf("bounded snapshot len=%d err=%v", len(got), err)
	}
	if cmds := hook.take(); !reflect.DeepEqual(cmds, []string{"evalsha", "eval", "evalsha", "evalsha"}) {
		t.Fatalf("cold batches=%v", cmds)
	}
	for id := 1; id <= 513; id++ {
		if got[id] != int64(id) {
			t.Fatalf("payer %d=%d", id, got[id])
		}
	}
	if _, err := engine.ReadBalances(ctx, ids); err != nil {
		t.Fatal(err)
	}
	if cmds := hook.take(); !reflect.DeepEqual(cmds, []string{"evalsha", "evalsha", "evalsha"}) {
		t.Fatalf("warm batches=%v", cmds)
	}
	if _, err := engine.ReadBalances(ctx, []int{1, 2, 1}); err != nil {
		t.Fatal(err)
	}
	if cmds := hook.take(); !reflect.DeepEqual(cmds, []string{"evalsha"}) {
		t.Fatalf("single page dispatches=%v", cmds)
	}
	for _, epoch := range []string{"", "changed-epoch"} {
		if epoch == "" {
			client.Del(ctx, ContinuityKey)
		} else {
			client.Set(ctx, ContinuityKey, epoch, 0)
		}
		if _, err := engine.ReadBalances(ctx, ids); !errors.Is(err, ErrContinuity) {
			t.Fatalf("epoch %q error=%v", epoch, err)
		}
	}
	engine.Continuity = ""
	if _, err := engine.ReadBalances(ctx, ids); !errors.Is(err, ErrContinuity) {
		t.Fatalf("unbound engine: %v", err)
	}
	engine.Continuity = "test-epoch:1"
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ReadBalances(ctx, ids); err == nil {
		t.Fatal("closed Redis reported a trusted display")
	}
}

func TestReadBalancesShowsReservedAndSettledMoneyBeforeWriterCommit(t *testing.T) {
	client := dedicatedScriptRedis(t)
	storage := newMemoryStorage(1000, 1000)
	engine := testEngine(client, storage)
	ctx := context.Background()
	input := testInput("wallet-display-before-commit", 200)
	if err := engine.Reserve(ctx, input); err != nil {
		t.Fatal(err)
	}
	got, err := engine.ReadBalances(ctx, []int{1})
	if err != nil || got[1] != 800 {
		t.Fatalf("active reservation display=%v %v", got, err)
	}
	remaining, err := engine.ReadTokenRemaining(ctx, []int{2})
	if err != nil || remaining[2] != 800 {
		t.Fatalf("active Key reservation display=%v %v", remaining, err)
	}
	if err := engine.Settle(ctx, input, 120); err != nil {
		t.Fatal(err)
	}
	got, err = engine.ReadBalances(ctx, []int{1})
	if err != nil || got[1] != 880 {
		t.Fatalf("settled display=%v %v", got, err)
	}
	remaining, err = engine.ReadTokenRemaining(ctx, []int{2})
	if err != nil || remaining[2] != 880 {
		t.Fatalf("settled Key display=%v %v", remaining, err)
	}
	if storage.applyCalls != 0 || storage.snapshot.Balance != 1000 {
		t.Fatalf("display modified permanent storage: %+v", storage)
	}
	if n, err := client.XLen(ctx, Stream).Result(); err != nil || n != 1 {
		t.Fatalf("uncommitted receipt=%d %v", n, err)
	}
}

func TestReadTokenRemainingExactBoundedAndGuarded(t *testing.T) {
	client := dedicatedScriptRedis(t)
	engine := testEngine(client, nil)
	ctx := context.Background()
	ids := make([]int, 0, 515)
	want := map[int]int64{}
	for id := 1; id <= 513; id++ {
		amount := int64(id)
		switch id {
		case 1:
			amount = math.MaxInt64
		case 2:
			amount = math.MinInt64
		case 3:
			amount = 9007199254740993
		case 4:
			amount = -9007199254740993
		case 5:
			amount = 0
		}
		ids = append(ids, id)
		want[id] = amount
		client.Set(ctx, token(id), strconv.FormatInt(amount, 10), 0)
		client.Set(ctx, fmt.Sprintf("%stoken_gen:%d", Prefix, id), "g1", 0)
	}
	// Raw counters without their initialization marker must not be adopted.
	client.Set(ctx, token(514), "999", 0)
	for id, text := range map[int]string{515: "9e3", 516: "9223372036854775808", 517: " 9", 518: "+9", 519: "009"} {
		client.Set(ctx, token(id), text, 0)
		client.Set(ctx, fmt.Sprintf("%stoken_gen:%d", Prefix, id), "g1", 0)
		ids = append(ids, id)
	}
	hook := &scriptCommandHook{}
	client.AddHook(hook)
	got, err := engine.ReadTokenRemaining(ctx, append(ids, 514, 1, 513, 0, -1, 999))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Key exact snapshot=%v want=%v err=%v", got, want, err)
	}
	if cmds := hook.take(); !reflect.DeepEqual(cmds, []string{"evalsha", "eval", "evalsha", "evalsha"}) {
		t.Fatalf("cold bounded Key dispatches=%v", cmds)
	}
	got, err = engine.ReadTokenRemaining(ctx, []int{1, 2, 1})
	if err != nil || len(got) != 2 {
		t.Fatalf("dedupe=%v %v", got, err)
	}
	if cmds := hook.take(); !reflect.DeepEqual(cmds, []string{"evalsha"}) {
		t.Fatalf("warm Key page=%v", cmds)
	}
	client.Set(ctx, Prefix+"maintenance", "held", 0)
	if _, err := engine.ReadTokenRemaining(ctx, []int{1}); !errors.Is(err, ErrRebuilding) {
		t.Fatalf("Key maintenance=%v", err)
	}
	client.Del(ctx, Prefix+"maintenance", ContinuityKey)
	if _, err := engine.ReadTokenRemaining(ctx, []int{1}); !errors.Is(err, ErrContinuity) {
		t.Fatalf("Key missing continuity=%v", err)
	}
	client.Set(ctx, ContinuityKey, "changed", 0)
	if _, err := engine.ReadTokenRemaining(ctx, []int{1}); !errors.Is(err, ErrContinuity) {
		t.Fatalf("Key changed continuity=%v", err)
	}
	engine.Continuity = ""
	if _, err := engine.ReadTokenRemaining(ctx, []int{1}); !errors.Is(err, ErrContinuity) {
		t.Fatalf("Key unbound engine=%v", err)
	}
	engine.Continuity = "test-epoch:1"
	client.Close()
	if _, err := engine.ReadTokenRemaining(ctx, []int{1}); err == nil {
		t.Fatal("closed Key client trusted")
	}
}
