package idempotency

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is a Store shared by every API instance.
//
// Sharing it is the point. A retry does not come back to the instance that handled the original —
// it hits whichever one the load balancer picks — so per-instance memory would call the duplicate
// new and send the notification again, which is the exact failure this prevents.
type Redis struct {
	client *redis.Client
	prefix string
}

// NewRedis returns a Store backed by the given client. Keys are namespaced so they cannot collide
// with the rate limiter's buckets or the call mailbox, which share this database.
func NewRedis(client *redis.Client, prefix string) *Redis {
	if prefix == "" {
		prefix = "idem:"
	}
	return &Redis{client: client, prefix: prefix}
}

// Seen records the key and reports whether it was already there.
//
// SET NX is what makes this correct rather than approximately correct: the check and the write are
// one round trip that Redis performs atomically, so two copies of the same request arriving at two
// instances in the same millisecond cannot both be told they are new. A GET followed by a SET would
// have exactly that race, and it would show up only under the concurrency that duplicates actually
// arrive with.
func (r *Redis) Seen(ctx context.Context, key string, window time.Duration) (bool, error) {
	stored, err := r.client.SetNX(ctx, r.prefix+key, "1", window).Result()
	if err != nil {
		return false, err
	}
	// SetNX reports whether it stored. Stored means nobody had this key: not seen before.
	return !stored, nil
}

var _ Store = (*Redis)(nil)
