package live

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

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
// connected user, and the registry then routes it to the subscribers it is actually for.
type RedisBus struct {
	client  *redis.Client
	channel string
	logger  *slog.Logger
	reg     *registry

	mu      sync.Mutex
	started bool
	stop    context.CancelFunc
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
		reg:     newRegistry(logger),
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

// Subscribe registers a subscriber for userID on the shared Redis subscription and returns its
// channel plus a cancel function. The first subscriber starts the subscription; the last one to
// leave stops it, so an idle process holds no Redis connection.
func (b *RedisBus) Subscribe(userID string) (<-chan Event, func()) {
	return b.reg.add(userID, b.start, b.shutdown)
}

// start opens the shared subscription. The registry calls it while holding its own lock when the
// first subscriber arrives, so this takes only b.mu and must never call back into the registry.
func (b *RedisBus) start() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return
	}
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
				b.reg.deliver(e)
			}
		}
	}()
}

// shutdown closes the shared subscription once the last subscriber has gone.
func (b *RedisBus) shutdown() {
	b.mu.Lock()
	stop := b.stop
	b.started = false
	b.stop = nil
	b.mu.Unlock()

	if stop != nil {
		stop()
	}
}

// Dropped reports how many deliveries were discarded because a subscriber was too slow.
func (b *RedisBus) Dropped() int64 { return b.reg.Dropped() }

// Verify RedisBus satisfies the Bus interface.
var _ Bus = (*RedisBus)(nil)
