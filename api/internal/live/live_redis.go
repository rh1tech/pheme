package live

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// RedisBus is a Bus backed by Redis pub/sub, so live events published by the
// Dispatcher reach SSE/WebSocket subscribers on any App API instance.
type RedisBus struct {
	client  *redis.Client
	channel string
	logger  *slog.Logger
}

// NewRedisBus creates a Redis-backed bus publishing on the given channel.
func NewRedisBus(client *redis.Client, channel string, logger *slog.Logger) *RedisBus {
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisBus{client: client, channel: channel, logger: logger}
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

// Subscribe returns a channel of events and a cancel function. It bridges the
// Redis subscription onto a Go channel so callers use the same Bus interface as
// the in-process implementation.
func (b *RedisBus) Subscribe() (<-chan Event, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	sub := b.client.Subscribe(ctx, b.channel)
	out := make(chan Event, 16)

	go func() {
		defer close(out)
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
				select {
				case out <- e:
				default: // drop for slow consumers
				}
			}
		}
	}()

	stop := func() {
		cancel()
		_ = sub.Close()
	}
	return out, stop
}

// Verify RedisBus satisfies the Bus interface.
var _ Bus = (*RedisBus)(nil)
