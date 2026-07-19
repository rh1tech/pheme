package live

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The Redis bus against a real Redis.
//
// The property under test is not "events arrive" — a Go channel could do that. It is that a
// thousand connected users cost ONE Redis connection rather than a thousand. The previous
// implementation called client.Subscribe() per subscriber, and a load test at a thousand streams
// found the server burning 75% of its CPU inside net.dialTCP opening those connections, at two
// messages a second. Nothing in the unit tests noticed, because they only ever used one subscriber.
//
// Skipped unless PHEME_TEST_REDIS_ADDR is set:
//
//	docker run -d --rm -p 6379:6379 redis:7-alpine
//	PHEME_TEST_REDIS_ADDR=localhost:6379 go test ./internal/live/

func redisOrSkip(t *testing.T) (*RedisBus, *redis.Client) {
	t.Helper()
	addr := os.Getenv("PHEME_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("PHEME_TEST_REDIS_ADDR not set — skipping the bus that runs in production")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis at %s: %v", addr, err)
	}
	// A channel per test: these are all publishing to the same Redis.
	bus := NewRedisBus(client, fmt.Sprintf("phemetest.live.%s", t.Name()),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = client.Close() })
	return bus, client
}

// pubsubConnections counts the Redis clients currently in subscriber mode. This is the number the
// whole fix is about, read from Redis itself rather than inferred.
func pubsubConnections(t *testing.T, client *redis.Client, channel string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := client.PubSubNumSub(ctx, channel).Result()
	if err != nil {
		t.Fatalf("pubsub numsub: %v", err)
	}
	return int(res[channel])
}

