package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
)

type Decision string

const (
	DecisionAllowed     Decision = "allowed"
	DecisionTokenRPM    Decision = "token_rpm_exceeded"
	DecisionAccountRPM  Decision = "account_rpm_exceeded"
	DecisionSuccessRPM  Decision = "success_rpm_exceeded"
	DecisionTotalRPM    Decision = "total_rpm_exceeded"
	DecisionTokenTPM    Decision = "token_tpm_exceeded"
	DecisionAccountTPM  Decision = "account_tpm_exceeded"
	DecisionCheckFailed Decision = "check_failed"
)

type Result struct {
	Decision          Decision
	Limit             int
	RetryAfterSeconds int64
	WindowMinutes     int
}

func (r Result) Allowed() bool {
	return r.Decision == "" || r.Decision == DecisionAllowed
}

type RequestPreflightInput struct {
	Namespace              string
	UserID                 int
	TokenID                int
	TokenRateLimitsEnabled bool
	TokenRPMLimit          int
	AccountRPMLimit        int
	TotalRequestLimit      int
	SuccessRequestLimit    int
	DurationSeconds        int64
	WindowMinutes          int
}

type TokenPreflightInput struct {
	UserID                 int
	TokenID                int
	TokenRateLimitsEnabled bool
	TokenTPMLimit          int
	AccountTPMLimit        int
	Tokens                 int
	DurationSeconds        int64
	WindowMinutes          int
}

type Service struct {
	memory *memoryStore
}

func NewService() *Service {
	return &Service{memory: newMemoryStore()}
}

var (
	defaultService     *Service
	defaultServiceOnce sync.Once
)

func DefaultService() *Service {
	defaultServiceOnce.Do(func() {
		defaultService = NewService()
	})
	return defaultService
}

func ResetDefaultForTesting() {
	defaultService = NewService()
	defaultServiceOnce = sync.Once{}
}

func (s *Service) PreflightRequest(ctx context.Context, input RequestPreflightInput) (Result, error) {
	input = normalizeRequestInput(input)
	if input.TokenRPMLimit <= 0 && input.AccountRPMLimit <= 0 && input.TotalRequestLimit <= 0 && input.SuccessRequestLimit <= 0 {
		return Result{Decision: DecisionAllowed}, nil
	}
	if shouldUseRedis() {
		return s.redisPreflightRequest(ctx, input)
	}
	return s.memory.preflightRequest(input), nil
}

func (s *Service) RecordSuccess(ctx context.Context, input RequestPreflightInput) error {
	input = normalizeRequestInput(input)
	if input.SuccessRequestLimit <= 0 || input.UserID <= 0 {
		return nil
	}
	key := successRPMKey(input.UserID)
	if shouldUseRedis() {
		return redisRecordCount(ctx, common.RDB, key, input.SuccessRequestLimit, input.DurationSeconds)
	}
	s.memory.recordCount(key, input.SuccessRequestLimit, input.DurationSeconds)
	return nil
}

func (s *Service) CheckAndRecordTPM(ctx context.Context, input TokenPreflightInput) (Result, error) {
	input = normalizeTokenInput(input)
	if input.Tokens <= 0 || (input.TokenTPMLimit <= 0 && input.AccountTPMLimit <= 0) {
		return Result{Decision: DecisionAllowed}, nil
	}
	if shouldUseRedis() {
		return s.redisCheckAndRecordTPM(ctx, input)
	}
	return s.memory.checkAndRecordTPM(input), nil
}

func normalizeRequestInput(input RequestPreflightInput) RequestPreflightInput {
	if input.DurationSeconds <= 0 {
		input.DurationSeconds = 60
	}
	if input.WindowMinutes <= 0 {
		input.WindowMinutes = int(input.DurationSeconds / 60)
		if input.WindowMinutes <= 0 {
			input.WindowMinutes = 1
		}
	}
	if !input.TokenRateLimitsEnabled || input.TokenID <= 0 {
		input.TokenRPMLimit = 0
	}
	if input.UserID <= 0 {
		input.AccountRPMLimit = 0
		input.TotalRequestLimit = 0
		input.SuccessRequestLimit = 0
	}
	return input
}

func normalizeTokenInput(input TokenPreflightInput) TokenPreflightInput {
	if input.DurationSeconds <= 0 {
		input.DurationSeconds = 60
	}
	if input.WindowMinutes <= 0 {
		input.WindowMinutes = int(input.DurationSeconds / 60)
		if input.WindowMinutes <= 0 {
			input.WindowMinutes = 1
		}
	}
	if !input.TokenRateLimitsEnabled || input.TokenID <= 0 {
		input.TokenTPMLimit = 0
	}
	if input.UserID <= 0 {
		input.AccountTPMLimit = 0
	}
	return input
}

