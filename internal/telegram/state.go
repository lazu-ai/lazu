package telegram

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// Short-lived coordination state (alert dedupe, mute, rate limits) lives in
// Redis so the API and the jobs process agree. A deployment without Redis
// runs in one process, where memory is the same thing.

const keyPrefix = "lazu:telegram:"

var local = struct {
	sync.Mutex
	until map[string]time.Time
	value map[string]string
}{until: map[string]time.Time{}, value: map[string]string{}}

func redisReady() bool { return common.RedisEnabled && common.RDB != nil }

// claim takes key for ttl and reports whether it was free.
func claim(ctx context.Context, key string, ttl time.Duration) bool {
	if redisReady() {
		ok, err := common.RDB.SetNX(ctx, keyPrefix+key, "1", ttl).Result()
		if err == nil {
			return ok
		}
		common.SysError("telegram: redis claim failed, using process memory: " + err.Error())
	}
	now := time.Now()
	local.Lock()
	defer local.Unlock()
	if until, held := local.until[key]; held && now.Before(until) {
		return false
	}
	local.until[key] = now.Add(ttl)
	return true
}

// release frees a claim so the next attempt may take it.
func release(ctx context.Context, key string) {
	if redisReady() {
		_ = common.RDB.Del(ctx, keyPrefix+key).Err()
	}
	local.Lock()
	delete(local.until, key)
	local.Unlock()
}

// putValue stores value under key for ttl; a zero ttl deletes it.
func putValue(ctx context.Context, key, value string, ttl time.Duration) error {
	if redisReady() {
		if ttl <= 0 {
			return common.RDB.Del(ctx, keyPrefix+key).Err()
		}
		return common.RDB.Set(ctx, keyPrefix+key, value, ttl).Err()
	}
	local.Lock()
	defer local.Unlock()
	if ttl <= 0 {
		delete(local.value, key)
		delete(local.until, "value:"+key)
		return nil
	}
	local.value[key] = value
	local.until["value:"+key] = time.Now().Add(ttl)
	return nil
}

// getValue returns the value under key, or "" when absent or expired.
func getValue(ctx context.Context, key string) string {
	if redisReady() {
		v, err := common.RDB.Get(ctx, keyPrefix+key).Result()
		if err == nil {
			return v
		}
		return ""
	}
	local.Lock()
	defer local.Unlock()
	if until, ok := local.until["value:"+key]; !ok || time.Now().After(until) {
		return ""
	}
	return local.value[key]
}

// countWithin adds one to a fixed-window counter and returns the new count.
func countWithin(ctx context.Context, key string, window time.Duration) int64 {
	if redisReady() {
		full := keyPrefix + key
		n, err := common.RDB.Incr(ctx, full).Result()
		if err == nil {
			if n == 1 {
				_ = common.RDB.Expire(ctx, full, window).Err()
			}
			return n
		}
	}
	now := time.Now()
	local.Lock()
	defer local.Unlock()
	if until, ok := local.until["count:"+key]; !ok || now.After(until) {
		local.until["count:"+key] = now.Add(window)
		local.value["count:"+key] = "0"
	}
	n, _ := strconv.ParseInt(local.value["count:"+key], 10, 64)
	n++
	local.value["count:"+key] = strconv.FormatInt(n, 10)
	return n
}
