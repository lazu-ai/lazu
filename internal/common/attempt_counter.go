package common

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
)

// Fixed-window counters keyed by what an attacker is aiming at — an account,
// a mailbox — rather than by where they come from. A per-IP limit is no limit
// to someone with a pool of addresses; these hold no matter how many there are.

type attemptWindow struct {
	count     int64
	expiresAt time.Time
}

var (
	attemptMutex sync.Mutex
	attemptStore = map[string]attemptWindow{}
)

func attemptKey(scope string, subject string) string {
	return "attempts:" + scope + ":" + strings.ToLower(strings.TrimSpace(subject))
}

// AttemptCount returns how many attempts the subject has made in the current
// window and how long until the window resets.
func AttemptCount(scope string, subject string) (int64, time.Duration) {
	key := attemptKey(scope, subject)
	if RedisEnabled && RDB != nil {
		ctx := context.Background()
		count, err := RDB.Get(ctx, key).Int64()
		if err == nil {
			ttl, _ := RDB.TTL(ctx, key).Result()
			return count, max(ttl, 0)
		}
		if errors.Is(err, redis.Nil) {
			return 0, 0
		}
	}
	attemptMutex.Lock()
	defer attemptMutex.Unlock()
	window, ok := attemptStore[key]
	if !ok || time.Now().After(window.expiresAt) {
		return 0, 0
	}
	return window.count, time.Until(window.expiresAt)
}

// RecordAttempt counts one attempt. The window starts at the first attempt and
// does not slide, so a steady trickle cannot keep it open forever.
func RecordAttempt(scope string, subject string, window time.Duration) (int64, time.Duration) {
	key := attemptKey(scope, subject)
	if RedisEnabled && RDB != nil {
		ctx := context.Background()
		count, err := RDB.Incr(ctx, key).Result()
		if err == nil {
			if count == 1 {
				_ = RDB.Expire(ctx, key, window).Err()
				return count, window
			}
			ttl, ttlErr := RDB.TTL(ctx, key).Result()
			if ttlErr == nil && ttl < 0 {
				// A key that lost its expiry would lock the subject out for good.
				_ = RDB.Expire(ctx, key, window).Err()
				ttl = window
			}
			return count, max(ttl, 0)
		}
	}
	attemptMutex.Lock()
	defer attemptMutex.Unlock()
	now := time.Now()
	current, ok := attemptStore[key]
	if !ok || now.After(current.expiresAt) {
		current = attemptWindow{expiresAt: now.Add(window)}
		if len(attemptStore) > 10000 {
			for k, v := range attemptStore {
				if now.After(v.expiresAt) {
					delete(attemptStore, k)
				}
			}
		}
	}
	current.count++
	attemptStore[key] = current
	return current.count, time.Until(current.expiresAt)
}

// ClearAttempts forgets the subject's attempts, e.g. after a successful login.
func ClearAttempts(scope string, subject string) {
	key := attemptKey(scope, subject)
	if RedisEnabled && RDB != nil {
		_ = RDB.Del(context.Background(), key).Err()
	}
	attemptMutex.Lock()
	delete(attemptStore, key)
	attemptMutex.Unlock()
}

// RetryAfterSeconds rounds a remaining window up to whole seconds, at least 1.
func RetryAfterSeconds(remaining time.Duration) int64 {
	seconds := int64((remaining + time.Second - 1) / time.Second)
	return max(seconds, 1)
}

// ResetAttempts drops every in-process counter. Tests that reuse one account
// or mailbox across cases call it; Redis-backed counters are untouched.
func ResetAttempts() {
	attemptMutex.Lock()
	attemptStore = map[string]attemptWindow{}
	attemptMutex.Unlock()
}
