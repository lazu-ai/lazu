package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/internal/ratelimit"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestPrepareLedgerRateLimitsUsesPayerAndDoesNotDuplicateRPM(t *testing.T) {
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousTotal, previousSuccess := setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		setting.ModelRequestRateLimitCount = previousTotal
		setting.ModelRequestRateLimitSuccessCount = previousSuccess
	})
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 2
	setting.ModelRequestRateLimitCount, setting.ModelRequestRateLimitSuccessCount = 30, 20
	c := rateLimitTestContext(101, 11)
	common.SetContextKey(c, constant.ContextKeyTokenRPMLimit, 10)
	common.SetContextKey(c, constant.ContextKeyProjectID, 3)
	common.SetContextKey(c, constant.ContextKeyPayerUserID, 202)
	prepareLedgerRateLimits(c, 0)
	prepareLedgerRateLimits(c, 60)
	value, _ := c.Get(ledger.RateLimitsContextKey)
	rates, ok := value.([]ledger.RateLimit)
	if !ok || len(rates) != 6 {
		t.Fatalf("prepared buckets lost or duplicated: %+v", value)
	}
	seen := map[string]bool{}
	for _, rate := range rates {
		if seen[rate.Key] || rate.WindowSeconds != 120 {
			t.Fatalf("duplicate/window mismatch: %+v", rate)
		}
		seen[rate.Key] = true
		if strings.Contains(rate.Key, "account_") || strings.Contains(rate.Key, "success_") || strings.Contains(rate.Key, "total_") {
			if !strings.HasSuffix(rate.Key, ":202") {
				t.Fatalf("account limit charged actor instead of payer: %+v", rate)
			}
		}
		if strings.Contains(rate.Key, "tpm") && rate.Cost != 60 {
			t.Fatalf("TPM estimate not in atomic admission metadata: %+v", rate)
		}
		if strings.Contains(rate.Key, "rpm") && rate.Cost != 1 {
			t.Fatalf("RPM counted twice after TPM preparation: %+v", rate)
		}
	}
}

func TestDecisionChildDedicatedRateSurvivesDisabledOrdinaryLimiter(t *testing.T) {
	previous := setting.ModelRequestRateLimitEnabled
	t.Cleanup(func() { setting.ModelRequestRateLimitEnabled = previous })
	setting.ModelRequestRateLimitEnabled = false
	c := rateLimitTestContext(101, 11)
	common.SetContextKey(c, constant.ContextKeyProjectID, 3)
	common.SetContextKey(c, constant.ContextKeyPayerUserID, 202)
	c.Request = c.Request.WithContext(common.WithInternalRelayCallState(c.Request.Context(), &common.InternalRelayCallState{Reservation: &ledger.Input{RequestID: "decision-child"}}))
	prepareLedgerRateLimits(c, 600)
	value, _ := c.Get(ledger.RateLimitsContextKey)
	rates, ok := value.([]ledger.RateLimit)
	if !ok || len(rates) != 2 {
		t.Fatalf("dedicated generation guard missing: %+v", value)
	}
	for _, rate := range rates {
		if !strings.HasPrefix(rate.Key, "decision_qgen:") || rate.Cost != 1 || rate.Limit != 2 || rate.WindowSeconds != 60 {
			t.Fatalf("incorrect generation policy: %+v", rate)
		}
	}
	if rates[1].Key != "decision_qgen:account:202" {
		t.Fatal("generation guard counted the actor rather than payer")
	}
}

func TestCheckRequestTokenRateLimitAppliesPerTokenTPM(t *testing.T) {
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousRedisEnabled := common.RedisEnabled
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		common.RedisEnabled = previousRedisEnabled
		ratelimit.ResetDefaultForTesting()
	})

	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	common.RedisEnabled = false
	ratelimit.ResetDefaultForTesting()

	c := rateLimitTestContext(101, 11)
	code, err := CheckRequestTokenRateLimit(c, 60)
	if err != nil {
		t.Fatalf("expected first token batch to pass, got code=%s err=%v", code, err)
	}

	code, err = CheckRequestTokenRateLimit(c, 50)
	if err == nil {
		t.Fatal("expected second token batch to exceed per-token TPM")
	}
	if code != types.ErrorCodeTokenRateLimitExceeded {
		t.Fatalf("expected token rate limit code, got %s", code)
	}

	otherToken := rateLimitTestContext(101, 12)
	code, err = CheckRequestTokenRateLimit(otherToken, 50)
	if err != nil {
		t.Fatalf("expected a different token under same user to pass, got code=%s err=%v", code, err)
	}
}