// THE ONE THAT MATTERS. Many subscribers, one Redis subscription.
func TestRedisBusSharesOneSubscriptionAcrossManySubscribers(t *testing.T) {
	bus, client := redisOrSkip(t)

	const subscribers = 50
	cancels := make([]func(), 0, subscribers)
	chans := make([]<-chan Event, 0, subscribers)
	for i := 0; i < subscribers; i++ {
		ch, cancel := bus.Subscribe(fmt.Sprintf("user-%d", i))
		chans = append(chans, ch)
		cancels = append(cancels, cancel)
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()

	// Give the single subscription a moment to register with Redis.
	waitUntil(t, 5*time.Second, func() bool { return pubsubConnections(t, client, bus.channel) >= 1 })

	if n := pubsubConnections(t, client, bus.channel); n != 1 {
		t.Fatalf("%d subscribers opened %d Redis subscriptions, want exactly 1.\n"+
			"One connection per user is what made a thousand streams collapse the server.", subscribers, n)
	}

	// And every one of them still receives.
	bus.Publish(Event{ConversationID: "c1"})
	for i, ch := range chans {
		select {
		case e := <-ch:
			if e.ConversationID != "c1" {
				t.Errorf("subscriber %d got %+v", i, e)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("subscriber %d never received the event", i)
		}
	}
}

// The subscription is opened lazily and released when the last subscriber leaves, so an idle
// process holds no Redis connection.
func TestRedisBusOpensAndClosesTheSubscriptionWithItsSubscribers(t *testing.T) {
	bus, client := redisOrSkip(t)

	if n := pubsubConnections(t, client, bus.channel); n != 0 {
		t.Fatalf("an unused bus already holds %d subscriptions", n)
	}

	first, cancelFirst := bus.Subscribe("first")
	_ = first
	waitUntil(t, 5*time.Second, func() bool { return pubsubConnections(t, client, bus.channel) == 1 })

	second, cancelSecond := bus.Subscribe("second")
	_ = second
	if n := pubsubConnections(t, client, bus.channel); n != 1 {
		t.Errorf("a second subscriber opened another subscription (%d)", n)
	}

	cancelFirst()
	if n := pubsubConnections(t, client, bus.channel); n != 1 {
		t.Errorf("the subscription closed while a subscriber remained (%d); that subscriber would "+
			"go silent", n)
	}

	cancelSecond()
	if !waitUntil(t, 5*time.Second, func() bool { return pubsubConnections(t, client, bus.channel) == 0 }) {
		t.Error("the last subscriber left but the Redis subscription stayed open")
	}

	// And it comes back for a new subscriber, rather than being a one-shot.
	third, cancelThird := bus.Subscribe("third")
	defer cancelThird()
	waitUntil(t, 5*time.Second, func() bool { return pubsubConnections(t, client, bus.channel) == 1 })
	bus.Publish(Event{ConversationID: "after-restart", Recipients: []string{"third"}})
	select {
	case e := <-third:
		if e.ConversationID != "after-restart" {
			t.Errorf("got %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Error("the bus did not resubscribe after going idle; every stream after the first quiet " +
			"moment would receive nothing")
	}
}

// Cancelling while events are in flight must not race. Delivery sends under a read lock and cancel
// closes under the write lock; an earlier version sent outside the lock and recovered the panic,
// which CI correctly reported as a data race.
func TestRedisBusCancelDuringPublishDoesNotPanic(t *testing.T) {
	bus, _ := redisOrSkip(t)

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		ch, cancel := bus.Subscribe(fmt.Sprintf("churn-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Read a little, then leave mid-stream.
			for j := 0; j < 3; j++ {
				select {
				case <-ch:
				case <-time.After(200 * time.Millisecond):
				}
			}
			cancel()
			cancel() // cancelling twice must also be safe
		}()
	}
	for i := 0; i < 50; i++ {
		bus.Publish(Event{ConversationID: fmt.Sprintf("c%d", i)})
	}
	wg.Wait()
}

// A subscriber that stops reading must not starve its neighbours, and its losses must be COUNTED.
//
// Two things this test learned the hard way about what it can and cannot assert.
//
// It must not demand that the healthy subscriber receives EVERY event. This bus is deliberately
// lossy — subscriberBuffer is "how many events a subscriber may fall behind by before its events
// start being dropped" — so under a burst any reader can lose some, including a healthy one that is
// merely a moment behind the publisher. The first version demanded all 192 and duly failed in CI at
// 174, which was the test asserting a guarantee the bus does not make rather than the bus
// misbehaving.
//
// Nor can it watch Publish for signs of blocking. On the Redis bus Publish only writes to Redis;
// delivery happens later on the subscription goroutine, so Publish returns promptly however wedged
// the subscribers are. A second version asserted that and passed against a deliberately blocking
// send, which is worse than the first: a test that cannot fail.
//
// And a third mistake, which CI caught and which is the first one wearing a different hat: the
// healthy subscriber must actually READ. A version that published all 192 events and only then
// started reading left both subscribers stalled — its buffer filled at 64 and the rest were
// dropped, exactly as designed — and then failed for not seeing the last event. Demanding a
// specific event of a reader that was not reading is the same error as demanding all of them.
//
// What separates a working bus from a stalled one is whether a continuously-draining subscriber
// keeps receiving past the point where its wedged neighbour filled up. A delivery loop that waited
// on the wedged reader would stop dead at one buffer's worth; a working one goes well beyond it.
// That is the assertion, and it does not depend on any single event surviving.
func TestRedisBusCountsWhatItDropsForASlowSubscriber(t *testing.T) {
	bus, _ := redisOrSkip(t)

	stalled, cancelStalled := bus.Subscribe("stalled") // never read from
	defer cancelStalled()
	healthy, cancelHealthy := bus.Subscribe("healthy")
	defer cancelHealthy()

	// The healthy subscriber drains from the start, which is what makes it healthy.
	var received atomic.Int64
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for range healthy {
			received.Add(1)
		}
	}()

	// Comfortably more than one buffer's worth, so the stalled subscriber is over-full long before
	// the end.
	const sent = subscriberBuffer * 3
	for i := 0; i < sent; i++ {
		bus.Publish(Event{
			ConversationID: fmt.Sprintf("c%d", i),
			Recipients:     []string{"stalled", "healthy"},
		})
	}

	// Past one buffer's worth is the whole claim: delivery continued after the wedged subscriber
	// stopped accepting. A blocking send caps this at exactly subscriberBuffer and no more.
	if !waitUntil(t, 20*time.Second, func() bool { return received.Load() > int64(subscriberBuffer) }) {
		t.Fatalf("the healthy subscriber received %d events and got no further than one buffer's "+
			"worth (%d); one reader that stopped reading has stopped delivery to everyone else",
			received.Load(), subscriberBuffer)
	}

	// And the losses are visible. A drop is a message a user never sees, with nothing in any HTTP
	// response to say so, so it is at least countable.
	if !waitUntil(t, 5*time.Second, func() bool { return bus.Dropped() > 0 }) {
		t.Error("a subscriber that never read lost events but Dropped() stayed at 0; the loss is " +
			"invisible to operators")
	}
	_ = stalled
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return cond()
}
