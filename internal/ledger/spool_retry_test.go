package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readOnlySpoolEvent(t *testing.T, engine *Engine) (string, []byte, Event) {
	t.Helper()
	entries, err := os.ReadDir(engine.SpoolDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("spool entries=%v err=%v", entries, err)
	}
	path := filepath.Join(engine.SpoolDir, entries[0].Name())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	return path, raw, event
}

func TestSpoolRetryChangedTelemetryRetainsFirstFacts(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	var failures atomic.Int64
	engine.SpoolFailure = func(error) { failures.Add(1) }
	input := testInput("retry-facts", 100)
	first := `{"created_at":1,"gateway_post_ms":2}`
	if err := engine.PersistSettlementWithTelemetry(input, 70, "settle", first); err != nil {
		t.Fatal(err)
	}
	_, original, _ := readOnlySpoolEvent(t, engine)
	input.OccurredAt = input.OccurredAt.In(time.FixedZone("same-instant", 8*60*60))
	input.Budgets, input.Generations, input.Rates = []Budget{}, []Generation{}, []RateLimit{}
	for _, next := range []string{`{"created_at":2,"gateway_post_ms":5}`, "", first} {
		if err := engine.PersistSettlementWithTelemetry(input, 70, "settle", next); err != nil {
			t.Fatalf("same financial operation rejected changed diagnostics: %v", err)
		}
	}
	_, retained, event := readOnlySpoolEvent(t, engine)
	if !bytes.Equal(original, retained) || event.TelemetryEnvelope != first || event.Amount != 70 || failures.Load() != 0 {
		t.Fatalf("retry rewrote durable intent or latched admission: event=%+v failures=%d", event, failures.Load())
	}
}

func TestSpoolRetryFinancialConflictsRetainFactsWithoutDiskFailure(t *testing.T) {
	changes := []struct {
		name   string
		mutate func(*Input, *int64)
	}{
		{"amount", func(_ *Input, actual *int64) { *actual += 1 }},
		{"payer", func(in *Input, _ *int64) { in.PayerID++ }},
		{"actor", func(in *Input, _ *int64) { in.ActorID++ }},
		{"project", func(in *Input, _ *int64) { in.ProjectID++ }},
		{"token", func(in *Input, _ *int64) { in.TokenID++ }},
		{"unlimited", func(in *Input, _ *int64) { in.Unlimited = !in.Unlimited }},
		{"free_policy", func(in *Input, _ *int64) { in.AllowZeroBalance = !in.AllowZeroBalance }},
		{"model", func(in *Input, _ *int64) { in.Model = "other-model" }},
		{"pricing", func(in *Input, _ *int64) { in.PricingVersion++ }},
		{"time", func(in *Input, _ *int64) { in.OccurredAt = in.OccurredAt.Add(time.Second) }},
		{"budget", func(in *Input, _ *int64) { in.Budgets[0].Limit++ }},
		{"generation", func(in *Input, _ *int64) { in.Generations[0].Value = "next" }},
		{"rate", func(in *Input, _ *int64) { in.Rates[0].Cost++ }},
		{"reference", func(in *Input, _ *int64) { in.ReferenceRequestID = "other-original-operation" }},
		{"parent", func(in *Input, _ *int64) { in.ParentRequestID = "other-parent" }},
		{"session", func(in *Input, _ *int64) { in.SessionID = "other-session" }},
		{"agent", func(in *Input, _ *int64) { in.AgentID = "other-agent" }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			engine := New(nil, nil)
			engine.SpoolDir = t.TempDir()
			failures := 0
			engine.SpoolFailure = func(error) { failures++ }
			input := testInput("same-financial-id", 100)
			input.Budgets = []Budget{{ID: 1, Limit: 1000}}
			input.Generations = []Generation{{Key: "user:1", Value: "1"}}
			input.Rates = []RateLimit{{Key: "rpm:1", Decision: "rpm", Cost: 1, Limit: 10, WindowSeconds: 60}}
			if err := engine.PersistSettlementWithTelemetry(input, 70, "settle", "first-facts"); err != nil {
				t.Fatal(err)
			}
			_, original, _ := readOnlySpoolEvent(t, engine)
			actual := int64(70)
			change.mutate(&input, &actual)
			if err := engine.PersistSettlementWithTelemetry(input, actual, "settle", "changed-facts"); err == nil {
				t.Fatal("changed financial facts accepted under the same operation identity")
			}
			_, retained, _ := readOnlySpoolEvent(t, engine)
			if failures != 0 || !bytes.Equal(original, retained) {
				t.Fatalf("conflict failure count=%d or durable intent changed", failures)
			}
		})
	}
}

