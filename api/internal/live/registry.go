package live

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// registry holds the connected subscribers and decides which of them an event is for.
//
// It is shared by both buses so the in-process one and the Redis one cannot drift apart on
// delivery semantics — which is the kind of difference that shows up only in production, where the
// bus is Redis and the tests are not.
//
// Subscribers are INDEXED BY USER. Without that index, delivering one message meant offering it to
// every open connection on the instance and letting each one work out that it was not the intended
// recipient. That is O(connected users) work per message no matter how small the conversation: at a
// thousand streams, a ten-person conversation woke a thousand goroutines to deliver ten events, and
// the scheduler churn from doing so was about 40% of the server's CPU under load.
type registry struct {
	mu     sync.RWMutex
	byUser map[string]map[chan Event]struct{}
	// owner lets a cancel find its way back into byUser, and gives broadcast a list to walk.
	owner map[chan Event]string

	dropped atomic.Int64
	logger  *slog.Logger
}

func newRegistry(logger *slog.Logger) *registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &registry{
		byUser: map[string]map[chan Event]struct{}{},
		owner:  map[chan Event]string{},
		logger: logger,
	}
}

// add registers a subscriber for userID and returns its channel and a remove function. onEmpty, if
// non-nil, is called after the last subscriber leaves.
func (r *registry) add(userID string, onFirst, onEmpty func()) (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)

	r.mu.Lock()
	first := len(r.owner) == 0
	if r.byUser[userID] == nil {
		r.byUser[userID] = map[chan Event]struct{}{}
	}
	r.byUser[userID][ch] = struct{}{}
	r.owner[ch] = userID
	if first && onFirst != nil {
		onFirst()
	}
	r.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			r.mu.Lock()
			uid, ok := r.owner[ch]
			if ok {
				delete(r.owner, ch)
				if subs := r.byUser[uid]; subs != nil {
					delete(subs, ch)
					if len(subs) == 0 {
						delete(r.byUser, uid)
					}
				}
				// Closed under the write lock, and deliver only ever sends under the read lock.
				// Those two must never overlap: closing a channel another goroutine is sending on
				// is a data race whether or not the resulting panic is recovered.
				close(ch)
			}
			empty := len(r.owner) == 0
			r.mu.Unlock()

			if ok && empty && onEmpty != nil {
				onEmpty()
			}
		})
	}
}

// deliver routes one event to the subscribers entitled to it.
//
// An event that names its recipients goes only to their connections. One that does not is
// broadcast, and the receiving stream authorises it for itself — that is the path channel
// broadcasts take, and the fallback when a conversation's roster could not be read. Broadcasting
// is the expensive path, so anything on a hot path should name its recipients.
func (r *registry) deliver(e Event) {
	var dropped int64

	r.mu.RLock()
	if len(e.Recipients) > 0 {
		for _, uid := range e.Recipients {
			for ch := range r.byUser[uid] {
				select {
				case ch <- e:
				default:
					dropped++
				}
			}
		}
	} else {
		for ch := range r.owner {
			select {
			case ch <- e:
			default:
				dropped++
			}
		}
	}
	subscribers := len(r.owner)
	r.mu.RUnlock()

	if dropped == 0 {
		return
	}
	total := r.dropped.Add(dropped)
	// Logged sparsely and outside the lock: when drops happen there are a great many of them, and
	// a line per dropped event would bury the reason under its own symptom.
	if before := total - dropped; before == 0 || before/1000 != total/1000 {
		r.logger.Warn("live event dropped: a subscriber is not keeping up",
			"dropped_total", total, "subscribers", subscribers)
	}
}

func (r *registry) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.owner)
}

// Dropped reports how many deliveries were discarded because a subscriber was too slow. A drop is
// invisible to the user — the message simply never appears — so it is at least countable.
func (r *registry) Dropped() int64 { return r.dropped.Load() }
