package blob

import (
	"context"
	"sync"
)

type memItem struct {
	data        []byte
	contentType string
}

// Memory is an in-memory Store for local development and tests. It is safe for
// concurrent use; data does not survive a restart.
type Memory struct {
	mu    sync.RWMutex
	items map[string]memItem
}

// NewMemory returns an initialised in-memory blob store.
func NewMemory() *Memory {
	return &Memory{items: map[string]memItem{}}
}

// Put stores a copy of data under a new id.
func (m *Memory) Put(_ context.Context, data []byte, contentType string) (string, error) {
	id := newID()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.mu.Lock()
	m.items[id] = memItem{data: cp, contentType: contentType}
	m.mu.Unlock()
	return id, nil
}

// Get returns a copy of the stored bytes and content type for an id.
func (m *Memory) Get(_ context.Context, id string) ([]byte, string, error) {
	m.mu.RLock()
	it, ok := m.items[id]
	m.mu.RUnlock()
	if !ok {
		return nil, "", ErrNotFound
	}
	cp := make([]byte, len(it.data))
	copy(cp, it.data)
	return cp, it.contentType, nil
}

// Delete removes a blob; deleting a missing id is a no-op.
func (m *Memory) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.items, id)
	m.mu.Unlock()
	return nil
}

// Len is the number of stored blobs. For tests that assert nothing is orphaned.
func (m *Memory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}

// Close is a no-op for the in-memory store.
func (m *Memory) Close(context.Context) error { return nil }
