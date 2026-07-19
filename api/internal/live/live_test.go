package live

import (
	"encoding/json"
	"testing"
)

// The Redis bus moves an Event between processes by marshalling it to JSON
// (RedisBus.Publish) and unmarshalling it on the other side (RedisBus.Subscribe). Any
// field that does not survive that round trip does not exist in production, however
// well it works in the tests — which run on the in-memory bus, where the struct is
// passed by value and never encoded at all.
//
// Recipients is the field that matters. It authorises delivery of an event whose usual
// membership check CANNOT pass, because the membership rows are already gone: a
// conversation deletion. When it was tagged `json:"-"`, the Redis bus silently dropped
// it, every subscriber saw nil, the SSE handler fell through to the membership check,
// found nothing, and skipped the event.
//
// The visible symptom was that deleting a conversation notified nobody in production,
// while working perfectly in dev. This test is the thing that would have caught it.
func TestRecipientsSurviveTheRedisRoundTrip(t *testing.T) {
	sent := Event{
		ConversationID:      "conv-1",
		ConversationDeleted: true,
		Recipients:          []string{"alice", "bob"},
	}

	payload, err := json.Marshal(sent)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Event
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Recipients) != 2 || got.Recipients[0] != "alice" || got.Recipients[1] != "bob" {
		t.Fatalf("recipients did not survive the wire: %#v — a conversation deletion "+
			"would reach nobody in production", got.Recipients)
	}
	if !got.ConversationDeleted || got.ConversationID != "conv-1" {
		t.Fatalf("event did not survive the wire: %#v", got)
	}
}

// Every subscriber's buffer receives every event, so a slow consumer must never block
// the publisher. It is dropped instead — which is why anything that cannot tolerate
// loss (call signalling) needs its own at-least-once channel rather than riding this bus.
func TestPublishDoesNotBlockOnASlowSubscriber(t *testing.T) {
	bus := NewMemoryBus()
	events, cancel := bus.Subscribe("u1")
	defer cancel()

	// Far more than the 16-slot buffer, with nobody draining it.
	for i := 0; i < 100; i++ {
		bus.Publish(Event{ChannelID: "c"})
	}

	// The subscriber still holds a full buffer rather than a wedged publisher.
	if len(events) == 0 {
		t.Fatal("expected the subscriber to have buffered events")
	}
}

// A subscriber that has cancelled must stop receiving, and must not wedge the bus.
func TestCancelUnsubscribes(t *testing.T) {
	bus := NewMemoryBus()
	events, cancel := bus.Subscribe("u1")
	cancel()

	bus.Publish(Event{ChannelID: "c"})

	if _, open := <-events; open {
		t.Fatal("a cancelled subscriber must not receive events")
	}
}

// Routing. An event that names its recipients must reach exactly them.
//
// This is what keeps delivery proportional to the conversation rather than to the number of people
// signed in. Before it, one message was offered to every open connection on the instance and each
// one worked out for itself that the message was not for it — at a thousand streams, a ten-person
// conversation woke a thousand goroutines to deliver ten events.
//
// It is also an authorisation boundary in practice, even though the stream re-checks: a bug here
// would hand one conversation's traffic to someone outside it.
func TestAnAddressedEventReachesOnlyTheNamedUsers(t *testing.T) {
	bus := NewMemoryBus()

	alice, cancelA := bus.Subscribe("alice")
	defer cancelA()
	bob, cancelB := bus.Subscribe("bob")
	defer cancelB()
	stranger, cancelS := bus.Subscribe("stranger")
	defer cancelS()

	bus.Publish(Event{ConversationID: "conv1", Recipients: []string{"alice", "bob"}})

	if len(alice) != 1 {
		t.Errorf("alice was named but received %d events", len(alice))
	}
	if len(bob) != 1 {
		t.Errorf("bob was named but received %d events", len(bob))
	}
	if len(stranger) != 0 {
		t.Errorf("a user who was not named received %d events from a conversation they are not in",
			len(stranger))
	}
}

// Every one of a user's streams gets it — that is what keeps a second device in sync.
func TestAnAddressedEventReachesAllOfAUsersStreams(t *testing.T) {
	bus := NewMemoryBus()

	phone, cancelPhone := bus.Subscribe("alice")
	defer cancelPhone()
	laptop, cancelLaptop := bus.Subscribe("alice")
	defer cancelLaptop()

	bus.Publish(Event{ConversationID: "conv1", Recipients: []string{"alice"}})

	if len(phone) != 1 || len(laptop) != 1 {
		t.Errorf("a user's two devices received %d and %d events, want 1 each; one of their devices "+
			"would be silently out of date", len(phone), len(laptop))
	}
}

// Closing one device's stream must not disturb the other's.
func TestClosingOneStreamLeavesTheUsersOthersAlone(t *testing.T) {
	bus := NewMemoryBus()

	phone, cancelPhone := bus.Subscribe("alice")
	laptop, cancelLaptop := bus.Subscribe("alice")
	defer cancelLaptop()

	cancelPhone()
	bus.Publish(Event{ConversationID: "conv1", Recipients: []string{"alice"}})

	if len(laptop) != 1 {
		t.Errorf("closing one device's stream cost the other its events (%d)", len(laptop))
	}
	if _, open := <-phone; open {
		t.Error("the cancelled stream is still open")
	}
}

// An event with NO recipients is broadcast, and the receiving stream authorises it for itself.
// Channel broadcasts take this path, as does a chat event whose roster could not be read — so
// losing it would mean losing channel messages entirely.
func TestAnUnaddressedEventStillReachesEveryone(t *testing.T) {
	bus := NewMemoryBus()

	alice, cancelA := bus.Subscribe("alice")
	defer cancelA()
	bob, cancelB := bus.Subscribe("bob")
	defer cancelB()

	bus.Publish(Event{ChannelID: "channel1"})

	if len(alice) != 1 || len(bob) != 1 {
		t.Errorf("a channel broadcast reached %d and %d subscribers, want both; channel messages "+
			"would stop arriving", len(alice), len(bob))
	}
}

// Naming somebody who is not connected is not an error — most recipients are usually offline.
func TestNamingAnAbsentUserIsHarmless(t *testing.T) {
	bus := NewMemoryBus()

	alice, cancel := bus.Subscribe("alice")
	defer cancel()

	bus.Publish(Event{ConversationID: "conv1", Recipients: []string{"alice", "nobody-here"}})

	if len(alice) != 1 {
		t.Errorf("alice received %d events", len(alice))
	}
	if n := bus.Dropped(); n != 0 {
		t.Errorf("an offline recipient counted as %d dropped deliveries; the drop counter is meant "+
			"to mean a connected subscriber fell behind", n)
	}
}
