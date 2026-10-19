package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
)

// withMemoryLimits runs the limiter on its in-process store with small
// budgets so a test can reach them.
func withMemoryLimits(t *testing.T, public, console int) {
	t.Helper()
	previousRedisEnabled := common.RedisEnabled
	previousPublic := publicAPIPolicy
	previousConsole := consoleAPIPolicy
	previousExpiration := common.RateLimitKeyExpirationDuration
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		publicAPIPolicy = previousPublic
		consoleAPIPolicy = previousConsole
		common.RateLimitKeyExpirationDuration = previousExpiration
		inMemoryRateLimiter.Reset()
	})
	common.RedisEnabled = false
	publicAPIPolicy.limit = public
	consoleAPIPolicy.limit = console
	common.RateLimitKeyExpirationDuration = time.Minute
	inMemoryRateLimiter.Reset()
	gin.SetMode(gin.TestMode)
}

func okHandler(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) }

func serve(router *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestAPIRateLimitBypassesHealthRoutes(t *testing.T) {
	withMemoryLimits(t, 1, 1)

	router := gin.New()
	router.Use(APIRateLimit())
	for _, path := range []string{"/api/status", "/api/uptime/status", "/health", "/api/limited"} {
		router.GET(path, okHandler)
	}

	for _, path := range []string{"/api/status", "/api/uptime/status", "/health"} {
		for i := 0; i < 3; i++ {
			if got := serve(router, httptest.NewRequest(http.MethodGet, path, nil)).Code; got != http.StatusOK {
				t.Fatalf("expected %s request %d to bypass rate limit, got %d", path, i+1, got)
			}
		}
	}
	if got := serve(router, httptest.NewRequest(http.MethodGet, "/api/limited", nil)).Code; got != http.StatusOK {
		t.Fatalf("expected first limited request to pass, got %d", got)
	}
	if got := serve(router, httptest.NewRequest(http.MethodGet, "/api/limited", nil)).Code; got != http.StatusTooManyRequests {
		t.Fatalf("expected second limited request to be rate limited, got %d", got)
	}
}

func TestAPIRateLimitRejectsSpoofedForwardHeaders(t *testing.T) {
	withMemoryLimits(t, 1, 1)

	router := gin.New()
	router.Use(APIRateLimit())
	router.GET("/api/limited", okHandler)

	first := httptest.NewRequest(http.MethodGet, "/api/limited", nil)
	first.RemoteAddr = "8.8.8.8:1111"
	first.Header.Set("CF-Connecting-IP", "1.1.1.1")
	if got := serve(router, first).Code; got != http.StatusOK {
		t.Fatalf("expected first request to pass, got %d", got)
	}
	second := httptest.NewRequest(http.MethodGet, "/api/limited", nil)
	second.RemoteAddr = "8.8.8.8:2222"
	second.Header.Set("CF-Connecting-IP", "1.0.0.1")
	if got := serve(router, second).Code; got != http.StatusTooManyRequests {
		t.Fatalf("expected spoofed second request to be rate limited, got %d", got)
	}
}

// A 429 says what happened and when to come back, in the console envelope.
func TestAPIRateLimitAnswersWithCodeAndRetryAfter(t *testing.T) {
	withMemoryLimits(t, 1, 1)

	router := gin.New()
	router.Use(APIRateLimit())
	router.GET("/api/limited", okHandler)

	serve(router, httptest.NewRequest(http.MethodGet, "/api/limited", nil))
	rejected := serve(router, httptest.NewRequest(http.MethodGet, "/api/limited", nil))
	if rejected.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rejected.Code)
	}
	if rejected.Header().Get("Retry-After") == "" {
		t.Fatal("expected a Retry-After header")
	}
	var body struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rejected.Body.Bytes(), &body); err != nil {
		t.Fatalf("expected a JSON body, got %q: %v", rejected.Body.String(), err)
	}
	if body.Success || body.Error.Code != "too_many_attempts" {
		t.Fatalf("expected too_many_attempts, got %+v", body)
	}
	if seconds, _ := body.Error.Details["retry_after_seconds"].(float64); seconds < 1 {
		t.Fatalf("expected retry_after_seconds in details, got %+v", body.Error.Details)
	}
}

// Signed-in callers spend their own account's budget, so two people behind
// one address do not limit each other, and one person does not get a fresh
// budget from another address.
func TestAPIRateLimitCountsSignedInCallersByAccount(t *testing.T) {
	withMemoryLimits(t, 1, 2)

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-secret"))))
	router.GET("/login/:id", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", common.ParseIntFromAny(c.Param("id")))
		_ = session.Save()
		c.Status(http.StatusNoContent)
	})
	api := router.Group("/api", APIRateLimit())
	api.GET("/limited", okHandler)

	cookieFor := func(id string) string {
		login := serve(router, httptest.NewRequest(http.MethodGet, "/login/"+id, nil))
		return strings.Split(login.Header().Get("Set-Cookie"), ";")[0]
	}
	alice, bob := cookieFor("1"), cookieFor("2")
	request := func(cookie string, ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/limited", nil)
		req.RemoteAddr = ip + ":1234"
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		return serve(router, req).Code
	}

	// Same address, two accounts: each has its own budget of 2.
	for i := 0; i < 2; i++ {
		if got := request(alice, "203.0.113.1"); got != http.StatusOK {
			t.Fatalf("alice request %d: got %d", i+1, got)
		}
		if got := request(bob, "203.0.113.1"); got != http.StatusOK {
			t.Fatalf("bob request %d: got %d", i+1, got)
		}
	}
	// Moving address does not refill alice's budget.
	if got := request(alice, "198.51.100.9"); got != http.StatusTooManyRequests {
		t.Fatalf("expected alice to stay limited from a new address, got %d", got)
	}
	// Anonymous callers from that shared address still have the public budget.
	if got := request("", "203.0.113.1"); got != http.StatusOK {
		t.Fatalf("expected anonymous caller to use the public budget, got %d", got)
	}
}

// Sensitive policies are separate buckets: exhausting sign-in does not block
// a checkout from the same address.
func TestSensitivePoliciesDoNotShareBuckets(t *testing.T) {
	withMemoryLimits(t, 100, 100)
	previousLogin := loginPolicy
	previousAction := accountActionPolicy
	t.Cleanup(func() {
		loginPolicy = previousLogin
		accountActionPolicy = previousAction
	})
	loginPolicy.limit = 1
	accountActionPolicy.limit = 1

	router := gin.New()
	router.POST("/login", LoginRateLimit(), okHandler)
	router.POST("/pay", func(c *gin.Context) {
		c.Set(string(constant.ContextKeyUserId), 7)
		c.Next()
	}, AccountActionRateLimit(), okHandler)

	login := func() int { return serve(router, httptest.NewRequest(http.MethodPost, "/login", nil)).Code }
	pay := func() int { return serve(router, httptest.NewRequest(http.MethodPost, "/pay", nil)).Code }

	if login() != http.StatusOK || login() != http.StatusTooManyRequests {
		t.Fatal("expected the login budget of 1 to be spent")
	}
	if got := pay(); got != http.StatusOK {
		t.Fatalf("expected checkout to have its own budget, got %d", got)
	}
}
