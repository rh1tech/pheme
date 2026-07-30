// Package ratelimit provides request rate limiting for the public ingest
// endpoint. The development implementation is an in-memory token bucket;
// production should use Redis so limits are shared across instances.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter reports whether an action for a given key is allowed right now.
type Limiter interface {
	Allow(key string) bool
}

// TokenBucket is an in-memory per-key token-bucket Limiter.
type TokenBucket struct {
	mu       sync.Mutex
	rate     float64 // tokens added per second
	capacity float64
	buckets  map[string]*bucket
	once     sync.Once
}

type bucket struct {
	tokens     float64
	last       time.Time
	lastAccess time.Time
}

// NewTokenBucket creates a limiter allowing bursts up to capacity and refilling
// at ratePerSec tokens per second.
func NewTokenBucket(ratePerSec, capacity float64) *TokenBucket {
	return &TokenBucket{
		rate:     ratePerSec,
		capacity: capacity,
		buckets:  map[string]*bucket{},
	}
}

// Allow consumes one token for key, returning false when the bucket is empty.
// On first call it also starts a background goroutine that evicts idle buckets.
func (t *TokenBucket) Allow(key string) bool {
	t.once.Do(func() {
		go t.evictLoop()
	})

	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	b, ok := t.buckets[key]
	if !ok {
		t.buckets[key] = &bucket{tokens: t.capacity - 1, last: now, lastAccess: now}
		return true
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens = min(t.capacity, b.tokens+elapsed*t.rate)
	b.last = now
	b.lastAccess = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictLoop runs in the background and removes buckets that have not been
// accessed in the last 10 minutes, preventing unbounded memory growth.
func (t *TokenBucket) evictLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		t.mu.Lock()
		for key, b := range t.buckets {
			if b.lastAccess.Before(cutoff) {
				delete(t.buckets, key)
			}
		}
		t.mu.Unlock()
	}
}
