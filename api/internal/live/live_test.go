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
	events, cancel := bus.Subscribe()
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
	events, cancel := bus.Subscribe()
	cancel()

	bus.Publish(Event{ChannelID: "c"})

	if _, open := <-events; open {
		t.Fatal("a cancelled subscriber must not receive events")
	}
}
