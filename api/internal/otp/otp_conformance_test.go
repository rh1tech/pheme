package otp

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// One set of assertions for both OTP stores, for the same reason the store package has one:
// production runs the Redis implementation and the tests exercised the in-memory one.
//
// This is the code that guards signup and password reset. A store that forgets an attempt counter,
// or lets a cooldown be claimed twice, is the difference between a rate limit and the appearance of
// one.
//
// The Redis half is skipped unless PHEME_TEST_REDIS_ADDR is set:
//
//	docker run -d --rm -p 6479:6379 redis:7-alpine
//	PHEME_TEST_REDIS_ADDR=localhost:6479 go test ./internal/otp/

func eachOTPStore(t *testing.T, fn func(t *testing.T, s Store)) {
	t.Helper()

	t.Run("memory", func(t *testing.T) { fn(t, NewMemory()) })

	addr := os.Getenv("PHEME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Log("PHEME_TEST_REDIS_ADDR not set — skipping the implementation that runs in production")
		return
	}
	t.Run("redis", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{Addr: addr})
		if err := client.Ping(context.Background()).Err(); err != nil {
			t.Fatalf("connect to redis: %v", err)
		}
		// A prefix per test, so tests cannot see each other's keys on a shared server.
		prefix := "phemetest:" + t.Name()
		t.Cleanup(func() {
			keys, _ := client.Keys(context.Background(), prefix+"*").Result()
			if len(keys) > 0 {
				_ = client.Del(context.Background(), keys...).Err()
			}
			_ = client.Close()
		})
		fn(t, NewRedis(client, prefix))
	})
}

func TestOTPConformance_SignupRoundTrip(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		want := Signup{Email: "new@pheme.test", PasswordHash: "hash", CodeHash: "codehash"}

		if err := s.PutSignup(ctx, want, time.Minute); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.GetSignup(ctx, want.Email)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Email != want.Email || got.PasswordHash != want.PasswordHash || got.CodeHash != want.CodeHash {
			t.Errorf("round trip = %+v, want %+v", got, want)
		}
		if got.Attempts != 0 {
			t.Errorf("a fresh signup starts with %d attempts, want 0", got.Attempts)
		}
	})
}

func TestOTPConformance_MissingSignupIsNotFound(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		// Not a zero Signup with a nil error: a caller that took that as real would verify a code
		// against an empty hash.
		if _, err := s.GetSignup(ctx, "nobody@pheme.test"); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetSignup for a stranger = %v, want ErrNotFound", err)
		}
		if _, err := s.IncrSignupAttempts(ctx, "nobody@pheme.test"); !errors.Is(err, ErrNotFound) {
			t.Errorf("IncrSignupAttempts for a stranger = %v, want ErrNotFound", err)
		}
	})
}

// The attempt counter is the whole rate limit on a six-digit code. If it does not survive being
// read back, a code can be guessed indefinitely.
func TestOTPConformance_SignupAttemptsAccumulate(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		if err := s.PutSignup(ctx, Signup{Email: "counter@pheme.test", CodeHash: "c"}, time.Minute); err != nil {
			t.Fatalf("put: %v", err)
		}

		for want := 1; want <= 3; want++ {
			got, err := s.IncrSignupAttempts(ctx, "counter@pheme.test")
			if err != nil {
				t.Fatalf("incr: %v", err)
			}
			if got != want {
				t.Fatalf("attempt %d reported as %d", want, got)
			}
		}
		// And it is visible to the next reader, not just to the incrementer.
		after, err := s.GetSignup(ctx, "counter@pheme.test")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if after.Attempts != 3 {
			t.Errorf("attempts read back as %d, want 3", after.Attempts)
		}
	})
}

// Re-issuing a code must not inherit the previous attempt count, or asking for a new code would
// arrive pre-exhausted.
func TestOTPConformance_ReissuingASignupResetsAttempts(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		email := "reissue@pheme.test"
		if err := s.PutSignup(ctx, Signup{Email: email, CodeHash: "first"}, time.Minute); err != nil {
			t.Fatalf("put: %v", err)
		}
		if _, err := s.IncrSignupAttempts(ctx, email); err != nil {
			t.Fatalf("incr: %v", err)
		}
		if err := s.PutSignup(ctx, Signup{Email: email, CodeHash: "second"}, time.Minute); err != nil {
			t.Fatalf("re-put: %v", err)
		}

		got, err := s.GetSignup(ctx, email)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Attempts != 0 {
			t.Errorf("a re-issued code carried %d attempts over, want 0", got.Attempts)
		}
		if got.CodeHash != "second" {
			t.Errorf("codeHash = %q, want the new code", got.CodeHash)
		}
	})
}

