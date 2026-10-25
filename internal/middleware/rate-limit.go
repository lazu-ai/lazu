package middleware

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/types"
)

// Console and public API rate limits.
//
// Every limit is a named policy: a budget, a window and the identity it is
// spent by. Routes say which policy guards them, so one busy page can never
// spend the budget that guards sign-in, and a signed-in person is counted as
// themselves rather than as everyone behind their office NAT. Model calls on
// /v1 are governed separately by the RPM/TPM limits in model-rate-limit.go.

type rateLimitPolicy struct {
	// name keys the Redis bucket and labels the log line. Keep it short.
	name   string
	limit  int
	window time.Duration
}

var (
	// Anonymous /api traffic: public catalog, status, docs data. A floor
	// against scraping and runaway scripts, far above what a visitor clicks.
	publicAPIPolicy = rateLimitPolicy{name: "public", limit: 300, window: time.Minute}
	// Signed-in console traffic, counted per account. Fast page switching
	// with several tabs open stays well under it; a script in a loop does not.
	consoleAPIPolicy = rateLimitPolicy{name: "console", limit: 600, window: time.Minute}
	// Credential checks: password, e-mail code, reset token, OAuth/OIDC round
	// trips. Per IP; each account also has its own failure cap (login_guard.go).
	loginPolicy = rateLimitPolicy{name: "login", limit: 30, window: 20 * time.Minute}
	// New accounts carry a sign-up credit, so creating them is its own budget.
	registerPolicy = rateLimitPolicy{name: "register", limit: 20, window: 20 * time.Minute}
	// Requests that send mail. Per IP here; each recipient is also capped
	// (mail_guard.go) so one address cannot be flooded from many IPs.
	mailSendPolicy = rateLimitPolicy{name: "mail", limit: 10, window: 10 * time.Minute}
	// Endpoints that answer for a secret someone pastes: a Key, an invite or
	// unsubscribe token. Slow enough that guessing is pointless.
	lookupPolicy = rateLimitPolicy{name: "lookup", limit: 30, window: 20 * time.Minute}
	// Ephemeral credential issuance, before the Key is checked.
	ephemeralCredentialPolicy = rateLimitPolicy{name: "ephemeral", limit: 20, window: 20 * time.Minute}
	// Account actions with side effects outside Lazu: checkout sessions,
	// referral codes, e-mail binding, accepting invites.
	accountActionPolicy = rateLimitPolicy{name: "account_action", limit: 20, window: 20 * time.Minute}
	// The live username availability check runs while someone types.
	usernameCheckPolicy = rateLimitPolicy{name: "username_check", limit: 30, window: time.Minute}
	// Endpoints that return a stored secret in full. Backs every copy button
	// on the API Keys page, so a person working through a list spends it
	// quickly; a stolen session still only gets a slow trickle.
	secretRevealPolicy = rateLimitPolicy{name: "secret_reveal", limit: 60, window: 10 * time.Minute}
)

var inMemoryRateLimiter InMemoryRateLimiter

var apiRateLimitBypassPaths = map[string]struct{}{
	"/api/status":        {},
	"/api/uptime/status": {},
	"/health":            {},
}

// Sliding log: the bucket keeps the timestamps of the last `limit` requests.
// Returns 0 when the request is admitted, otherwise the milliseconds until the
// oldest one leaves the window.
var redisSlidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local ttl_seconds = math.ceil(window_ms / 1000)
if redis.call('LLEN', key) >= limit then
  local oldest = tonumber(redis.call('LINDEX', key, -1))
  if oldest ~= nil and now_ms - oldest < window_ms then
    return window_ms - (now_ms - oldest)
  end
