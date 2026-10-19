package model

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestIdentityL1EvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := newBoundedIdentityCache(time.Minute, 2, 1024)
	now := time.Now()
	cache.set("a", `{"token_id":1}`, now)
	cache.set("b", `{"token_id":2}`, now)
	if _, ok := cache.get("a", now); !ok {
		t.Fatal("expected a to be cached")
	}
	cache.set("c", `{"token_id":3}`, now)

	if _, ok := cache.get("b", now); ok {
		t.Fatal("expected least recently used entry b to be evicted")
	}
	if _, ok := cache.get("a", now); !ok {
		t.Fatal("expected recently used entry a to remain")
	}
	if stats := cache.snapshot(true); stats.Entries != 2 || stats.Evictions != 1 {
		t.Fatalf("unexpected cache stats: %+v", stats)
	}
}

func TestIdentityL1ExpiresAndHonorsByteBudget(t *testing.T) {
	cache := newBoundedIdentityCache(time.Second, 10, 180)
	now := time.Now()
	cache.set("too-large", "x", now)
	cache.set("too-large", string(make([]byte, 200)), now)
	if stats := cache.snapshot(true); stats.Entries != 0 {
		t.Fatalf("oversized replacement must remove the previous entry: %+v", stats)
	}

	cache.set("a", "x", now)
	if _, ok := cache.get("a", now.Add(2*time.Second)); ok {
		t.Fatal("expired entry must miss")
	}
	if stats := cache.snapshot(true); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("expired entry must be removed: %+v", stats)
	}
}

func TestIdentityL1ConcurrentAccessStaysBounded(t *testing.T) {
	cache := newBoundedIdentityCache(time.Minute, 50, 16*1024)
	now := time.Now()
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := 0; index < 250; index++ {
				digest := fmt.Sprintf("token-%d", (worker+index)%100)
				cache.set(digest, `{"token_id":1}`, now)
				cache.get(digest, now)
				if index%7 == 0 {
					cache.delete(digest)
				}
			}
		}()
	}
	wg.Wait()

	stats := cache.snapshot(true)
	if stats.Entries > stats.MaxEntries {
		t.Fatalf("entry bound exceeded: %+v", stats)
	}
	if stats.Bytes > stats.MaxBytes {
		t.Fatalf("byte bound exceeded: %+v", stats)
	}
}

func TestIdentityL1RejectsFillStartedBeforeInvalidation(t *testing.T) {
	cache := newBoundedIdentityCache(time.Minute, 10, 1024)
	now := time.Now()
	generation := cache.captureFillGeneration()

	// This models Redis returning an old projection while a policy
	// invalidation is delivered before the caller can populate L1.
	cache.delete("token-digest")
	if cache.setIfGeneration("token-digest", `{"token_id":1}`, generation, now) {
		t.Fatal("fill started before invalidation must not repopulate L1")
	}
	if _, ok := cache.get("token-digest", now); ok {
		t.Fatal("stale projection must not remain in L1")
	}
}

func TestIdentityL1AllowsFillWithoutInvalidation(t *testing.T) {
	cache := newBoundedIdentityCache(time.Minute, 10, 1024)
	now := time.Now()
	generation := cache.captureFillGeneration()

	if !cache.setIfGeneration("token-digest", `{"token_id":1}`, generation, now) {
		t.Fatal("fill should succeed when no invalidation was observed")
	}
	if _, ok := cache.get("token-digest", now); !ok {
		t.Fatal("expected guarded fill to populate L1")
	}
}

func BenchmarkIdentityL1Hit(b *testing.B) {
	cache := newBoundedIdentityCache(time.Minute, 20_000, 64*1024*1024)
	now := time.Now()
	cache.set("stable-token-digest", `{"token_id":1,"user_id":2,"group":"default"}`, now)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, ok := cache.get("stable-token-digest", now); !ok {
			b.Fatal("expected benchmark entry to remain cached")
		}
	}
}