func TestSpoolRetryLegacyEncodingAndNegativeAmount(t *testing.T) {
	for _, actual := range []int64{70, -30} {
		t.Run(fmt.Sprint(actual), func(t *testing.T) {
			engine := New(nil, nil)
			engine.SpoolDir = t.TempDir()
			failures := 0
			engine.SpoolFailure = func(error) { failures++ }
			kind := "settle"
			if actual < 0 {
				kind = "refund"
			}
			input := testInput("legacy-money-encoding", 100)
			if err := engine.PersistSettlementWithTelemetry(input, actual, kind, "first-facts"); err != nil {
				t.Fatal(err)
			}
			path, raw, _ := readOnlySpoolEvent(t, engine)
			legacy := strings.Replace(string(raw), fmt.Sprintf(`"amount":"%d"`, actual), fmt.Sprintf(`"amount":%d`, actual), 1)
			for _, field := range []string{"budgets", "generations", "rates"} {
				legacy = strings.Replace(legacy, `"`+field+`":[]`, `"`+field+`":{}`, 1)
			}
			if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
				t.Fatal(err)
			}
			if err := engine.PersistSettlementWithTelemetry(input, actual, kind, "new-facts"); err != nil || failures != 0 {
				t.Fatalf("equivalent old money/empty-array encoding rejected: err=%v failures=%d", err, failures)
			}
			_, retained, event := readOnlySpoolEvent(t, engine)
			if string(retained) != legacy || event.Amount != actual || event.TelemetryEnvelope != "first-facts" {
				t.Fatal("retry rewrote old-format accepted financial evidence")
			}
			if err := engine.PersistSettlementWithTelemetry(input, actual+1, kind, "new-facts"); err == nil || failures != 0 {
				t.Fatalf("changed positive/negative amount was accepted: err=%v failures=%d", err, failures)
			}
		})
	}
}

func TestSpoolConcurrentChangedTelemetryKeepsOneFinancialIntent(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	var failures atomic.Int64
	engine.SpoolFailure = func(error) { failures.Add(1) }
	input := testInput("concurrent-facts", 100)
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			if err := engine.PersistSettlementWithTelemetry(input, 70, "settle", fmt.Sprintf(`{"created_at":%d}`, index)); err != nil {
				t.Errorf("concurrent same financial operation rejected: %v", err)
			}
		}(i)
	}
	close(start)
	workers.Wait()
	_, _, event := readOnlySpoolEvent(t, engine)
	if event.Amount != 70 || event.OperationID != input.RequestID || event.TelemetryEnvelope == "" || failures.Load() != 0 {
		t.Fatalf("concurrent durable intent=%+v failures=%d", event, failures.Load())
	}
}

func TestSpoolRetryCorruptedExistingIntentIsRejected(t *testing.T) {
	engine := New(nil, nil)
	engine.SpoolDir = t.TempDir()
	failures := 0
	engine.SpoolFailure = func(error) { failures++ }
	input := testInput("corrupted-retry", 100)
	if err := engine.PersistSettlementWithTelemetry(input, 70, "settle", "first-facts"); err != nil {
		t.Fatal(err)
	}
	path, _, _ := readOnlySpoolEvent(t, engine)
	broken := []byte(`{"broken"`)
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.PersistSettlementWithTelemetry(input, 70, "settle", "new-facts"); err == nil || failures != 0 {
		t.Fatalf("corrupt existing intent accepted: err=%v failures=%d", err, failures)
	}
	retained, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(retained, broken) {
		t.Fatal("retry overwrote corrupt evidence")
	}
}