end
redis.call('LPUSH', key, now_ms)
redis.call('LTRIM', key, 0, limit - 1)
redis.call('EXPIRE', key, ttl_seconds)
return 0
`)

// take spends one request of identity's budget under the policy. When it is
// refused, the second value is how many seconds to wait.
func (p rateLimitPolicy) take(identity string) (bool, int64) {
	key := p.name + ":" + identity
	windowSeconds := int64(p.window / time.Second)
	if common.RedisEnabled && common.RDB != nil {
		timer := common.StartHotPathStage(common.HotPathStageRateLimitPreflight)
		waitMs, err := redisSlidingWindowScript.Run(
			context.Background(), common.RDB, []string{"rateLimit:" + key},
			p.limit, p.window.Milliseconds(),
		).Int64()
		timer.Finish(err)
		if err == nil {
			if waitMs <= 0 {
				return true, 0
			}
			return false, max(1, (waitMs+999)/1000)
		}
		// A Redis hiccup must neither lock everyone out nor switch limits off:
		// fall back to this process's own window until Redis answers again.
		reportRateLimitBackendError(err)
	}
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return inMemoryRateLimiter.RequestWithWait(key, p.limit, windowSeconds)
}

// guard runs the policy for the identity and aborts with 429 when it is spent.
// An empty identity is not limited (nothing to count it by).
func (p rateLimitPolicy) guard(c *gin.Context, identity string) bool {
	if identity == "" {
		return true
	}
	allowed, retryAfter := p.take(identity)
	if allowed {
		return true
	}
	rejectRateLimited(c, p.name, identity, retryAfter)
	return false
}

// byIP limits by the client address. Use it before authentication.
func (p rateLimitPolicy) byIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p.guard(c, ipIdentity(c)) {
			c.Next()
		}
	}
}

// byAccount limits by the signed-in account. Mount it after UserAuth/RootAuth
// so a stolen session cannot buy a fresh budget by moving to another IP.
func (p rateLimitPolicy) byAccount() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := accountIdentity(c)
		if identity == "" {
			// Auth did not run or did not resolve an account; count the caller
			// by address rather than letting the request through uncounted.
			identity = ipIdentity(c)
		}
		if p.guard(c, identity) {
			c.Next()
		}
	}
}

func ipIdentity(c *gin.Context) string {
	ip := common.GetRealClientIP(c)
	if ip == "" {
		return ""
	}
	return "ip:" + ip
}

func accountIdentity(c *gin.Context) string {
	if id := common.GetContextKeyInt(c, constant.ContextKeyUserId); id > 0 {
		return "user:" + strconv.Itoa(id)
	}
	return ""
}

// sessionIdentity reads the account from the signed session cookie without
// touching the database. The cookie is HMAC-signed, so a client cannot claim
// someone else's id; a stale session only spends its own account's budget.
func sessionIdentity(c *gin.Context) string {
	if _, ok := c.Get(sessions.DefaultKey); !ok {
		return ""
	}
	id := common.ParseIntFromAny(sessions.Default(c).Get("id"))
	if id <= 0 {
		return ""
	}
	return "user:" + strconv.Itoa(id)
}

func rejectRateLimited(c *gin.Context, policy string, identity string, retryAfter int64) {
	if retryAfter < 1 {
		retryAfter = 1
	}
	logRateLimited(c, policy, identity)
	c.Header("Retry-After", strconv.FormatInt(retryAfter, 10))
	httperr.Abort(c, types.Err(types.ErrorCodeTooManyAttempts, types.D{
		"retry_after_seconds": retryAfter,
	}))
}

// One line per policy and identity per minute: enough to see who is being
// limited and where, without a flood turning into a disk-filling log storm.
var rateLimitLogSeen sync.Map

func logRateLimited(c *gin.Context, policy string, identity string) {
	now := time.Now()
	logKey := policy + "|" + identity
	if last, ok := rateLimitLogSeen.Load(logKey); ok && now.Sub(last.(time.Time)) < time.Minute {
		return
	}
	rateLimitLogSeen.Store(logKey, now)
	if now.Unix()%64 == 0 {
		rateLimitLogSeen.Range(func(key, value any) bool {
			if now.Sub(value.(time.Time)) >= time.Minute {
				rateLimitLogSeen.Delete(key)
			}
			return true
		})
	}
	common.SysWarn(fmt.Sprintf(
		"rate limited | policy=%s identity=%s %s %s request_id=%s",
		policy, identity, c.Request.Method, c.Request.URL.Path, c.GetString(common.RequestIdKey),
	))
}

var rateLimitBackendErrorAt sync.Map

func reportRateLimitBackendError(err error) {
	now := time.Now()
	if last, ok := rateLimitBackendErrorAt.Load("redis"); ok && now.Sub(last.(time.Time)) < time.Minute {
		return
	}
	rateLimitBackendErrorAt.Store("redis", now)
	common.SysError("rate limit: redis unavailable, using in-process limits: " + err.Error())
}

// APIRateLimit is the floor under every console and public API route: the
// signed-in account's console budget, or the address's public budget for
// anonymous callers. Bearer-token callers on /api without a session (access
// tokens, model Keys reading usage) are counted by address.
func APIRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if shouldBypassAPIRateLimit(c) {
			c.Next()
			return
		}
		if identity := sessionIdentity(c); identity != "" {
			if consoleAPIPolicy.guard(c, identity) {
				c.Next()
			}
			return
		}
		if publicAPIPolicy.guard(c, ipIdentity(c)) {
			c.Next()
		}
	}
}

func shouldBypassAPIRateLimit(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	path := c.FullPath()
	if path == "" {
		path = c.Request.URL.Path
	}
	_, ok := apiRateLimitBypassPaths[path]
	return ok
}

func LoginRateLimit() gin.HandlerFunc               { return loginPolicy.byIP() }
func RegisterRateLimit() gin.HandlerFunc            { return registerPolicy.byIP() }
func MailSendRateLimit() gin.HandlerFunc            { return mailSendPolicy.byIP() }
func LookupRateLimit() gin.HandlerFunc              { return lookupPolicy.byIP() }
func EphemeralCredentialRateLimit() gin.HandlerFunc { return ephemeralCredentialPolicy.byIP() }
func AccountActionRateLimit() gin.HandlerFunc       { return accountActionPolicy.byAccount() }
func UsernameCheckRateLimit() gin.HandlerFunc       { return usernameCheckPolicy.byAccount() }
func SecretRevealRateLimit() gin.HandlerFunc        { return secretRevealPolicy.byAccount() }
