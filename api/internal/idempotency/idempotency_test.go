package idempotency

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Remembering that a request has already been made, run against BOTH implementations.
//
// Production uses Redis and the tests would otherwise use the map, which is precisely the wrong way
// round for this: the whole point is that every instance shares one view. A per-instance memory
// deduplicates only the retries that happen to land on the same instance, which is worse than no
// deduplication at all, because it looks like it works.
//
// Redis tests are skipped unless PHEME_TEST_REDIS_ADDR is set:
//
//	docker run -d --rm -p 6379:6379 redis:7-alpine
//	PHEME_TEST_REDIS_ADDR=localhost:6379 go test ./internal/idempotency/

func eachStore(t *testing.T, fn func(*testing.T, Store)) {
	t.Helper()

	t.Run("memory", func(t *testing.T) { fn(t, NewMemory()) })

	addr := os.Getenv("PHEME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Log("PHEME_TEST_REDIS_ADDR not set — skipping the implementation that runs in production")
		return
	}
	t.Run("redis", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{Addr: addr})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			t.Fatalf("ping redis: %v", err)
		}
		// A prefix per RUN, not merely per test. Keys live for the window they were written with —
		// up to a day — so a prefix keyed only on the test name means the second run of the suite
		// against the same Redis finds the first run's keys and reports every request as a
		// duplicate. The test then fails for reasons that have nothing to do with the code, which
		// is how it failed the first time I ran it twice.
		prefix := fmt.Sprintf("phemetest:idem:%s:%d:", t.Name(), time.Now().UnixNano())
		t.Cleanup(func() {
			clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if keys, err := client.Keys(clean, prefix+"*").Result(); err == nil && len(keys) > 0 {
				_ = client.Del(clean, keys...).Err()
			}
			_ = client.Close()
		})
		fn(t, NewRedis(client, prefix))
	})
}

func TestConformance_FirstUseIsNew(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		seen, err := s.Seen(context.Background(), "order-1", Window)
		if err != nil {
			t.Fatalf("seen: %v", err)
		}
		if seen {
			t.Error("a key that has never been used was reported as already seen; the first request " +
				"would be silently discarded")
		}
	})
}

func TestConformance_SecondUseIsSeen(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		if _, err := s.Seen(ctx, "order-2", Window); err != nil {
			t.Fatalf("first: %v", err)
		}
		seen, err := s.Seen(ctx, "order-2", Window)
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if !seen {
			t.Error("a repeated key was reported as new; a client retrying a request that timed out " +
				"sends the notification to every subscriber twice")
		}
	})
}

// Different keys are different requests. A store that collided them would silently swallow real
// notifications, which is far worse than the duplicate it is trying to prevent.
func TestConformance_DistinctKeysAreIndependent(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		for i := 0; i < 5; i++ {
			seen, err := s.Seen(ctx, fmt.Sprintf("distinct-%d", i), Window)
			if err != nil {
				t.Fatalf("seen %d: %v", i, err)
			}
			if seen {
				t.Errorf("key distinct-%d collided with another key; a real notification is dropped", i)
			}
		}
	})
}

// The window expires. A key reused next week is a new request, not a duplicate — otherwise a
// caller with a stable key ("nightly-report") would send exactly one notification, ever.
func TestConformance_TheWindowExpires(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		const short = 300 * time.Millisecond

		if seen, err := s.Seen(ctx, "expiring", short); err != nil || seen {
			t.Fatalf("first: seen=%v err=%v", seen, err)
		}
		if seen, err := s.Seen(ctx, "expiring", short); err != nil || !seen {
			t.Fatalf("immediately after: seen=%v err=%v, want it remembered", seen, err)
		}

		time.Sleep(short + 400*time.Millisecond)

		seen, err := s.Seen(ctx, "expiring", short)
		if err != nil {
			t.Fatalf("after the window: %v", err)
		}
		if seen {
			t.Error("a key was still remembered after its window; a caller reusing a stable key " +
				"would send one notification and never another")
		}
	})
}

// THE ONE THAT MATTERS. Two copies of the same request arriving at once — which is exactly how
// duplicates arrive — must produce exactly one "new".
//
// A check-then-write would pass every test above and fail this one, and it would fail it only under
// the concurrency that real retries have.
func TestConformance_SimultaneousDuplicatesYieldOneNew(t *testing.T) {
	eachStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		const racers = 16

		var newCount atomic.Int64
		var wg sync.WaitGroup
		errs := make(chan error, racers)
		start := make(chan struct{})

		// Each racer touches the store on a key of its own BEFORE the barrier.
		//
		// Without this the test does not race. go-redis opens connections on demand, so the first
		// goroutine gets one at once and the rest queue behind connection dials — which serialises
		// them by more than the window a non-atomic implementation leaves open. Verified: with a
		// deliberately non-atomic check-then-write, and even a millisecond of sleep wedged between
		// the check and the write, this test still reported exactly one winner until the pool was
		// warmed. It was a race test that could not observe a race.
		var warm sync.WaitGroup
		for i := 0; i < racers; i++ {
			warm.Add(1)
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if _, err := s.Seen(ctx, fmt.Sprintf("warmup-%d", i), Window); err != nil {
					errs <- err
					warm.Done()
					return
				}
				warm.Done()
				<-start // now all at once, with a connection each already in hand
				seen, err := s.Seen(ctx, "thundering", Window)
				if err != nil {
					errs <- err
					return
				}
				if !seen {
					newCount.Add(1)
				}
			}(i)
		}
		warm.Wait()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("seen: %v", err)
		}

		if n := newCount.Load(); n != 1 {
			t.Errorf("%d of %d simultaneous copies of one request were each told they were new; "+
				"that many notifications go out", n, racers)
		}
	})
}
