package service

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"os"
	"sync"
	"testing"
	"time"
)

func TestChannelCapacityAdmissionAtomicAcrossScopes(t *testing.T) {
	store := channelLimitStore{scopes: make(map[string]*localChannelLimit)}
	now := time.Now()
	group := channelLimitScope{key: "group", rps: 1, inFlight: 1}
	release, err := store.acquire([]channelLimitScope{group}, "busy", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	channel := channelLimitScope{key: "channel", rps: 1, inFlight: 2}
	if _, err = store.acquire([]channelLimitScope{channel, group}, "rejected", time.Minute, now); err == nil {
		t.Fatal("group admitted too many requests")
	}
	if store.scopes["channel"].tokens != 1 || len(store.scopes["channel"].leases) != 0 {
		t.Fatal("rejected group admission consumed channel capacity")
	}
	release()
	release()
	first, err := store.acquire([]channelLimitScope{channel, group}, "next", time.Minute, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	first()
	first()
}

func TestChannelCapacityLeaseExpirationAndLateRelease(t *testing.T) {
	store := channelLimitStore{scopes: make(map[string]*localChannelLimit)}
	scope := []channelLimitScope{{key: "channel", inFlight: 1}}
	now := time.Now()
	old, err := store.acquire(scope, "old", time.Second, now)
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.acquire(scope, "next", time.Minute, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	old()
	if _, err = store.acquire(scope, "third", time.Minute, now.Add(3*time.Second)); err == nil {
		t.Fatal("late release freed another lease")
	}
	next()
}

func TestChannelCapacityConcurrentAdmission(t *testing.T) {
	store := channelLimitStore{scopes: make(map[string]*localChannelLimit)}
	scopes := []channelLimitScope{{key: "channel", inFlight: 4}, {key: "group", inFlight: 3}}
	var wg sync.WaitGroup
	var lock sync.Mutex
	var releases []func()
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, err := store.acquire(scopes, time.Unix(int64(i), 0).String(), time.Minute, time.Now())
			if err == nil {
				lock.Lock()
				releases = append(releases, release)
				lock.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(releases) != 3 {
		t.Fatalf("admitted %d, expected shared ceiling 3", len(releases))
	}
	for _, release := range releases {
		release()
	}
}

func TestChannelCapacityRedisSharedAtomicAndExpiry(t *testing.T) {
	raw := os.Getenv("TEST_REDIS_URL")
	if raw == "" {
		t.Skip("TEST_REDIS_URL not configured")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	old := common.RDB
	common.RDB = client
	defer func() { common.RDB = old }()
	ctx := context.Background()
	prefix := "fixture:" + common.GenerateRequestID()
	channelScope := channelLimitScope{key: prefix + ":channel", rps: 10, inFlight: 10}
	groupScope := channelLimitScope{key: prefix + ":group", rps: 2, inFlight: 1}
	defer client.Del(ctx, channelScope.key+":rate", channelScope.key+":leases", groupScope.key+":rate", groupScope.key+":leases")
	first, err := acquireRedisChannelCapacity(ctx, []channelLimitScope{channelScope, groupScope}, "old", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = acquireRedisChannelCapacity(ctx, []channelLimitScope{channelScope, groupScope}, "rejected", time.Second); err == nil {
		t.Fatal("shared admission exceeded")
	}
	// Rejection must not consume the per-channel rate token.
	tokens, err := client.HGet(ctx, channelScope.key+":rate", "tokens").Float64()
	if err != nil || tokens < 9 {
		t.Fatalf("partial scope admission tokens=%f err=%v", tokens, err)
	}
	// The old lease outlives the rejection checks on a loaded runner; Redis
	// TIME then expires it before the next admission.
	time.Sleep(400 * time.Millisecond)
	second, err := acquireRedisChannelCapacity(ctx, []channelLimitScope{channelScope, groupScope}, "new", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	first()
	first()
	if _, err = acquireRedisChannelCapacity(ctx, []channelLimitScope{channelScope, groupScope}, "third", time.Second); err == nil {
		t.Fatal("expired release freed current lease")
	}
	second()
	second()
}
