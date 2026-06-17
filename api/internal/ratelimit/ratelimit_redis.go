package ratelimit

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisTokenBucket is an atomic token-bucket implemented as a Lua script so that
// the read-modify-write is performed entirely on the Redis server, making limits
// consistent across multiple API instances.
//
// KEYS[1] = bucket key
// ARGV[1] = rate (tokens/sec), ARGV[2] = capacity,
// ARGV[3] = now (unix seconds, float), ARGV[4] = requested tokens
// Returns 1 if allowed, 0 otherwise.
var redisTokenBucket = redis.NewScript(`
local rate = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])

local data = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then
  tokens = capacity
  ts = now
end

local delta = math.max(0, now - ts)
tokens = math.min(capacity, tokens + delta * rate)

local allowed = 0
if tokens >= requested then
  tokens = tokens - requested
  allowed = 1
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', now)
-- Expire idle buckets after they would fully refill.
local ttl = math.ceil(capacity / rate) + 1
redis.call('EXPIRE', KEYS[1], ttl)
return allowed
`)

// RedisLimiter is a distributed token-bucket Limiter backed by Redis.
type RedisLimiter struct {
	client   *redis.Client
	rate     float64
	capacity float64
	prefix   string
	timeout  time.Duration
}

// NewRedisLimiter creates a limiter refilling at ratePerSec with the given burst
// capacity. Keys are namespaced with prefix.
func NewRedisLimiter(client *redis.Client, ratePerSec, capacity float64, prefix string) *RedisLimiter {
	return &RedisLimiter{
		client:   client,
		rate:     ratePerSec,
		capacity: capacity,
		prefix:   prefix,
		timeout:  500 * time.Millisecond,
	}
}

// Allow consumes one token for key. On a Redis error it fails open (allows the
// request) so a cache outage does not take down ingestion.
func (l *RedisLimiter) Allow(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()

	now := float64(time.Now().UnixNano()) / 1e9
	res, err := redisTokenBucket.Run(ctx, l.client,
		[]string{l.prefix + ":" + key},
		l.rate, l.capacity, now, 1,
	).Int()
	if err != nil {
		return true // fail open
	}
	return res == 1
}
