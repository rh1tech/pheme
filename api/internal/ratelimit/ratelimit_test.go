package ratelimit

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The only throttle on the public ingest endpoint, run against BOTH implementations.
//
// This is what stands between a leaked API key and every subscriber's phone going off a thousand
// times, and it had no tests. Production uses Redis specifically so the limit is shared: with
// per-instance buckets a caller gets the limit multiplied by however many instances are running,
// which is the kind of thing that is fine in staging with one and wrong in production with three.
//
// Redis tests are skipped unless PHEME_TEST_REDIS_ADDR is set:
//
//	docker run -d --rm -p 6379:6379 redis:7-alpine
//	PHEME_TEST_REDIS_ADDR=localhost:6379 go test ./internal/ratelimit/

// eachLimiter runs fn against both implementations, built with the given rate and burst.
func eachLimiter(t *testing.T, ratePerSec, capacity float64, fn func(*testing.T, Limiter)) {
	t.Helper()

	t.Run("memory", func(t *testing.T) { fn(t, NewTokenBucket(ratePerSec, capacity)) })

	addr := os.Getenv("PHEME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Log("PHEME_TEST_REDIS_ADDR not set — skipping the limiter that runs in production")
		return
	}
	t.Run("redis", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = client.Close() })
		// A prefix per RUN. Buckets outlive a test — they expire only once they would have fully
		// refilled — so a prefix keyed on the test name alone would hand the next run a bucket the
		// last one had already drained.
		prefix := fmt.Sprintf("phemetest:rl:%s:%d", t.Name(), time.Now().UnixNano())
		fn(t, NewRedisLimiter(client, ratePerSec, capacity, prefix))
	})
}

// A burst up to capacity is allowed, and the one after it is not. This is the whole contract.
func TestConformance_BurstUpToCapacityThenRefused(t *testing.T) {
	const capacity = 5
	// A rate slow enough that refill cannot rescue the burst mid-test.
	eachLimiter(t, 0.1, capacity, func(t *testing.T, l Limiter) {
		for i := 0; i < capacity; i++ {
			if !l.Allow("channel-1") {
				t.Fatalf("request %d of a burst of %d was refused; the limit is tighter than it claims",
					i+1, capacity)
			}
		}
		if l.Allow("channel-1") {
			t.Error("a request past the burst capacity was allowed; a leaked key can send as fast " +
				"as it likes")
		}
	})
}

// Keys are independent. One channel exhausting its budget must not silence another — a single
// noisy customer would otherwise take the endpoint down for everybody.
func TestConformance_KeysAreIndependent(t *testing.T) {
	const capacity = 3
	eachLimiter(t, 0.1, capacity, func(t *testing.T, l Limiter) {
		for i := 0; i < capacity; i++ {
			if !l.Allow("noisy") {
				t.Fatalf("noisy request %d refused early", i+1)
			}
		}
		if l.Allow("noisy") {
			t.Fatal("noisy was not limited")
		}
		if !l.Allow("quiet") {
			t.Error("one channel exhausting its budget refused another's first request")
		}
	})
}

// The bucket refills. A limiter that drained permanently would turn a brief burst into a channel
// that never works again.
//
// Two per second, and the numbers are chosen so neither half of the test races the other. Draining
// takes three round trips, which at this rate accumulates about a fifth of a token — nowhere near
// the one needed to wrongly allow the request that proves the bucket emptied. Then 800ms refills
// comfortably more than one.
//
// An earlier version ran at 20/sec, where a token appears every 50ms. That is fine on an idle
// machine and fails on a busy one, because the drain itself outlasts the refill interval and the
// bucket is no longer empty by the time the test checks. It failed exactly once, in a full-suite
// run while the rest of the packages were competing for the same Redis — which is the only
// condition under which it could.
func TestConformance_TheBucketRefills(t *testing.T) {
	const capacity = 2
	eachLimiter(t, 2, capacity, func(t *testing.T, l Limiter) {
		for i := 0; i < capacity; i++ {
			if !l.Allow("refilling") {
				t.Fatalf("request %d refused before the burst was used up", i+1)
			}
		}
		if l.Allow("refilling") {
			t.Fatal("the bucket did not empty")
		}

		time.Sleep(800 * time.Millisecond)

		if !l.Allow("refilling") {
			t.Error("the bucket never refilled; one burst would disable the channel permanently")
		}
	})
}

