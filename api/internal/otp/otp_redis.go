package otp

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is a Store backed by Redis so verification state is shared across API
// instances. Pending signups/resets are stored as hashes with a TTL; cooldowns
// use SET NX EX for an atomic once-per-window check.
type Redis struct {
	client *redis.Client
	prefix string
}

// NewRedis returns a Redis-backed Store. Keys are namespaced under prefix.
func NewRedis(client *redis.Client, prefix string) *Redis {
	if prefix == "" {
		prefix = "pheme:otp"
	}
	return &Redis{client: client, prefix: prefix}
}

func (r *Redis) signupKey(email string) string { return r.prefix + ":signup:" + email }
func (r *Redis) resetKey(email string) string  { return r.prefix + ":reset:" + email }
func (r *Redis) cdKey(key string) string       { return r.prefix + ":cd:" + key }

func (r *Redis) PutSignup(ctx context.Context, s Signup, ttl time.Duration) error {
	key := r.signupKey(s.Email)
	pipe := r.client.TxPipeline()
	pipe.Del(ctx, key)
	pipe.HSet(ctx, key,
		"email", s.Email,
		"passwordHash", s.PasswordHash,
		"codeHash", s.CodeHash,
		"attempts", s.Attempts,
		"inviteId", s.InviteID,
	)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) GetSignup(ctx context.Context, email string) (Signup, error) {
	res, err := r.client.HGetAll(ctx, r.signupKey(email)).Result()
	if err != nil {
		return Signup{}, err
	}
	if len(res) == 0 {
		return Signup{}, ErrNotFound
	}
	attempts, _ := strconv.Atoi(res["attempts"])
	return Signup{
		Email:        res["email"],
		PasswordHash: res["passwordHash"],
		CodeHash:     res["codeHash"],
		Attempts:     attempts,
		InviteID:     res["inviteId"],
	}, nil
}

func (r *Redis) IncrSignupAttempts(ctx context.Context, email string) (int, error) {
	key := r.signupKey(email)
	exists, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, ErrNotFound
	}
	n, err := r.client.HIncrBy(ctx, key, "attempts", 1).Result()
	return int(n), err
}

func (r *Redis) DelSignup(ctx context.Context, email string) error {
	return r.client.Del(ctx, r.signupKey(email)).Err()
}

func (r *Redis) PutReset(ctx context.Context, rst Reset, ttl time.Duration) error {
	key := r.resetKey(rst.Email)
	pipe := r.client.TxPipeline()
	pipe.Del(ctx, key)
	pipe.HSet(ctx, key,
		"email", rst.Email,
		"userId", rst.UserID,
		"codeHash", rst.CodeHash,
		"attempts", rst.Attempts,
	)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) GetReset(ctx context.Context, email string) (Reset, error) {
	res, err := r.client.HGetAll(ctx, r.resetKey(email)).Result()
	if err != nil {
		return Reset{}, err
	}
	if len(res) == 0 {
		return Reset{}, ErrNotFound
	}
	attempts, _ := strconv.Atoi(res["attempts"])
	return Reset{
		Email:    res["email"],
		UserID:   res["userId"],
		CodeHash: res["codeHash"],
		Attempts: attempts,
	}, nil
}

func (r *Redis) IncrResetAttempts(ctx context.Context, email string) (int, error) {
	key := r.resetKey(email)
	exists, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, ErrNotFound
	}
	n, err := r.client.HIncrBy(ctx, key, "attempts", 1).Result()
	return int(n), err
}

func (r *Redis) DelReset(ctx context.Context, email string) error {
	return r.client.Del(ctx, r.resetKey(email)).Err()
}

func (r *Redis) CooldownOK(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return r.client.SetNX(ctx, r.cdKey(key), "1", ttl).Result()
}
