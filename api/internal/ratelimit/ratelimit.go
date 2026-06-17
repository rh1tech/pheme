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
}

type bucket struct {
	tokens float64
	last   time.Time
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
func (t *TokenBucket) Allow(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	b, ok := t.buckets[key]
	if !ok {
		t.buckets[key] = &bucket{tokens: t.capacity - 1, last: now}
		return true
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens = min(t.capacity, b.tokens+elapsed*t.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