func TestOTPConformance_DeletingASignupForgetsIt(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		email := "gone@pheme.test"
		if err := s.PutSignup(ctx, Signup{Email: email, CodeHash: "c"}, time.Minute); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := s.DelSignup(ctx, email); err != nil {
			t.Fatalf("del: %v", err)
		}
		if _, err := s.GetSignup(ctx, email); !errors.Is(err, ErrNotFound) {
			t.Errorf("a deleted signup is still readable: %v", err)
		}
		// Deleting again is not an error — a retry must not fail.
		if err := s.DelSignup(ctx, email); err != nil {
			t.Errorf("deleting twice = %v, want nil", err)
		}
	})
}

func TestOTPConformance_ResetRoundTripAndAttempts(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		email := "reset@pheme.test"

		if _, err := s.GetReset(ctx, email); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetReset for a stranger = %v, want ErrNotFound", err)
		}
		if err := s.PutReset(ctx, Reset{Email: email, CodeHash: "rc"}, time.Minute); err != nil {
			t.Fatalf("put: %v", err)
		}
		got, err := s.GetReset(ctx, email)
		if err != nil || got.CodeHash != "rc" {
			t.Fatalf("GetReset = %+v, %v", got, err)
		}
		n, err := s.IncrResetAttempts(ctx, email)
		if err != nil || n != 1 {
			t.Fatalf("IncrResetAttempts = %d, %v", n, err)
		}
		if err := s.DelReset(ctx, email); err != nil {
			t.Fatalf("del: %v", err)
		}
		if _, err := s.GetReset(ctx, email); !errors.Is(err, ErrNotFound) {
			t.Errorf("a deleted reset is still readable: %v", err)
		}
	})
}

// Signup and reset are separate namespaces. A pending reset must not satisfy a signup lookup, or
// one flow could consume the other's code.
func TestOTPConformance_SignupAndResetDoNotCollide(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		email := "both@pheme.test"

		if err := s.PutSignup(ctx, Signup{Email: email, CodeHash: "signup-code"}, time.Minute); err != nil {
			t.Fatalf("put signup: %v", err)
		}
		if err := s.PutReset(ctx, Reset{Email: email, CodeHash: "reset-code"}, time.Minute); err != nil {
			t.Fatalf("put reset: %v", err)
		}

		gotSignup, err := s.GetSignup(ctx, email)
		if err != nil {
			t.Fatalf("get signup: %v", err)
		}
		gotReset, err := s.GetReset(ctx, email)
		if err != nil {
			t.Fatalf("get reset: %v", err)
		}
		if gotSignup.CodeHash != "signup-code" || gotReset.CodeHash != "reset-code" {
			t.Errorf("the two flows share storage: signup=%q reset=%q", gotSignup.CodeHash, gotReset.CodeHash)
		}
		// Deleting one must leave the other.
		if err := s.DelSignup(ctx, email); err != nil {
			t.Fatalf("del signup: %v", err)
		}
		if _, err := s.GetReset(ctx, email); err != nil {
			t.Errorf("deleting the signup also deleted the reset: %v", err)
		}
	})
}

// The cooldown is what stops somebody asking for a code in a loop. It has to be atomic: two callers
// racing must not both be told to go ahead.
func TestOTPConformance_CooldownAllowsOnceThenRefuses(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()

		ok, err := s.CooldownOK(ctx, "send:cool@pheme.test", time.Minute)
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		if !ok {
			t.Fatal("the first request in a cooldown window was refused")
		}

		ok, err = s.CooldownOK(ctx, "send:cool@pheme.test", time.Minute)
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if ok {
			t.Error("a second request inside the window was allowed; the cooldown does nothing")
		}

		// A different key is a different window.
		ok, err = s.CooldownOK(ctx, "send:other@pheme.test", time.Minute)
		if err != nil || !ok {
			t.Errorf("an unrelated key was caught by another key's cooldown: %v, %v", ok, err)
		}
	})
}

// A cooldown that never lets go is a lockout. This uses a very short TTL rather than waiting a
// realistic one.
func TestOTPConformance_CooldownExpires(t *testing.T) {
	eachOTPStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		key := "send:expiring@pheme.test"

		if ok, err := s.CooldownOK(ctx, key, 50*time.Millisecond); err != nil || !ok {
			t.Fatalf("first = %v, %v", ok, err)
		}
		if ok, _ := s.CooldownOK(ctx, key, 50*time.Millisecond); ok {
			t.Fatal("the window did not hold at all")
		}

		time.Sleep(150 * time.Millisecond)

		ok, err := s.CooldownOK(ctx, key, 50*time.Millisecond)
		if err != nil {
			t.Fatalf("after expiry: %v", err)
		}
		if !ok {
			t.Error("the cooldown never expired; a user is locked out permanently")
		}
	})
}