func shouldUseRedis() bool {
	return common.RedisEnabled && common.RDB != nil
}

func tokenRPMKey(tokenID int) string {
	if tokenID <= 0 {
		return ""
	}
	return fmt.Sprintf("rateLimit:RPM:token:%d", tokenID)
}

func accountRPMKey(userID int) string {
	if userID <= 0 {
		return ""
	}
	return fmt.Sprintf("rateLimit:RPM:account:user:%d", userID)
}

func successRPMKey(userID int) string {
	if userID <= 0 {
		return ""
	}
	return fmt.Sprintf("rateLimit:RPM:success:user:%d", userID)
}

func totalRPMKey(userID int) string {
	if userID <= 0 {
		return ""
	}
	return fmt.Sprintf("rateLimit:RPM:total:user:%d", userID)
}

func tokenTPMKey(tokenID int) string {
	if tokenID <= 0 {
		return ""
	}
	return fmt.Sprintf("rateLimit:TPM:token:%d", tokenID)
}

func accountTPMKey(userID int) string {
	if userID <= 0 {
		return ""
	}
	return fmt.Sprintf("rateLimit:TPM:account:user:%d", userID)
}

func result(decision Decision, limit int, input RequestPreflightInput) Result {
	return Result{
		Decision:          decision,
		Limit:             limit,
		RetryAfterSeconds: input.DurationSeconds,
		WindowMinutes:     input.WindowMinutes,
	}
}

func tokenResult(decision Decision, limit int, input TokenPreflightInput) Result {
	return Result{
		Decision:          decision,
		Limit:             limit,
		RetryAfterSeconds: input.DurationSeconds,
		WindowMinutes:     input.WindowMinutes,
	}
}

type countWindow []int64

type tokenWindowEntry struct {
	at     time.Time
	tokens int
}

type memoryStore struct {
	mu           sync.Mutex
	countWindows map[string]countWindow
	tokenWindows map[string][]tokenWindowEntry
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		countWindows: map[string]countWindow{},
		tokenWindows: map[string][]tokenWindowEntry{},
	}
}

func (m *memoryStore) preflightRequest(input RequestPreflightInput) Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	checks := []struct {
		key      string
		limit    int
		decision Decision
	}{
		{requestLimitKey(input.Namespace, tokenRPMKey(input.TokenID)), input.TokenRPMLimit, DecisionTokenRPM},
		{requestLimitKey(input.Namespace, accountRPMKey(input.UserID)), input.AccountRPMLimit, DecisionAccountRPM},
		{successRPMKey(input.UserID), input.SuccessRequestLimit, DecisionSuccessRPM},
		{totalRPMKey(input.UserID), input.TotalRequestLimit, DecisionTotalRPM},
	}
	for _, check := range checks {
		if !m.countAllowedLocked(check.key, check.limit, input.DurationSeconds) {
			return result(check.decision, check.limit, input)
		}
	}
	for _, check := range checks {
		if check.decision == DecisionSuccessRPM {
			continue
		}
		m.recordCountLocked(check.key, check.limit, input.DurationSeconds)
	}
	return Result{Decision: DecisionAllowed}
}

func (m *memoryStore) recordCount(key string, limit int, durationSeconds int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordCountLocked(key, limit, durationSeconds)
}

func (m *memoryStore) countAllowedLocked(key string, limit int, durationSeconds int64) bool {
	if key == "" || limit <= 0 {
		return true
	}
	fresh := freshCountWindow(m.countWindows[key], durationSeconds)
	m.countWindows[key] = fresh
	return len(fresh) < limit
}

func (m *memoryStore) recordCountLocked(key string, limit int, durationSeconds int64) {
	if key == "" || limit <= 0 {
		return
	}
	fresh := freshCountWindow(m.countWindows[key], durationSeconds)
	fresh = append(fresh, time.Now().Unix())
	if len(fresh) > limit {
		fresh = fresh[len(fresh)-limit:]
	}
	m.countWindows[key] = fresh
}

func freshCountWindow(window countWindow, durationSeconds int64) countWindow {
	if len(window) == 0 {
		return window
	}
	now := time.Now().Unix()
	fresh := window[:0]
	for _, at := range window {
		if now-at < durationSeconds {
			fresh = append(fresh, at)
		}
	}
	return fresh
}

