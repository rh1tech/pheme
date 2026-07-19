// Package live broadcasts notification events to connected web clients. The
// development implementation is in-process; production should fan out via Redis
// pub/sub so multiple App API instances share events.
package live

import (
	"sync"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Event is a live event delivered to subscribed clients over the per-user SSE
// stream. It is one of two shapes, distinguished by which id is set:
//   - a channel broadcast: ChannelID + Message (authorised by channel read access)
//   - a conversation message: ConversationID + ChatMessage (authorised by membership)
//
// Both ride the same stream; the SSE handler branches on ConversationID.
type Event struct {
	ChannelID string         `json:"channelId,omitempty"`
	Message   domain.Message `json:"message,omitempty"`

	ConversationID string              `json:"conversationId,omitempty"`
	ChatMessage    *domain.ChatMessage `json:"chatMessage,omitempty"`
	// ConversationDeleted marks a conversation that has been removed, so members drop
	// it from their list without a refetch. Carries ConversationID.
	ConversationDeleted bool `json:"conversationDeleted,omitempty"`
	// Receipt is a member's delivered/read watermarks moving. It carries ConversationID,
	// and goes to the conversation's members like any other event — so a sender's ticks
	// fill in under their eyes rather than on the next fetch. It says nothing about what
	// was read; only how far.
	Receipt *domain.ConversationReceipt `json:"receipt,omitempty"`
	// Recipients authorises delivery of an event whose usual membership check no longer
	// holds — a deletion, whose membership rows are already gone by the time anyone can
	// be told about it.
	//
	// It MUST be serialised. In production the bus is Redis (live_redis.go), which moves
	// an Event between processes by marshalling it to JSON: a `json:"-"` here — which is
	// what this field used to carry — silently deleted the list in transit, so every
	// subscriber saw nil, fell through to the membership check, and found the very rows
	// the deletion had just removed. The event was then dropped. Conversation deletions
	// reached nobody in production, and the in-memory bus used by dev and the tests hid
	// it, because there the struct is passed by value and never encoded at all.
	//
	// The stream strips it again before writing to the client (see the SSE handler): it
	// is an authorisation list, and no subscriber has any business seeing who else is on
	// it.
	Recipients []string `json:"recipients,omitempty"`

	// CallSignal is a NUDGE for a 1:1 voice call: "call X now has a signal N". It is
	// deliberately not the signal.
	//
	// This bus is allowed to drop events, and a dropped SDP answer is a call that silently
	// never connects. So the signal itself lives in a short-lived ordered mailbox
	// (internal/calls) and the client fetches it from there; losing this event costs a few
	// hundred milliseconds, not the call. It also means a browser whose EventSource was
	// reconnecting misses nothing, which the bus alone has no answer for.
	CallSignal *CallSignal `json:"callSignal,omitempty"`
}

// CallSignal tells a member's devices that a call has something new to fetch.
type CallSignal struct {
	CallID string `json:"callId"`
	Seq    int    `json:"seq"`
	// FromUserID lets a device tell a call it is receiving from one its own user placed on
	// another device — the caller's other devices must not ring.
	FromUserID string `json:"fromUserId"`
}

// subscriberBuffer is how many events a subscriber may fall behind by before its events start
// being dropped. A drop is silent to the user — the message simply never appears — so the number
// is a tradeoff between memory per connection and how long a stalled reader is tolerated.
const subscriberBuffer = 64

// Bus distributes live events to subscribers.
type Bus interface {
	Publish(e Event)
	Subscribe() (<-chan Event, func())
}

// MemoryBus is an in-process Bus. It is safe for concurrent use.
type MemoryBus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// NewMemoryBus returns an initialised in-process bus.
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{subs: map[chan Event]struct{}{}}
}

// Publish delivers e to all current subscribers without blocking on slow ones.
func (b *MemoryBus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default: // drop for slow consumers
		}
	}
}

// Subscribe registers a new subscriber and returns its channel plus a cancel
// function that unsubscribes and closes the channel.
func (b *MemoryBus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}