// Refill is capped at capacity. A channel that goes quiet must not bank tokens indefinitely and
// then spend them all at once, which is the thing capacity exists to bound.
//
// Two things this test needed to get right, and got wrong first.
//
// The bucket has to EXIST and be DRAINED before the idle period. Both implementations create a
// bucket at full capacity on first use, so a version that simply slept and then burst was measuring
// a brand-new bucket and never exercised accumulation at all — it passed with the cap deliberately
// removed from both implementations.
//
// And the rate has to be slow enough that refill during the measuring loop is a rounding error. An
// earlier version used 1000/sec, at which a Redis round trip of a few milliseconds refills the
// whole bucket between one request and the next; every request was allowed, and the test called
// that "banked tokens without limit" when it was the limiter doing exactly as configured. The
// in-memory one passed the same test only because its iterations take nanoseconds.
func TestConformance_RefillDoesNotExceedCapacity(t *testing.T) {
	const capacity = 3
	const rate = 10.0
	eachLimiter(t, rate, capacity, func(t *testing.T, l Limiter) {
		// Create the bucket and empty it, so what follows measures accumulation rather than a
		// bucket that started full.
		for i := 0; i < capacity; i++ {
			if !l.Allow("idle") {
				t.Fatalf("request %d refused while draining the fresh bucket", i+1)
			}
		}
		if l.Allow("idle") {
			t.Fatal("the bucket did not empty; the rest of this test would measure nothing")
		}

		// Idle long enough to bank ten tokens if nothing capped it.
		time.Sleep(time.Second)

		allowed := 0
		for i := 0; i < capacity*5; i++ {
			if l.Allow("idle") {
				allowed++
			}
		}
		// A little refill happens during the loop itself, so this cannot demand exactly capacity —
		// but it must be nowhere near the ten an uncapped bucket would have accumulated.
		if allowed > capacity+2 {
			t.Errorf("a bucket left idle allowed %d requests in a burst with capacity %d; a channel "+
				"that goes quiet for a while could then send everything at once", allowed, capacity)
		}
	})
}

// THE ONE THAT MATTERS UNDER LOAD. Concurrent requests must not let more through than the capacity.
//
// A read-modify-write that is not atomic passes every test above — they are all sequential — and
// fails only when requests actually arrive at once, which is the only time the limit matters.
func TestConformance_ConcurrentRequestsRespectTheCapacity(t *testing.T) {
	const capacity = 10
	const attempts = 100
	eachLimiter(t, 0.1, capacity, func(t *testing.T, l Limiter) {
		var allowed atomic.Int64
		var wg sync.WaitGroup
		start := make(chan struct{})

		for i := 0; i < attempts; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Warm the connection before the barrier: go-redis dials on demand, and racers
				// queueing behind connection setup do not actually race.
				l.Allow("warmup")
				<-start
				if l.Allow("concurrent") {
					allowed.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()

		if n := allowed.Load(); n > capacity {
			t.Errorf("%d of %d simultaneous requests were allowed through a capacity of %d; the "+
				"limit does not hold when requests actually arrive together", n, attempts, capacity)
		}
		if allowed.Load() == 0 {
			t.Error("no request was allowed at all; the limiter refuses everything")
		}
	})
}

// The Redis limiter FAILS OPEN. A cache outage must not stop notifications going out — losing the
// limit for a few minutes is a smaller problem than losing delivery.
func TestRedisLimiterFailsOpenWhenRedisIsUnreachable(t *testing.T) {
	// A port nothing is listening on, and a short timeout so the test is not slow.
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer func() { _ = client.Close() }()
	l := NewRedisLimiter(client, 1, 1, "phemetest:rl:down")
	l.timeout = 200 * time.Millisecond

	if !l.Allow("anything") {
		t.Error("the limiter refused a request because Redis was unreachable; a cache outage " +
			"becomes an ingestion outage")
	}
}