func (m *memoryStore) checkAndRecordTPM(input TokenPreflightInput) Result {
	m.mu.Lock()
	defer m.mu.Unlock()

	tokenKey := tokenTPMKey(input.TokenID)
	accountKey := accountTPMKey(input.UserID)
	tokenFresh, tokenTotal, tokenActive := m.freshTokenWindowLocked(tokenKey, input.TokenTPMLimit, input.DurationSeconds)
	if tokenActive && tokenTotal+input.Tokens > input.TokenTPMLimit {
		m.tokenWindows[tokenKey] = tokenFresh
		return tokenResult(DecisionTokenTPM, input.TokenTPMLimit, input)
	}
	accountFresh, accountTotal, accountActive := m.freshTokenWindowLocked(accountKey, input.AccountTPMLimit, input.DurationSeconds)
	if accountActive && accountTotal+input.Tokens > input.AccountTPMLimit {
		m.tokenWindows[tokenKey] = tokenFresh
		m.tokenWindows[accountKey] = accountFresh
		return tokenResult(DecisionAccountTPM, input.AccountTPMLimit, input)
	}
	now := time.Now()
	if tokenActive {
		m.tokenWindows[tokenKey] = append(tokenFresh, tokenWindowEntry{at: now, tokens: input.Tokens})
	}
	if accountActive {
		m.tokenWindows[accountKey] = append(accountFresh, tokenWindowEntry{at: now, tokens: input.Tokens})
	}
	return Result{Decision: DecisionAllowed}
}

func (m *memoryStore) freshTokenWindowLocked(key string, limit int, durationSeconds int64) ([]tokenWindowEntry, int, bool) {
	if key == "" || limit <= 0 {
		return nil, 0, false
	}
	now := time.Now()
	window := m.tokenWindows[key]
	fresh := window[:0]
	total := 0
	for _, entry := range window {
		if int64(now.Sub(entry.at).Seconds()) >= durationSeconds {
			continue
		}
		fresh = append(fresh, entry)
		total += entry.tokens
	}
	return fresh, total, true
}

const redisTimestampLua = `
local function entry_time_to_ms(value)
  return tonumber(value)
end
`

var redisRequestPreflightScript = redis.NewScript(redisTimestampLua + `
local token_key = KEYS[1]
local account_key = KEYS[2]
local success_key = KEYS[3]
local total_key = KEYS[4]
local token_max = tonumber(ARGV[1])
local account_max = tonumber(ARGV[2])
local success_max = tonumber(ARGV[3])
local total_max = tonumber(ARGV[4])
local duration_ms = tonumber(ARGV[5]) * 1000
local ttl_seconds = tonumber(ARGV[6])

local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

local function load_window(key, max_count)
  local fresh = {}
  if key == '' or max_count <= 0 then
    return fresh, 0, 0
  end
  local entries = redis.call('LRANGE', key, 0, -1)
  for _, raw in ipairs(entries) do
    local at = entry_time_to_ms(raw)
    if at ~= nil and now_ms - at < duration_ms then
      table.insert(fresh, tostring(at))
    end
  end
  return fresh, #fresh, 1
end

local function allowed(key, max_count)
  local fresh, total, active = load_window(key, max_count)
  if active == 0 then
    return 1, fresh, active
  end
  if total >= max_count then
    redis.call('DEL', key)
    for _, entry in ipairs(fresh) do
      redis.call('RPUSH', key, entry)
    end
    if #fresh > 0 then
      redis.call('EXPIRE', key, ttl_seconds)
    end
    return 0, fresh, active
  end
  return 1, fresh, active
end

local function record(key, fresh, active, max_count)
  if active == 0 then
    return
  end
  redis.call('DEL', key)
  redis.call('RPUSH', key, tostring(now_ms))
  for _, entry in ipairs(fresh) do
    redis.call('RPUSH', key, entry)
  end
  redis.call('LTRIM', key, 0, max_count - 1)
  redis.call('EXPIRE', key, ttl_seconds)
end

local token_allowed, token_fresh, token_active = allowed(token_key, token_max)
if token_allowed == 0 then
  return 2
end
local account_allowed, account_fresh, account_active = allowed(account_key, account_max)
if account_allowed == 0 then
  return 3
end
local success_allowed = allowed(success_key, success_max)
if success_allowed == 0 then
  return 4
end
local total_allowed, total_fresh, total_active = allowed(total_key, total_max)
if total_allowed == 0 then
  return 5
end

record(token_key, token_fresh, token_active, token_max)
record(account_key, account_fresh, account_active, account_max)
record(total_key, total_fresh, total_active, total_max)
return 1
`)

var redisRecordCountScript = redis.NewScript(`
local key = KEYS[1]
local max_count = tonumber(ARGV[1])
local ttl_seconds = tonumber(ARGV[2])
if key == '' or max_count <= 0 then
  return 1
end
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
redis.call('LPUSH', key, now_ms)
redis.call('LTRIM', key, 0, max_count - 1)
redis.call('EXPIRE', key, ttl_seconds)
return 1
`)

