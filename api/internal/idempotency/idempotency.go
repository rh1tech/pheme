// Package idempotency makes a retried ingest request safe to send twice.
//
// The public ingest endpoint accepts an Idempotency-Key header, and the architecture document
// promises what that header universally means: sending the same request again is not the same as
// sending a second notification. It was accepted, carried on the task through the broker, and used
// by nothing — so a caller retrying after a timeout, which is precisely what the header exists for,
// woke every subscriber's phone twice.
//
// Retries are not exotic here. An HTTP client that times out on a request the server did receive
// has no way to know whether it arrived, and the honest thing for it to do is send it again. That
// is the case this exists for.
//
// The store is deliberately tiny: remember that a key was used, for a while. It is not a record of
// the response, so a duplicate is answered "accepted" rather than replayed — for a notification,
// "we already have this one" and "we have taken this one" are the same answer to the caller.
package idempotency

import (
	"context"
	"sync"
	"time"
)

// Window is how long a key is remembered.
//
// Long enough to cover a client's retry schedule, including a person hitting send again after a
// spinner sat there; short enough that a key reused next week is treated as new. A caller sending
// the same key a day apart means it, and a caller retrying a day later has long since given up.
const Window = 24 * time.Hour

// Store remembers idempotency keys.
type Store interface {
	// Seen records the key and reports whether it had already been recorded. The check and the
	// record are one operation: two copies of the same request arriving at two instances at the
	// same moment must not both be told "new".
	Seen(ctx context.Context, key string, window time.Duration) (bool, error)
}

// Memory is an in-process Store, for development and tests. Production uses Redis so that two
// instances agree — with per-instance memory, a retry that lands on the other instance is a
// duplicate notification, which is the whole failure this prevents.
type Memory struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func NewMemory() *Memory { return &Memory{seen: map[string]time.Time{}} }

// Seen records key and reports whether it was already present.
func (m *Memory) Seen(_ context.Context, key string, window time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if expires, ok := m.seen[key]; ok && now.Before(expires) {
		return true, nil
	}
	// Expiry is lazy, swept while we are already holding the lock and already walking nothing
	// else. A key nobody asks about again is a few dozen bytes; the sweep keeps a long-running
	// development server from growing without bound.
	if len(m.seen) > sweepThreshold {
		for k, expires := range m.seen {
			if now.After(expires) {
				delete(m.seen, k)
			}
		}
	}
	m.seen[key] = now.Add(window)
	return false, nil
}

// sweepThreshold is when the lazy expiry sweep becomes worth its cost.
const sweepThreshold = 1024
