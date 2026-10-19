package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestRedisAdmissionErrorPublicStatusPreservesFinancialAndRateDecisions(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		code   types.ErrorCode
		status int
	}{
		{"unavailable", ledger.ErrUnavailable, types.ErrorCodeGatewayTemporarilyUnavailable, 503},
		{"rebuilding", ledger.ErrRebuilding, types.ErrorCodeGatewayTemporarilyUnavailable, 503},
		{"stale", ledger.ErrStale, types.ErrorCodeGatewayTemporarilyUnavailable, 503},
		{"timeout", context.DeadlineExceeded, types.ErrorCodeGatewayTemporarilyUnavailable, 503},
		{"balance", &ledger.Rejection{Code: "balance"}, types.ErrorCodeInsufficientQuota, 429},
		{"key", &ledger.Rejection{Code: "key"}, types.ErrorCodeKeyBudgetExceeded, 429},
		{"token-rpm", &ledger.Rejection{Code: "rate", RateDecision: "token_rpm_exceeded", RateLimit: 2}, types.ErrorCodeTokenRateLimitExceeded, 429},
		{"account-tpm", &ledger.Rejection{Code: "rate", RateDecision: "account_tpm_exceeded", RateLimit: 100}, types.ErrorCodeRequestRateLimitExceeded, 429},
		{"total-rpm", &ledger.Rejection{Code: "rate", RateDecision: "total_rpm_exceeded", RateLimit: 10}, types.ErrorCodeTotalRequestRateLimitExceeded, 429},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := redisAdmissionError(test.err, nil)
			if result.GetErrorCode() != test.code || result.WireStatus() != test.status {
				t.Fatalf("wrong public admission decision: %+v %d", result.Wire(), result.WireStatus())
			}
		})
	}
}

func TestRedisAdmissionMissingIdentityCacheFailsClosedWithRetryAfter(t *testing.T) {
	previous := common.RDB
	common.RDB = nil
	t.Cleanup(func() { common.RDB = previous })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	err := reserveRedisLedger(c, 0, &relaycommon.RelayInfo{TokenKey: "sk-unavailable"})
	if err == nil || err.GetErrorCode() != types.ErrorCodeGatewayTemporarilyUnavailable || err.WireStatus() != 503 || recorder.Header().Get("Retry-After") != "1" {
		t.Fatalf("identity outage was not closed with retry guidance: %v headers=%v", err, recorder.Header())
	}
}

func TestLedgerTokenPolicyComesFromVerifiedSnapshot(t *testing.T) {
	snapshot := &model.IdentitySnapshot{Token: &model.Token{Id: 5, UnlimitedQuota: false}}
	info := &relaycommon.RelayInfo{TokenId: 6, TokenUnlimited: true}
	if err := bindLedgerSnapshotToken(snapshot, info); !errors.Is(err, model.ErrInvalidAPIKey) {
		t.Fatalf("different token identity accepted: %v", err)
	}
	info.TokenId = 5
	if err := bindLedgerSnapshotToken(snapshot, info); err != nil {
		t.Fatal(err)
	}
	if info.TokenUnlimited {
		t.Fatal("stale unlimited flag overrode verified snapshot policy")
	}
	info.IsPlayground = true
	if err := bindLedgerSnapshotToken(snapshot, info); !errors.Is(err, model.ErrInvalidAPIKey) {
		t.Fatalf("unverified playground flag accepted: %v", err)
	}
	info.IsPlayground = false
	snapshot.Token.UnlimitedQuota = true
	if err := bindLedgerSnapshotToken(snapshot, info); err != nil || !info.TokenUnlimited {
		t.Fatalf("verified unlimited policy lost: %v", err)
	}
}
