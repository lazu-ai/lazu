package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
)

type channelLimitScope struct {
	key           string
	rps, inFlight int
}
type localChannelLimit struct {
	tokens  float64
	updated time.Time
	leases  map[string]time.Time
}
type channelLimitStore struct {
	mu     sync.Mutex
	scopes map[string]*localChannelLimit
}

var channelRedisWarningAt atomic.Int64

var channelLimits = channelLimitStore{scopes: make(map[string]*localChannelLimit)}

type ChannelCapacityError struct{ RetryAfter time.Duration }

func (e *ChannelCapacityError) Error() string { return "upstream capacity is exhausted" }

var sharedLimitsCache struct {
	sync.Mutex
	until  time.Time
	groups map[string]channelLimitScope
}

// Resolve group limits from configuration, not from whichever channel happens
// to receive traffic first. Unset values do not erase another member's cap.
func sharedChannelLimit(name string, setting dto.ChannelSettings) (channelLimitScope, error) {
	key := fmt.Sprintf("chlimit:{admission}:group:%x", sha256.Sum256([]byte(name)))
	result := channelLimitScope{key: key, rps: setting.SharedMaxRPS, inFlight: setting.SharedMaxInFlight}
	if model.CoreDB() == nil {
		return result, nil
	}
	sharedLimitsCache.Lock()
	defer sharedLimitsCache.Unlock()
	if time.Now().After(sharedLimitsCache.until) {
		var rows []model.Channel
		if err := model.CoreDB().Select("id", "setting").Find(&rows).Error; err != nil {
			return result, err
		}
		groups := make(map[string]channelLimitScope)
		for _, row := range rows {
			s := row.GetSetting()
			if s.SharedLimitGroup == "" {
				continue
			}
			g := groups[s.SharedLimitGroup]
			g.rps = minPositive(g.rps, s.SharedMaxRPS)
			g.inFlight = minPositive(g.inFlight, s.SharedMaxInFlight)
			groups[s.SharedLimitGroup] = g
		}
		sharedLimitsCache.groups = groups
		sharedLimitsCache.until = time.Now().Add(5 * time.Second)
	}
	if g, ok := sharedLimitsCache.groups[name]; ok {
		result.rps = minPositive(result.rps, g.rps)
		result.inFlight = minPositive(result.inFlight, g.inFlight)
	}
	return result, nil
}

func minPositive(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

// A request and its shared group are admitted together. Leases are identified
// independently, so cancellation, retries and duplicate Close cannot free
// another request's slot. The lease deadline exceeds the upstream deadline.
func AcquireChannelCapacity(ctx context.Context, channelID int, setting dto.ChannelSettings) (func(), error) {
	scopes := []channelLimitScope{{key: fmt.Sprintf("chlimit:{admission}:channel:%d", channelID), rps: setting.MaxRPS, inFlight: setting.MaxInFlight}}
	if setting.SharedLimitGroup != "" {
		g, err := sharedChannelLimit(setting.SharedLimitGroup, setting)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, g)
	}
	active := scopes[:0]
	for _, s := range scopes {
		if s.rps > 0 || s.inFlight > 0 {
			active = append(active, s)
		}
	}
	scopes = active
	if len(scopes) == 0 {
		return func() {}, nil
	}
	lease := common.GenerateRequestID()
	ttl := 15 * time.Minute
	if deadline, ok := ctx.Deadline(); ok {
		ttl = time.Until(deadline) + time.Minute
		if ttl < time.Minute {
			ttl = time.Minute
		}
	}
	if common.RedisEnabled && common.RDB != nil {
		release, err := acquireRedisChannelCapacity(ctx, scopes, lease, ttl)
		if err == nil {
			return release, nil
		}
		if _, capacity := err.(*ChannelCapacityError); capacity {
			return nil, err
		}
		now := time.Now().Unix()
		previous := channelRedisWarningAt.Load()
		if now-previous >= 60 && channelRedisWarningAt.CompareAndSwap(previous, now) {
			common.SysLog("channel capacity Redis unavailable; using local in-flight protection")
		}
		// Cross-replica RPS cannot be guaranteed during the outage. Retain the
		// local concurrency ceiling to keep the process and connection pool bounded.
		for i := range scopes {
			scopes[i].rps = 0
		}
	}
	return channelLimits.acquire(scopes, lease, ttl, time.Now())
}

