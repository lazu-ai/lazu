package model

import (
	"container/list"
	"strings"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// boundedIdentityEntry stores serialized projection data. Keeping the raw
// payload avoids sharing mutable map/slice fields between concurrent requests
// and gives the cache a predictable byte budget.
type boundedIdentityEntry struct {
	digest    string
	payload   string
	expiresAt time.Time
	bytes     int
}

type boundedIdentityCache struct {
	mu                     sync.Mutex
	entries                map[string]*list.Element
	recency                *list.List
	ttl                    time.Duration
	maxEntries             int
	maxBytes               int
	bytes                  int
	evictions              int64
	invalidationGeneration uint64
}

type IdentityL1Stats struct {
	Enabled    bool  `json:"enabled"`
	Entries    int   `json:"entries"`
	Bytes      int   `json:"bytes"`
	MaxEntries int   `json:"max_entries"`
	MaxBytes   int   `json:"max_bytes"`
	TTLSeconds int   `json:"ttl_seconds"`
	Evictions  int64 `json:"evictions"`
}

const boundedIdentityEntryOverheadBytes = 128

func newBoundedIdentityCache(ttl time.Duration, maxEntries, maxBytes int) *boundedIdentityCache {
	ttl, maxEntries, maxBytes = normalizeIdentityL1Config(ttl, maxEntries, maxBytes)
	return &boundedIdentityCache{
		entries:    make(map[string]*list.Element),
		recency:    list.New(),
		ttl:        ttl,
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
	}
}

func normalizeIdentityL1Config(ttl time.Duration, maxEntries, maxBytes int) (time.Duration, int, int) {
	if ttl <= 0 {
		ttl = 5 * time.Second
	}
	if maxEntries <= 0 {
		maxEntries = 1
	}
	if maxBytes <= 0 {
		maxBytes = 1
	}
	return ttl, maxEntries, maxBytes
}

func (cache *boundedIdentityCache) get(digest string, now time.Time) (string, bool) {
	if cache == nil || strings.TrimSpace(digest) == "" {
		return "", false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, ok := cache.entries[digest]
	if !ok {
		return "", false
	}
	entry := element.Value.(*boundedIdentityEntry)
	if !entry.expiresAt.After(now) {
		cache.removeElement(element, false)
		return "", false
	}
	cache.recency.MoveToFront(element)
	return entry.payload, true
}

func (cache *boundedIdentityCache) set(digest, payload string, now time.Time) {
	if cache == nil || strings.TrimSpace(digest) == "" || payload == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.setLocked(digest, payload, now)
}

// captureFillGeneration and setIfGeneration close the race between a shared
// Redis read (or conditional Redis write) and an L1 fill. Every local or
// Pub/Sub invalidation advances the cache-wide generation while holding the
// same lock. If any invalidation is observed while Redis is in flight, the L1
// fill is skipped. Using one cache-wide generation keeps the guard bounded;
// an unrelated invalidation may cause one harmless L1 miss but can never admit
// stale policy data.
func (cache *boundedIdentityCache) captureFillGeneration() uint64 {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.invalidationGeneration
}

func (cache *boundedIdentityCache) setIfGeneration(digest, payload string, generation uint64, now time.Time) bool {
	if cache == nil || strings.TrimSpace(digest) == "" || payload == "" {
		return false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.invalidationGeneration != generation {
		return false
	}
	cache.setLocked(digest, payload, now)
	return true
}

func (cache *boundedIdentityCache) setLocked(digest, payload string, now time.Time) {
	entryBytes := len(digest) + len(payload) + boundedIdentityEntryOverheadBytes
	if element, ok := cache.entries[digest]; ok {
		cache.removeElement(element, false)
	}
	if entryBytes > cache.maxBytes {
		return
	}
	entry := &boundedIdentityEntry{
		digest:    digest,
		payload:   payload,
		expiresAt: now.Add(cache.ttl),
		bytes:     entryBytes,
	}
	element := cache.recency.PushFront(entry)
	cache.entries[digest] = element
	cache.bytes += entryBytes

	for len(cache.entries) > cache.maxEntries || cache.bytes > cache.maxBytes {
		cache.removeElement(cache.recency.Back(), true)
	}
}

func (cache *boundedIdentityCache) delete(digest string) {
	if cache == nil || strings.TrimSpace(digest) == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.invalidationGeneration++
	if element, ok := cache.entries[digest]; ok {
		cache.removeElement(element, false)
	}
}

func (cache *boundedIdentityCache) clear() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.invalidationGeneration++
	cache.entries = make(map[string]*list.Element)
	cache.recency.Init()
	cache.bytes = 0
}

func (cache *boundedIdentityCache) removeElement(element *list.Element, evicted bool) {
	if cache == nil || element == nil {
		return
	}
	entry := element.Value.(*boundedIdentityEntry)
	delete(cache.entries, entry.digest)
	cache.recency.Remove(element)
	cache.bytes -= entry.bytes
	if cache.bytes < 0 {
		cache.bytes = 0
	}
	if evicted {
		cache.evictions++
		common.IncrementHotPathCounter(common.HotPathCounterIdentitySnapshotEvicted)
	}
}

func (cache *boundedIdentityCache) snapshot(enabled bool) IdentityL1Stats {
	if cache == nil {
		return IdentityL1Stats{Enabled: enabled}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return IdentityL1Stats{
		Enabled:    enabled,
		Entries:    len(cache.entries),
		Bytes:      cache.bytes,
		MaxEntries: cache.maxEntries,
		MaxBytes:   cache.maxBytes,
		TTLSeconds: int(cache.ttl / time.Second),
		Evictions:  cache.evictions,
	}
}

func SnapshotIdentityL1() IdentityL1Stats {
	return identitySnapshotL1.snapshot(IdentitySnapshotsEnabled())
}
