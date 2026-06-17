// Package live broadcasts notification events to connected web clients. The
// development implementation is in-process; production should fan out via Redis
// pub/sub so multiple App API instances share events.
package live

import (
	"sync"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Event is a live notification delivered to subscribed web clients.
type Event struct {
	ChannelID string         `json:"channelId"`
	Message   domain.Message `json:"message"`
}

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
	ch := make(chan Event, 16)
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
