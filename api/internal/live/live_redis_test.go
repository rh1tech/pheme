package live

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
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
		ch, cancel := bus.Subscribe()
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

	first, cancelFirst := bus.Subscribe()
	_ = first
	waitUntil(t, 5*time.Second, func() bool { return pubsubConnections(t, client, bus.channel) == 1 })

	second, cancelSecond := bus.Subscribe()
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
	third, cancelThird := bus.Subscribe()
	defer cancelThird()
	waitUntil(t, 5*time.Second, func() bool { return pubsubConnections(t, client, bus.channel) == 1 })
	bus.Publish(Event{ConversationID: "after-restart"})
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

// Cancelling while events are in flight must not panic. fanout sends outside the lock, so a
// subscriber can close its channel between the snapshot and the send — the race this guards.
func TestRedisBusCancelDuringPublishDoesNotPanic(t *testing.T) {
	bus, _ := redisOrSkip(t)

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		ch, cancel := bus.Subscribe()
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

// A subscriber that stops reading must not stall anyone else, and its losses must be COUNTED. A
// silent drop is a message the user never sees with nothing anywhere to say so.
func TestRedisBusCountsWhatItDropsForASlowSubscriber(t *testing.T) {
	bus, _ := redisOrSkip(t)

	stalled, cancelStalled := bus.Subscribe() // never read from
	defer cancelStalled()
	healthy, cancelHealthy := bus.Subscribe()
	defer cancelHealthy()

	// Comfortably more than one buffer's worth.
	const sent = subscriberBuffer * 3
	go func() {
		for i := 0; i < sent; i++ {
			bus.Publish(Event{ConversationID: fmt.Sprintf("c%d", i)})
		}
	}()

	// The healthy subscriber keeps up throughout, despite its neighbour being wedged.
	received := 0
	deadline := time.After(15 * time.Second)
	for received < sent {
		select {
		case <-healthy:
			received++
		case <-deadline:
			t.Fatalf("the healthy subscriber received only %d of %d events; one stalled reader "+
				"stalled the bus", received, sent)
		}
	}

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
