package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/requestpath"
	"github.com/lazu-ai/lazu/internal/types"
)

type reviewReserveGateway struct {
	requestpath.Gateway
	snapshot                  *model.IdentitySnapshot
	reserveCalls, reloadCalls int
	alwaysStale               bool
}

func (g *reviewReserveGateway) Reserve(context.Context, ledger.Input) error {
	g.reserveCalls++
	if g.alwaysStale || g.reserveCalls == 1 {
		return ledger.ErrStale
	}
	return nil
}
func (g *reviewReserveGateway) ReserveAdmitted(ctx context.Context, input ledger.Input) (int64, error) {
	return input.Amount, g.Reserve(ctx, input)
}
func (g *reviewReserveGateway) Identity(context.Context, string) (*model.IdentitySnapshot, error) {
	g.reloadCalls++
	return g.snapshot, nil
}
func reviewIdentity() *model.IdentitySnapshot {
	return &model.IdentitySnapshot{
		Token: &model.Token{Id: 2, UserId: 1, ProjectID: 3, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
		Actor: &model.UserBase{Id: 1, Status: common.UserStatusEnabled}, Payer: &model.UserBase{Id: 1, Status: common.UserStatusEnabled},
		Principal: model.ProjectPrincipal{ProjectID: 3, PayerUserID: 1, ActorUserID: 1}, MemberStatus: model.ProjectMemberActive,
		CachedUntil: time.Now().Add(time.Minute).UnixMilli(),
	}
}

type pausedReserveGateway struct{ requestpath.Gateway }

func (pausedReserveGateway) Reserve(context.Context, ledger.Input) error {
	return ledger.ErrAdmissionPaused
}
func (pausedReserveGateway) ReserveAdmitted(context.Context, ledger.Input) (int64, error) {
	return 0, ledger.ErrAdmissionPaused
}

// Production 2026-10-08: a deploy quiesce held lazu:ledger:maintenance for
// ~2 minutes. Customers received Retry-After: 1, so openai-node spent all three
// attempts in ~5s inside the pause. The pause is retryable, but on its own scale.
func TestAdmissionPauseIsRetryable503WithPauseScaleRetryAfter(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Set(model.IdentitySnapshotContextKey, reviewIdentity())
	result := reserveRedisLedgerWithGateway(c, 1, &relaycommon.RelayInfo{UserId: 1, TokenId: 2, TokenKey: "fixture-only"}, pausedReserveGateway{})
	if result == nil || result.GetErrorCode() != types.ErrorCodeGatewayTemporarilyUnavailable || result.WireStatus() != 503 {
		t.Fatalf("admission pause was not a retryable 503: %v", result)
	}
	if got := recorder.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("admission pause Retry-After=%q want 30", got)
	}
}

func TestPR380ReserveReloadsStaleIdentityOnce(t *testing.T) {
	for _, stale := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
		snapshot := reviewIdentity()
		c.Set(model.IdentitySnapshotContextKey, snapshot)
		gateway := &reviewReserveGateway{snapshot: snapshot, alwaysStale: stale}
		result := reserveRedisLedgerWithGateway(c, 1, &relaycommon.RelayInfo{UserId: 1, TokenId: 2, TokenKey: "fixture-only"}, gateway)
		if gateway.reserveCalls != 2 || gateway.reloadCalls != 1 || (result != nil) != stale {
			t.Fatalf("stale=%t reserve=%d reload=%d result=%v", stale, gateway.reserveCalls, gateway.reloadCalls, result)
		}
	}
}
func TestPR380BudgetErrorIsProjectScopedAfterSnapshotRoundTrip(t *testing.T) {
	snapshot := reviewIdentity()
	snapshot.Budgets = []model.SpendBudget{{ID: 8, ProjectID: 3, Scope: model.SpendBudgetScopeOrg}}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.IdentitySnapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	result := redisAdmissionError(&ledger.Rejection{Code: "budget", BudgetID: 8}, &restored)
	if result.GetErrorCode() != types.ErrorCodeProjectBudgetExceeded {
		t.Fatalf("wrong budget scope: %s", result.GetErrorCode())
	}
}

func TestPR380StaleReloadCannotReuseChangedDistributorPermissions(t *testing.T) {
	for _, key := range []string{"gen:policy", "gen:ephemeral:7", "gen:member:1", "gen:project:3"} {
		prior, fresh := reviewIdentity(), reviewIdentity()
		prior.Generations = []model.IdentityGeneration{{Key: key, Value: "1"}}
		fresh.Generations = []model.IdentityGeneration{{Key: key, Value: "2"}}
		if sameLedgerAdmissionPolicy(prior, fresh, false) {
			t.Fatalf("changed %s authorization reused old distributor policy", key)
		}
	}
}

func TestPR380StaleSessionReloadPreservesUserRevocationFence(t *testing.T) {
	prior, fresh := reviewIdentity(), reviewIdentity()
	prior.Generations = []model.IdentityGeneration{{Key: "gen:user:1", Value: "1"}}
	fresh.Generations = []model.IdentityGeneration{{Key: "gen:user:1", Value: "2"}}
	if sameLedgerAdmissionPolicy(prior, fresh, true) {
		t.Fatal("trusted session continued after user generation changed")
	}
	if !sameLedgerAdmissionPolicy(prior, fresh, false) {
		t.Fatal("ordinary unchanged key authorization could not reload")
	}
}
