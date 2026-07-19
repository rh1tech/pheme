package live

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// RedisBus is a Bus backed by Redis pub/sub, so live events published by the
// Dispatcher reach SSE/WebSocket subscribers on any App API instance.
//
// There is ONE Redis subscription per process, shared by every subscriber, not one per
// subscriber. That distinction is the difference between this working and not: a load test at a
// thousand concurrent streams found the server spending 75% of its CPU inside net.dialTCP,
// opening Redis connections, because each SSE stream used to call client.Subscribe() and get its
// own. A thousand users meant a thousand Redis connections and a connection pool thrashing hard
// enough to take the machine down at two messages a second.
//
// Sharing it also means each event is unmarshalled once for the whole process rather than once per
// connected user.
type RedisBus struct {
	client  *redis.Client
	channel string
	logger  *slog.Logger

	mu      sync.RWMutex
	subs    map[chan Event]struct{}
	started bool
	stop    context.CancelFunc

	// dropped counts events discarded because a subscriber was not reading fast enough. A drop is
	// a message a user never sees, with nothing in any HTTP response to say so, so it is at least
	// counted and logged rather than being invisible.
	dropped atomic.Int64
}

// NewRedisBus creates a Redis-backed bus publishing on the given channel.
func NewRedisBus(client *redis.Client, channel string, logger *slog.Logger) *RedisBus {
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisBus{
		client:  client,
		channel: channel,
		logger:  logger,
		subs:    map[chan Event]struct{}{},
	}
}

// Publish serialises and publishes the event to Redis.
func (b *RedisBus) Publish(e Event) {
	payload, err := json.Marshal(e)
	if err != nil {
		b.logger.Error("live marshal", "error", err)
		return
	}
	if err := b.client.Publish(context.Background(), b.channel, payload).Err(); err != nil {
		b.logger.Error("live publish", "error", err)
	}
}

// Subscribe registers a subscriber on the shared Redis subscription and returns its channel plus a
// cancel function. The first subscriber starts the subscription; the last one to leave stops it, so
// an idle process holds no Redis connection.
func (b *RedisBus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)

	b.mu.Lock()
	b.subs[ch] = struct{}{}
	if !b.started {
		b.startLocked()
	}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[ch]; ok {
				delete(b.subs, ch)
				close(ch)
			}
			if len(b.subs) == 0 && b.started {
				b.started = false
				stop := b.stop
				b.mu.Unlock()
				stop()
				return
			}
			b.mu.Unlock()
		})
	}
	return ch, cancel
}

// startLocked opens the shared subscription. The caller must hold b.mu.
func (b *RedisBus) startLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	sub := b.client.Subscribe(ctx, b.channel)
	b.stop = func() {
		cancel()
		_ = sub.Close()
	}
	b.started = true

	go func() {
		// go-redis reconnects and re-subscribes underneath Channel(), so a Redis restart does not
		// need handling here — but it does mean events published during the gap are gone. The live
		// stream has never been a durable log; clients refetch over REST.
		ch := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var e Event
				if err := json.Unmarshal([]byte(msg.Payload), &e); err != nil {
					b.logger.Error("live unmarshal", "error", err)
					continue
				}
				b.fanout(e)
			}
		}
	}()
}

// fanout delivers one event to every current subscriber without blocking on a slow one.
//
// The sends happen while holding the read lock, and cancel closes a subscriber's channel only
// under the write lock. That mutual exclusion is what makes this safe: closing a channel another
// goroutine is sending on is a data race whether or not the resulting panic is recovered, and an
// earlier version of this did exactly that. It sent outside the lock and recovered the panic,
// which looked fine and passed -race locally — CI reported it as a race on the first run.
//
// Holding the lock across the sends costs nothing, because every send here is non-blocking: a
// subscriber that is not reading is skipped, never waited for.
func (b *RedisBus) fanout(e Event) {
	var dropped int64

	b.mu.RLock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			dropped++
		}
	}
	subscribers := len(b.subs)
	b.mu.RUnlock()

	if dropped == 0 {
		return
	}
	total := b.dropped.Add(dropped)
	// Logged sparsely and outside the lock: when drops happen there are a great many of them, and
	// a line per dropped event would bury the reason under its own symptom.
	if before := total - dropped; before == 0 || before/1000 != total/1000 {
		b.logger.Warn("live event dropped: a subscriber is not keeping up",
			"dropped_total", total, "subscribers", subscribers)
	}
}

func (b *RedisBus) subscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Dropped reports how many events have been discarded because a subscriber was too slow. Exposed
// so a health endpoint or a test can see the number that is otherwise invisible.
func (b *RedisBus) Dropped() int64 { return b.dropped.Load() }

// Verify RedisBus satisfies the Bus interface.
var _ Bus = (*RedisBus)(nil)
