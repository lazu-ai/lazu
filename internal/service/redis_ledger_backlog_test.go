package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/ledger"
)

// This integration checks actual Redis admission/ACK and the production error
// mapper/HTTP envelope. The durable storage double makes drain deterministic;
// it does not stand in for the separate PostgreSQL downtime acceptance drill.
func TestRedisLedgerBacklogHardLimitCustomer503AndDrainReopensIntegration(t *testing.T) {
	redisURL := os.Getenv("TEST_LEDGER_BACKLOG_REDIS_URL")
	if redisURL == "" {
		t.Skip("isolated TEST_LEDGER_BACKLOG_REDIS_URL required")
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	clear := func() {
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, ledger.Prefix+"*", 500).Result()
			if err != nil {
				t.Error(err)
				return
			}
			if len(keys) > 0 {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
					return
				}
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	}
	clear()
	t.Cleanup(func() { clear(); _ = client.Close() })
	storage := &backlogTestStorage{balance: 1000, remaining: 1000, spent: map[string]int64{}, applied: map[string]ledger.Event{}}
	engine := ledger.New(client, storage)
	engine.Continuity = "backlog-fixture:1"
	// Exercise the live Options callback, not only the fixed default field.
	engine.HardLimitValue = func() int64 { return 100 }
	if err := client.Set(ctx, ledger.ContinuityKey, engine.Continuity, 0).Err(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	budget := ledger.Budget{ID: 1, Start: now.Add(-time.Hour).Unix(), End: now.Add(time.Hour).Unix(), Limit: 1000, Block: true}
	first := ledger.Input{RequestID: "fixture-backlog-first", PayerID: 1, ActorID: 1, ProjectID: 3, TokenID: 2, Amount: 100, OccurredAt: now, Budgets: []ledger.Budget{budget}}
	if err := engine.Reserve(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := engine.Settle(ctx, first, 100); err != nil {
		t.Fatal(err)
	}
	if exposure, err := client.Get(ctx, ledger.Prefix+"exposure").Int64(); err != nil || exposure != 100 {
		t.Fatalf("backlog did not reach exact configured limit: %d %v", exposure, err)
	}
	next := first
	next.RequestID, next.Amount = "fixture-backlog-next", 10
	rateKey := "fixture-backlog-rate"
	ratePattern := ledger.Prefix + "rate:" + rateKey + ":*"
	next.Rates = []ledger.RateLimit{{Key: rateKey, Decision: "token_rpm_exceeded", Limit: 10, Cost: 1, WindowSeconds: 60}}
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		err := engine.Reserve(c.Request.Context(), next)
		if err != nil {
			var rejected *ledger.Rejection
			if !errors.As(err, &rejected) || rejected.Code != "backlog" {
				t.Errorf("expected backlog rejection, got %v", err)
			}
			httperr.WriteOpenAI(c, redisAdmissionError(err, nil))
			return
		}
		c.Status(http.StatusNoContent)
	})
	call := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
		return response
	}
	blocked := call()
	var response struct {
		Error httperr.Body `json:"error"`
	}
	if err := json.Unmarshal(blocked.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if blocked.Code != http.StatusServiceUnavailable || response.Error.Code != "gateway_overloaded" {
		t.Fatalf("backlog was not customer HTTP 503: status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	assertHot := func(balance, reserved int64) {
		t.Helper()
		if actual, err := client.HGet(ctx, ledger.Prefix+"wallet:1", "avail").Int64(); err != nil || actual != balance {
			t.Fatalf("hot wallet=%d want=%d err=%v", actual, balance, err)
		}
		if actual, err := client.Get(ctx, ledger.Prefix+"token:2").Int64(); err != nil || actual != balance {
			t.Fatalf("hot key=%d want=%d err=%v", actual, balance, err)
		}
		if actual, err := client.HGet(ctx, budget.Key(), "reserved").Int64(); err != nil || actual != reserved {
			t.Fatalf("budget hold=%d want=%d err=%v", actual, reserved, err)
		}
		if actual, err := client.HGet(ctx, budget.Key(), "spent").Int64(); err != nil || actual != 100 {
			t.Fatalf("settled budget=%d want=100 err=%v", actual, err)
		}
	}
	assertHot(900, 0)
	if count, err := client.Exists(ctx, ledger.Prefix+"request:"+next.RequestID).Result(); err != nil || count != 0 {
		t.Fatalf("rejected request created a hold: %d %v", count, err)
	}
	if keys, err := client.Keys(ctx, ratePattern).Result(); err != nil || len(keys) != 0 {
		t.Fatalf("rejected request consumed a rate bucket: %v %v", keys, err)
	}
	if len(storage.applied) != 0 {
		t.Fatal("the paused writer unexpectedly applied a financial event")
	}
	if count, err := engine.Drain(ctx, "backlog-worker"); err != nil || count != 1 {
		t.Fatalf("worker did not drain preserved backlog: %d %v", count, err)
	}
	if storage.balance != 900 || storage.remaining != 900 || storage.spent[budget.Key()] != 100 || len(storage.applied) != 1 {
		t.Fatalf("durable financial facts drifted: %+v", storage)
	}
	if exposure, err := client.Get(ctx, ledger.Prefix+"exposure").Int64(); err != nil || exposure != 0 {
		t.Fatalf("ACK did not clear backlog exposure: %d %v", exposure, err)
	}
	if count, err := client.ZCard(ctx, ledger.Prefix+"uncommitted").Result(); err != nil || count != 0 {
		t.Fatalf("ACK left uncommitted facts: %d %v", count, err)
	}
	if reopened := call(); reopened.Code != http.StatusNoContent {
		t.Fatalf("drain required manual reopening: status=%d body=%s", reopened.Code, reopened.Body.String())
	}
	assertHot(890, 10)
	rateKeys, err := client.Keys(ctx, ratePattern).Result()
	if err != nil || len(rateKeys) != 1 {
		t.Fatalf("reopened admission buckets=%v err=%v", rateKeys, err)
	}
	if count, err := client.Get(ctx, rateKeys[0]).Int64(); err != nil || count != 1 {
		t.Fatalf("reopened admission rate count=%d want=1 err=%v", count, err)
	}
	if err := engine.Release(ctx, next); err != nil {
		t.Fatal(err)
	}
	assertHot(900, 0)
}

type backlogTestStorage struct {
	balance   int64
	remaining int64
	spent     map[string]int64
	applied   map[string]ledger.Event
}

func (s *backlogTestStorage) Snapshot(_ context.Context, input ledger.Input) (ledger.Snapshot, error) {
	budgets := make(map[string]int64, len(input.Budgets))
	for _, budget := range input.Budgets {
		budgets[budget.Key()] = s.spent[budget.Key()]
	}
	return ledger.Snapshot{Balance: s.balance, TokenRemaining: map[int]int64{2: s.remaining}, Budgets: budgets}, nil
}
func (s *backlogTestStorage) Settled(_ context.Context, id, kind string) (bool, error) {
	_, exists := s.applied[kind+":"+id]
	return exists, nil
}
func (s *backlogTestStorage) Apply(_ context.Context, events []ledger.Event) error {
	for _, event := range events {
		id := event.Kind + ":" + event.OperationID
		if _, exists := s.applied[id]; exists {
			continue
		}
		s.applied[id] = event
		s.balance -= event.Amount
		s.remaining -= event.Amount
		for _, budget := range event.Budgets {
			s.spent[budget.Key()] += event.Amount
		}
	}
	return nil
}
func (*backlogTestStorage) PendingCredits(context.Context, int) ([]ledger.Credit, error) {
	return nil, nil
}
func (*backlogTestStorage) CreditApplied(context.Context, uint) error { return nil }
