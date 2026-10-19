package files

import (
	"sync"
	"time"
)

const (
	dataURLCacheTTL      = 10 * time.Minute
	dataURLCacheMaxBytes = int64(1024 * 1024 * 1024)
	dataURLCacheMaxFile  = int64(5 * 1024 * 1024)
)

type dataURLCacheEntry struct {
	value     string
	sizeBytes int64
	expiresAt time.Time
}

type dataURLCache struct {
	mu         sync.Mutex
	totalBytes int64
	items      map[string]dataURLCacheEntry
}

var globalDataURLCache = &dataURLCache{items: make(map[string]dataURLCacheEntry)}

func (c *dataURLCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.items, key)
		c.totalBytes -= entry.sizeBytes
		return "", false
	}
	return entry.value, true
}

func (c *dataURLCache) set(key string, value string) {
	size := int64(len(value))
	if size <= 0 || size > dataURLCacheMaxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked(time.Now())
	if existing, ok := c.items[key]; ok {
		c.totalBytes -= existing.sizeBytes
	}
	for c.totalBytes+size > dataURLCacheMaxBytes {
		if !c.evictOneLocked() {
			break
		}
	}
	c.items[key] = dataURLCacheEntry{
		value:     value,
		sizeBytes: size,
		expiresAt: time.Now().Add(dataURLCacheTTL),
	}
	c.totalBytes += size
}

func (c *dataURLCache) evictExpiredLocked(now time.Time) {
	for key, entry := range c.items {
		if now.After(entry.expiresAt) {
			delete(c.items, key)
			c.totalBytes -= entry.sizeBytes
		}
	}
}

func (c *dataURLCache) evictOneLocked() bool {
	var selected string
	var selectedExpires time.Time
	for key, entry := range c.items {
		if selected == "" || entry.expiresAt.Before(selectedExpires) {
			selected = key
			selectedExpires = entry.expiresAt
		}
	}
	if selected == "" {
		return false
	}
	entry := c.items[selected]
	delete(c.items, selected)
	c.totalBytes -= entry.sizeBytes
	return true
}
