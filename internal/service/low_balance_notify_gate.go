package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/email_setting"
)

type lowBalanceNotifyState struct {
	EpisodeStartUnix int64
	LastStep         int
}

var (
	lowBalanceStoreMu sync.Mutex

	// lowBalanceNotifyStore is a best-effort fallback when Redis is unavailable.
	// It is process-local and state will be lost after process restart.
	lowBalanceNotifyStore = make(map[string]lowBalanceNotifyState)
	lowBalanceNow         = time.Now

	lowBalanceCleanupMu     sync.Mutex
	lowBalanceCleanupCancel context.CancelFunc

	lowBalanceSendFailMu sync.Mutex
	lowBalanceSendFails  = make(map[int]lowBalanceSendFailRecord)
)

type lowBalanceSendFailRecord struct {
	Count    int
	LastFail time.Time
}

var defaultLowBalanceRemindDays = []int{1, 3, 7, 15, 30}

const lowBalanceNotifyStateVersion = "v1"

const (
	lowBalanceNotifyRedisMinTTLDays   = 90
	lowBalanceNotifyRedisBufferTTLDay = 60
	lowBalanceMemoryCleanupInterval   = time.Hour
	lowBalanceMemoryRetentionDays     = 180

	// lowBalanceSendRetryCooldown is the base interval between email send retries.
	// The actual cooldown grows exponentially: base * 2^(min(failCount-1, maxExp)).
	lowBalanceSendRetryCooldown    = time.Hour
	lowBalanceSendRetryMaxExponent = 4 // caps cooldown at 2^4 * base = 16 hours
)

// lowBalanceNotifyScheduleScript applies the low-balance backoff schedule atomically.
// State format:
// - Current:   "v1:<episode_start_unix>:<last_step_index>"
// - Legacy v0: "<episode_start_unix>:<last_step_index>"
// - Legacy v-1:"<episode_start_unix>"
// Transition summary:
// 1) No state         -> initialize to step 0, allow send.
// 2) Has state        -> compute elapsed day and next step by remind_days.
// 3) Step reached     -> update step, allow send.
// 4) Step not reached -> keep state (refresh TTL), suppress send.
var lowBalanceNotifyScheduleScript = redis.NewScript(`
local key = KEYS[1]
local now_unix = tonumber(ARGV[1]) or 0
local ttl_seconds = tonumber(ARGV[2]) or 0
local remind_count = #ARGV - 2
if remind_count <= 0 then
	return 0
end

local function persist(value)
	if ttl_seconds > 0 then
		redis.call("SET", key, value, "EX", ttl_seconds)
	else
		redis.call("SET", key, value)
	end
end

local raw = redis.call("GET", key)
if not raw then
	local state = "` + lowBalanceNotifyStateVersion + `:" .. tostring(now_unix) .. ":0"
	persist(state)
	return 1
end

local start_unix = nil
local last_step = nil

local _, v_start, v_step = string.match(raw, "^v(%d+):(%-?%d+):(%-?%d+)$")
if v_start then
	start_unix = tonumber(v_start)
	last_step = tonumber(v_step)
else
	local old_start, old_step = string.match(raw, "^(%-?%d+):(%-?%d+)$")
	if old_start then
		start_unix = tonumber(old_start)
		last_step = tonumber(old_step)
	else
		local legacy_start = tonumber(raw)
		if legacy_start then
			start_unix = legacy_start
			last_step = 0
		else
			start_unix = now_unix
			last_step = -1
		end
	end
end

if not start_unix or start_unix <= 0 then
	start_unix = now_unix
end
if not last_step then
	last_step = -1
end

local max_step = remind_count - 1
if last_step >= max_step then
	persist("` + lowBalanceNotifyStateVersion + `:" .. tostring(start_unix) .. ":" .. tostring(last_step))
	return 0
end

local elapsed_days = math.floor((now_unix - start_unix) / 86400) + 1
if elapsed_days < 1 then
	elapsed_days = 1
end

local next_step = -1
for i = remind_count, 1, -1 do
	local day = tonumber(ARGV[i + 2]) or 0
	local step = i - 1
	if step > last_step and elapsed_days >= day then
		next_step = step
		break
	end
end

if next_step < 0 then
	persist("` + lowBalanceNotifyStateVersion + `:" .. tostring(start_unix) .. ":" .. tostring(last_step))
	return 0
end

persist("` + lowBalanceNotifyStateVersion + `:" .. tostring(start_unix) .. ":" .. tostring(next_step))
return 1
`)

