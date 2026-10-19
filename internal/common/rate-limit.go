package common

import (
	"sync"
	"time"
)

type InMemoryRateLimiter struct {
	mutex sync.Mutex
	state *inMemoryRateLimiterState
}

type inMemoryRateLimiterState struct {
	store              map[string]*[]int64
	mutex              sync.Mutex
	expirationDuration time.Duration
	stop               chan struct{}
	stopOnce           sync.Once
}

func (l *InMemoryRateLimiter) Init(expirationDuration time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.state != nil {
		return
	}

	state := &inMemoryRateLimiterState{
		store:              make(map[string]*[]int64),
		expirationDuration: expirationDuration,
		stop:               make(chan struct{}),
	}
	l.state = state
	if expirationDuration > 0 {
		go state.clearExpiredItems()
	}
}

// Reset discards the active window state and stops its cleanup worker. It is
// primarily useful for tests that reuse a process, but is safe to call while a
// request still holds the previous state because that state remains valid.
func (l *InMemoryRateLimiter) Reset() {
	l.mutex.Lock()
	state := l.state
	l.state = nil
	l.mutex.Unlock()

	if state != nil {
		state.stopOnce.Do(func() {
			close(state.stop)
		})
	}
}

func (s *inMemoryRateLimiterState) clearExpiredItems() {
	ticker := time.NewTicker(s.expirationDuration)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mutex.Lock()
			now := time.Now().Unix()
			for key := range s.store {
				queue := s.store[key]
				size := len(*queue)
				if size == 0 || now-(*queue)[size-1] > int64(s.expirationDuration.Seconds()) {
					delete(s.store, key)
				}
			}
			s.mutex.Unlock()
		case <-s.stop:
			return
		}
	}
}

// Request parameter duration's unit is seconds
func (l *InMemoryRateLimiter) Request(key string, maxRequestNum int, duration int64) bool {
	allowed, _ := l.RequestWithWait(key, maxRequestNum, duration)
	return allowed
}

// RequestWithWait is Request that also says, when it refuses, how many seconds
// remain until the oldest request in the window ages out.
func (l *InMemoryRateLimiter) RequestWithWait(key string, maxRequestNum int, duration int64) (bool, int64) {
	l.mutex.Lock()
	state := l.state
	if state == nil {
		state = &inMemoryRateLimiterState{
			store: make(map[string]*[]int64),
			stop:  make(chan struct{}),
		}
		l.state = state
	}
	l.mutex.Unlock()

	state.mutex.Lock()
	defer state.mutex.Unlock()
	// [old <-- new]
	queue, ok := state.store[key]
	now := time.Now().Unix()
	if ok {
		if len(*queue) < maxRequestNum {
			*queue = append(*queue, now)
			return true, 0
		} else {
			if now-(*queue)[0] >= duration {
				*queue = (*queue)[1:]
				*queue = append(*queue, now)
				return true, 0
			} else {
				return false, max(1, duration-(now-(*queue)[0]))
			}
		}
	} else {
		s := make([]int64, 0, maxRequestNum)
		state.store[key] = &s
		*(state.store[key]) = append(*(state.store[key]), now)
	}
	return true, 0
}