func (store *channelLimitStore) acquire(scopes []channelLimitScope, lease string, ttl time.Duration, now time.Time) (func(), error) {
	store.mu.Lock()
	for _, scope := range scopes {
		s := store.scopes[scope.key]
		if s == nil {
			s = &localChannelLimit{tokens: float64(scope.rps), updated: now, leases: make(map[string]time.Time)}
			store.scopes[scope.key] = s
		}
		if scope.rps > 0 {
			s.tokens = math.Min(float64(scope.rps), s.tokens+now.Sub(s.updated).Seconds()*float64(scope.rps))
		}
		s.updated = now
		for id, end := range s.leases {
			if !end.After(now) {
				delete(s.leases, id)
			}
		}
		if scope.rps > 0 && s.tokens < 1 {
			store.mu.Unlock()
			return nil, &ChannelCapacityError{RetryAfter: time.Duration(math.Ceil((1-s.tokens)/float64(scope.rps)*1000)) * time.Millisecond}
		}
		if scope.inFlight > 0 && len(s.leases) >= scope.inFlight {
			store.mu.Unlock()
			return nil, &ChannelCapacityError{RetryAfter: time.Second}
		}
	}
	for _, scope := range scopes {
		s := store.scopes[scope.key]
		if scope.rps > 0 {
			s.tokens--
		}
		if scope.inFlight > 0 {
			s.leases[lease] = now.Add(ttl)
		}
	}
	store.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			store.mu.Lock()
			defer store.mu.Unlock()
			for _, scope := range scopes {
				delete(store.scopes[scope.key].leases, lease)
			}
		})
	}, nil
}

// The hash tag keeps all admission keys in one Redis Cluster slot. Read all
// scopes before mutating any of them; rejected admissions consume no tokens.
const channelAdmissionLua = `
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + tonumber(clock[2]) / 1000
local ttl = tonumber(ARGV[2])
local values = {}
local retry = 0
for i=1,#KEYS,2 do
 local pos=(i+1)/2
 local rate=tonumber(ARGV[3+(pos-1)*2])
 local cap=tonumber(ARGV[4+(pos-1)*2])
 local raw=redis.call('HMGET',KEYS[i],'tokens','updated')
 local tokens=tonumber(raw[1]) or rate
 local updated=tonumber(raw[2]) or now
 if rate>0 then tokens=math.min(rate,tokens+math.max(0,now-updated)*rate/1000) end
 redis.call('ZREMRANGEBYSCORE',KEYS[i+1],'-inf',now)
 if rate>0 and tokens<1 then retry=math.max(retry,math.ceil((1-tokens)*1000/rate)) end
 if cap>0 and redis.call('ZCARD',KEYS[i+1])>=cap then retry=math.max(retry,1000) end
 values[pos]={tokens,rate,cap}
end
if retry>0 then return retry end
for i=1,#KEYS,2 do
 local v=values[(i+1)/2]
 if v[2]>0 then redis.call('HSET',KEYS[i],'tokens',v[1]-1,'updated',now);redis.call('PEXPIRE',KEYS[i],math.max(ttl,60000)) end
 if v[3]>0 then redis.call('ZADD',KEYS[i+1],now+ttl,ARGV[1]);redis.call('PEXPIRE',KEYS[i+1],ttl+60000) end
end
return 0`

func acquireRedisChannelCapacity(ctx context.Context, scopes []channelLimitScope, lease string, ttl time.Duration) (func(), error) {
	keys := make([]string, 0, len(scopes)*2)
	args := []interface{}{lease, ttl.Milliseconds()}
	for _, s := range scopes {
		keys = append(keys, s.key+":rate", s.key+":leases")
		args = append(args, s.rps, s.inFlight)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	retry, err := common.RDB.Eval(checkCtx, channelAdmissionLua, keys, args...).Int64()
	if err != nil {
		return nil, err
	}
	if retry > 0 {
		return nil, &ChannelCapacityError{RetryAfter: time.Duration(retry) * time.Millisecond}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			for _, s := range scopes {
				if s.inFlight > 0 {
					if err := common.RDB.ZRem(releaseCtx, s.key+":leases", lease).Err(); err != nil {
						common.SysLog("channel lease release failed; expiry will reclaim it")
					}
				}
			}
		})
	}, nil
}