func lowBalanceRemindDays(cfg *email_setting.EmailSetting) []int {
	if cfg == nil {
		return append([]int(nil), defaultLowBalanceRemindDays...)
	}
	return normalizeLowBalanceRemindDays(cfg.LowBalanceRemindDays)
}

func normalizeLowBalanceRemindDays(days []int) []int {
	normalized := common.NormalizePositiveIntSlice(days)
	if len(normalized) == 0 {
		return append([]int(nil), defaultLowBalanceRemindDays...)
	}
	if normalized[0] != 1 {
		normalized = append([]int{1}, normalized...)
	}
	return normalized
}

func shouldSendLowBalanceEmail(userID int, remindDays []int) bool {
	if userID <= 0 {
		return true
	}
	if isLowBalanceSendInCooldown(userID) {
		return false
	}
	normalizedDays := normalizeLowBalanceRemindDays(remindDays)
	if common.RedisEnabled && common.RDB != nil {
		return shouldSendLowBalanceEmailRedis(userID, normalizedDays)
	}
	ensureLowBalanceMemoryCleanup()
	return shouldSendLowBalanceEmailMemory(userID, normalizedDays)
}

func shouldSendLowBalanceEmailRedis(userID int, remindDays []int) bool {
	key := lowBalanceNotifyKey(userID)
	now := lowBalanceNow()
	stateTTL := lowBalanceNotifyStateTTL(remindDays)

	args := make([]interface{}, 0, len(remindDays)+2)
	args = append(args, now.Unix(), int(stateTTL.Seconds()))
	for _, day := range remindDays {
		args = append(args, day)
	}

	result, err := lowBalanceNotifyScheduleScript.Run(context.Background(), common.RDB, []string{key}, args...).Int()
	if err != nil {
		common.SysError(fmt.Sprintf("failed to run atomic low-balance notify schedule for user %d: %v", userID, err))
		return shouldSendLowBalanceEmailMemory(userID, remindDays)
	}
	return result == 1
}

func shouldSendLowBalanceEmailMemory(userID int, remindDays []int) bool {
	lowBalanceStoreMu.Lock()
	defer lowBalanceStoreMu.Unlock()

	now := lowBalanceNow()
	key := lowBalanceNotifyKey(userID)
	if state, ok := lowBalanceNotifyStore[key]; ok {
		nextStep := nextLowBalanceNotifyStep(state, now, remindDays)
		if nextStep < 0 {
			return false
		}
		state.LastStep = nextStep
		if state.EpisodeStartUnix <= 0 {
			state.EpisodeStartUnix = now.Unix()
		}
		lowBalanceNotifyStore[key] = state
		return true
	}
	lowBalanceNotifyStore[key] = lowBalanceNotifyState{
		EpisodeStartUnix: now.Unix(),
		LastStep:         0,
	}
	return true
}

func ensureLowBalanceMemoryCleanup() {
	lowBalanceCleanupMu.Lock()
	defer lowBalanceCleanupMu.Unlock()
	if lowBalanceCleanupCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	lowBalanceCleanupCancel = cancel
	common.SafeCtxGo(ctx, "low-balance-memory-cleanup", func() {
		ticker := time.NewTicker(lowBalanceMemoryCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				pruneLowBalanceMemoryState(now)
			}
		}
	})
}

func pruneLowBalanceMemoryState(now time.Time) {
	cutoff := now.Add(-time.Duration(lowBalanceMemoryRetentionDays) * 24 * time.Hour).Unix()
	lowBalanceStoreMu.Lock()
	defer lowBalanceStoreMu.Unlock()
	for key, state := range lowBalanceNotifyStore {
		if state.EpisodeStartUnix <= 0 || state.EpisodeStartUnix < cutoff {
			delete(lowBalanceNotifyStore, key)
		}
	}
}