func (s *Service) redisPreflightRequest(ctx context.Context, input RequestPreflightInput) (Result, error) {
	code, err := redisRequestPreflightScript.Run(
		ctx,
		common.RDB,
		[]string{
			requestLimitKey(input.Namespace, tokenRPMKey(input.TokenID)),
			requestLimitKey(input.Namespace, accountRPMKey(input.UserID)),
			successRPMKey(input.UserID),
			totalRPMKey(input.UserID),
		},
		input.TokenRPMLimit,
		input.AccountRPMLimit,
		input.SuccessRequestLimit,
		input.TotalRequestLimit,
		input.DurationSeconds,
		input.DurationSeconds,
	).Int()
	if err != nil {
		return Result{Decision: DecisionCheckFailed}, err
	}
	switch code {
	case 2:
		return result(DecisionTokenRPM, input.TokenRPMLimit, input), nil
	case 3:
		return result(DecisionAccountRPM, input.AccountRPMLimit, input), nil
	case 4:
		return result(DecisionSuccessRPM, input.SuccessRequestLimit, input), nil
	case 5:
		return result(DecisionTotalRPM, input.TotalRequestLimit, input), nil
	default:
		return Result{Decision: DecisionAllowed}, nil
	}
}

func redisRecordCount(ctx context.Context, rdb *redis.Client, key string, limit int, durationSeconds int64) error {
	if key == "" || limit <= 0 {
		return nil
	}
	return redisRecordCountScript.Run(ctx, rdb, []string{key}, limit, durationSeconds).Err()
}

var redisDualTPMWindowScript = redis.NewScript(redisTimestampLua + `
local token_key = KEYS[1]
local account_key = KEYS[2]
local tokens = tonumber(ARGV[1])
local token_max = tonumber(ARGV[2])
local account_max = tonumber(ARGV[3])
local duration_ms = tonumber(ARGV[4]) * 1000
local ttl_seconds = tonumber(ARGV[5])
if tokens <= 0 then
  return 1
end
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)

local function load_window(key, max_tokens)
  local fresh = {}
  if key == '' or max_tokens <= 0 then
    return fresh, 0, 0
  end
  local total = 0
  local entries = redis.call('LRANGE', key, 0, -1)
  for _, raw in ipairs(entries) do
    local sep = string.find(raw, '|', 1, true)
    if sep ~= nil then
      local at = entry_time_to_ms(string.sub(raw, 1, sep - 1))
      local amount = tonumber(string.sub(raw, sep + 1))
      if at ~= nil and amount ~= nil and now_ms - at < duration_ms then
        table.insert(fresh, tostring(at) .. '|' .. tostring(amount))
        total = total + amount
      end
    end
  end
  return fresh, total, 1
end

local function restore_window(key, fresh, active)
  if active == 0 then
    return
  end
  redis.call('DEL', key)
  for _, entry in ipairs(fresh) do
    redis.call('RPUSH', key, entry)
  end
  if #fresh > 0 then
    redis.call('EXPIRE', key, ttl_seconds)
  end
end

local function record_window(key, fresh, active)
  if active == 0 then
    return
  end
  redis.call('DEL', key)
  redis.call('RPUSH', key, tostring(now_ms) .. '|' .. tostring(tokens))
  for _, entry in ipairs(fresh) do
    redis.call('RPUSH', key, entry)
  end
  redis.call('EXPIRE', key, ttl_seconds)
end

local token_fresh, token_total, token_active = load_window(token_key, token_max)
if token_active == 1 and token_total + tokens > token_max then
  restore_window(token_key, token_fresh, token_active)
  return 2
end

local account_fresh, account_total, account_active = load_window(account_key, account_max)
if account_active == 1 and account_total + tokens > account_max then
  restore_window(token_key, token_fresh, token_active)
  restore_window(account_key, account_fresh, account_active)
  return 3
end

record_window(token_key, token_fresh, token_active)
record_window(account_key, account_fresh, account_active)
return 1
`)

func (s *Service) redisCheckAndRecordTPM(ctx context.Context, input TokenPreflightInput) (Result, error) {
	code, err := redisDualTPMWindowScript.Run(
		ctx,
		common.RDB,
		[]string{tokenTPMKey(input.TokenID), accountTPMKey(input.UserID)},
		input.Tokens,
		input.TokenTPMLimit,
		input.AccountTPMLimit,
		input.DurationSeconds,
		input.DurationSeconds,
	).Int()
	if err != nil {
		return Result{Decision: DecisionCheckFailed}, err
	}
	switch code {
	case 2:
		return tokenResult(DecisionTokenTPM, input.TokenTPMLimit, input), nil
	case 3:
		return tokenResult(DecisionAccountTPM, input.AccountTPMLimit, input), nil
	default:
		return Result{Decision: DecisionAllowed}, nil
	}
}