func TestCheckRequestTokenRateLimitDoesNotRecordTokenTPMWhenAccountRejects(t *testing.T) {
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousRedisEnabled := common.RedisEnabled
	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		common.RedisEnabled = previousRedisEnabled
		ratelimit.ResetDefaultForTesting()
	})

	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	common.RedisEnabled = false
	ratelimit.ResetDefaultForTesting()

	unverified := rateLimitTestContextWithTokenTPM(201, 21, 10_000)
	common.SetContextKey(unverified, constant.ContextKeyUserHasToppedUp, false)
	code, err := CheckRequestTokenRateLimit(unverified, 6_000)
	if err == nil {
		t.Fatal("expected unverified tier TPM to reject the request")
	}
	if code != types.ErrorCodeRequestRateLimitExceeded {
		t.Fatalf("expected tier rate limit code, got %s", code)
	}

	tier3 := rateLimitTestContextWithTokenTPM(201, 21, 10_000)
	code, err = CheckRequestTokenRateLimit(tier3, 9_000)
	if err != nil {
		t.Fatalf("expected token TPM to remain available after tier rejection, got code=%s err=%v", code, err)
	}
}

func TestModelRequestRateLimitAppliesPerTokenRPM(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousTotalCount := setting.ModelRequestRateLimitCount
	previousSuccessCount := setting.ModelRequestRateLimitSuccessCount
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		setting.ModelRequestRateLimitCount = previousTotalCount
		setting.ModelRequestRateLimitSuccessCount = previousSuccessCount
		inMemoryRateLimiter.Reset()
		ratelimit.ResetDefaultForTesting()
	})

	common.RedisEnabled = false
	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = 0
	setting.ModelRequestRateLimitSuccessCount = 1000
	inMemoryRateLimiter.Reset()
	ratelimit.ResetDefaultForTesting()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		tokenID := 11
		if c.Query("token_id") == "12" {
			tokenID = 12
		}
		common.SetContextKey(c, constant.ContextKeyUserId, 101)
		common.SetContextKey(c, constant.ContextKeyUserHasToppedUp, true)
		common.SetContextKey(c, constant.ContextKeyTokenId, tokenID)
		common.SetContextKey(c, constant.ContextKeyTokenRateLimitsEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenRPMLimit, 1)
		c.Next()
	})
	router.Use(ModelRequestRateLimit())
	router.GET("/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("expected first token request to pass, got %d body=%s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second token request to hit per-token RPM, got %d body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), string(types.ErrorCodeTokenRateLimitExceeded)) {
		t.Fatalf("expected token rate limit error body, got %s", second.Body.String())
	}

	otherToken := httptest.NewRecorder()
	router.ServeHTTP(otherToken, httptest.NewRequest(http.MethodGet, "/v1/chat/completions?token_id=12", nil))
	if otherToken.Code != http.StatusOK {
		t.Fatalf("expected different token under same user to pass, got %d body=%s", otherToken.Code, otherToken.Body.String())
	}
}

func rateLimitTestContext(userID int, tokenID int) *gin.Context {
	return rateLimitTestContextWithTokenTPM(userID, tokenID, 100)
}

func rateLimitTestContextWithTokenTPM(userID int, tokenID int, tpmLimit int) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyUserId, userID)
	common.SetContextKey(c, constant.ContextKeyUserHasToppedUp, true)
	common.SetContextKey(c, constant.ContextKeyTokenId, tokenID)
	common.SetContextKey(c, constant.ContextKeyTokenRateLimitsEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenTPMLimit, tpmLimit)
	return c
}

func TestAccountRateLimitFromContextUsesUnpaidCeilingBeforeTopup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 123)

	rpm, tpm := accountRateLimitFromContext(c)
	if rpm != setting.RateLimitUnpaidRPM || tpm != setting.RateLimitUnpaidTPM {
		t.Fatalf("expected user without top-up to use the unpaid ceiling, got %d/%d", rpm, tpm)
	}

	common.SetContextKey(c, constant.ContextKeyUserHasToppedUp, true)
	rpm, tpm = accountRateLimitFromContext(c)
	if rpm != setting.RateLimitDefaultRPM || tpm != setting.RateLimitDefaultTPM {
		t.Fatalf("expected topped-up user to use the default ceiling, got %d/%d", rpm, tpm)
	}

	common.SetContextKey(c, constant.ContextKeyUserRPMLimit, 3)
	common.SetContextKey(c, constant.ContextKeyUserTPMLimit, 3000)
	rpm, tpm = accountRateLimitFromContext(c)
	if rpm != 3 || tpm != 3000 {
		t.Fatalf("expected per-account overrides to win, got %d/%d", rpm, tpm)
	}
}