func nextLowBalanceNotifyStep(state lowBalanceNotifyState, now time.Time, remindDays []int) int {
	if len(remindDays) == 0 {
		return -1
	}
	if state.EpisodeStartUnix <= 0 {
		return 0
	}
	if state.LastStep >= len(remindDays)-1 {
		return -1
	}
	// Day-1 includes the day the user first entered low-balance mode.
	elapsedDays := int(now.Sub(time.Unix(state.EpisodeStartUnix, 0)).Hours()/24) + 1
	for idx := len(remindDays) - 1; idx > state.LastStep; idx-- {
		if elapsedDays >= remindDays[idx] {
			return idx
		}
	}
	return -1
}

func lowBalanceNotifyStateTTL(remindDays []int) time.Duration {
	lastDay := 30
	if n := len(remindDays); n > 0 {
		lastDay = remindDays[n-1]
	}
	ttlDays := lastDay + lowBalanceNotifyRedisBufferTTLDay
	if ttlDays < lowBalanceNotifyRedisMinTTLDays {
		ttlDays = lowBalanceNotifyRedisMinTTLDays
	}
	return time.Duration(ttlDays) * 24 * time.Hour
}

func resetLowBalanceEmailCooldown(userID int) {
	if userID <= 0 {
		return
	}
	key := lowBalanceNotifyKey(userID)
	var redisDeleteErr error
	if common.RedisEnabled && common.RDB != nil {
		redisDeleteErr = common.RedisDel(key)
	}
	lowBalanceStoreMu.Lock()
	delete(lowBalanceNotifyStore, key)
	lowBalanceStoreMu.Unlock()
	if redisDeleteErr != nil {
		common.SysError(fmt.Sprintf("failed to reset low-balance notify state for user %d (key=%s): %v; in-memory state was still cleared", userID, key, redisDeleteErr))
	}
}

// handleLowBalanceEmailSendFailure records a send failure and resets the
// schedule state so the notification can be retried later. The retry is
// gated by an exponential cooldown (see isLowBalanceSendInCooldown) to
// avoid hammering a broken SMTP server on every quota check.
func handleLowBalanceEmailSendFailure(userID int) {
	if userID <= 0 {
		return
	}
	lowBalanceSendFailMu.Lock()
	rec := lowBalanceSendFails[userID]
	rec.Count++
	rec.LastFail = lowBalanceNow()
	lowBalanceSendFails[userID] = rec
	lowBalanceSendFailMu.Unlock()

	resetLowBalanceEmailCooldown(userID)
}

// clearLowBalanceSendFailure removes the failure counter for a user,
// called when an email is sent successfully or the low-balance episode
// ends (user balance goes above threshold).
func clearLowBalanceSendFailure(userID int) {
	lowBalanceSendFailMu.Lock()
	delete(lowBalanceSendFails, userID)
	lowBalanceSendFailMu.Unlock()
}

// isLowBalanceSendInCooldown returns true if a recent send failure puts the
// user in a retry cooldown period. The cooldown grows exponentially with
// consecutive failures: base * 2^min(failCount-1, maxExp), capping at 16h.
func isLowBalanceSendInCooldown(userID int) bool {
	lowBalanceSendFailMu.Lock()
	rec, ok := lowBalanceSendFails[userID]
	lowBalanceSendFailMu.Unlock()

	if !ok || rec.Count == 0 {
		return false
	}
	exp := rec.Count - 1
	if exp > lowBalanceSendRetryMaxExponent {
		exp = lowBalanceSendRetryMaxExponent
	}
	cooldown := lowBalanceSendRetryCooldown * time.Duration(1<<uint(exp))
	return lowBalanceNow().Sub(rec.LastFail) < cooldown
}

func lowBalanceNotifyKey(userID int) string {
	return fmt.Sprintf("notify:low_balance_email:%d", userID)
}
