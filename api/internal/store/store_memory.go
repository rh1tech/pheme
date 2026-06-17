package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Memory is an in-memory Store implementation for local development and tests.
// It is safe for concurrent use. Data does not survive a restart.
type Memory struct {
	mu            sync.RWMutex
	users         map[string]domain.User
	channels      map[string]domain.Channel
	apiKeys       map[string]domain.APIKey
	devices       map[string]domain.Device
	subscriptions map[string]domain.Subscription
	messages      map[string]domain.Message
	deliveries    map[string]domain.Delivery
}

// NewMemory returns an initialised in-memory store.
func NewMemory() *Memory {
	return &Memory{
		users:         map[string]domain.User{},
		channels:      map[string]domain.Channel{},
		apiKeys:       map[string]domain.APIKey{},
		devices:       map[string]domain.Device{},
		subscriptions: map[string]domain.Subscription{},
		messages:      map[string]domain.Message{},
		deliveries:    map[string]domain.Delivery{},
	}
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Memory) CreateUser(_ context.Context, u domain.User) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u.ID == "" {
		u.ID = newID()
	}
	m.users[u.ID] = u
	return u, nil
}

func (m *Memory) UserByEmail(_ context.Context, email string) (domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return domain.User{}, ErrNotFound
}

func (m *Memory) CreateChannel(_ context.Context, c domain.Channel) (domain.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == "" {
		c.ID = newID()
	}
	m.channels[c.ID] = c
	return c, nil
}

func (m *Memory) ChannelByID(_ context.Context, id string) (domain.Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if c, ok := m.channels[id]; ok {
		return c, nil
	}
	return domain.Channel{}, ErrNotFound
}

func (m *Memory) ChannelByPublicID(_ context.Context, publicID string) (domain.Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.channels {
		if c.PublicID == publicID {
			return c, nil
		}
	}
	return domain.Channel{}, ErrNotFound
}

func (m *Memory) ChannelsByOwner(_ context.Context, ownerID string) ([]domain.Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.Channel
	for _, c := range m.channels {
		if c.OwnerID == ownerID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *Memory) UpdateChannel(_ context.Context, id, name string, mode domain.SubscriptionMode) (domain.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.channels[id]
	if !ok {
		return domain.Channel{}, ErrNotFound
	}
	if name != "" {
		c.Name = name
	}
	if mode != "" {
		c.SubscriptionMode = mode
	}
	m.channels[id] = c
	return c, nil
}

func (m *Memory) DeleteChannel(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.channels[id]; !ok {
		return ErrNotFound
	}
	delete(m.channels, id)
	for kid, k := range m.apiKeys {
		if k.ChannelID == id {
			delete(m.apiKeys, kid)
		}
	}
	for sid, s := range m.subscriptions {
		if s.ChannelID == id {
			delete(m.subscriptions, sid)
		}
	}
	msgIDs := map[string]struct{}{}
	for mid, msg := range m.messages {
		if msg.ChannelID == id {
			msgIDs[mid] = struct{}{}
			delete(m.messages, mid)
		}
	}
	for did, d := range m.deliveries {
		if _, ok := msgIDs[d.MessageID]; ok {
			delete(m.deliveries, did)
		}
	}
	return nil
}

func (m *Memory) CreateAPIKey(_ context.Context, k domain.APIKey) (domain.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k.ID == "" {
		k.ID = newID()
	}
	m.apiKeys[k.ID] = k
	return k, nil
}

func (m *Memory) APIKeysByChannel(_ context.Context, channelID string) ([]domain.APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.APIKey
	for _, k := range m.apiKeys {
		if k.ChannelID == channelID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *Memory) RevokeAPIKey(_ context.Context, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.apiKeys[keyID]
	if !ok {
		return ErrNotFound
	}
	if k.RevokedAt == nil {
		now := time.Now().UTC()
		k.RevokedAt = &now
		m.apiKeys[keyID] = k
	}
	return nil
}

func (m *Memory) CreateDevice(_ context.Context, d domain.Device) (domain.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID == "" {
		d.ID = newID()
	}
	m.devices[d.ID] = d
	return d, nil
}

func (m *Memory) Subscribe(_ context.Context, s domain.Subscription) (domain.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.ID == "" {
		s.ID = newID()
	}
	m.subscriptions[s.ID] = s
	return s, nil
}

func (m *Memory) ActiveDevicesForChannel(_ context.Context, channelID string) ([]domain.Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.Device
	for _, s := range m.subscriptions {
		if s.ChannelID == channelID && s.Status == domain.SubActive {
			if d, ok := m.devices[s.DeviceID]; ok {
				out = append(out, d)
			}
		}
	}
	return out, nil
}

func (m *Memory) CreateMessage(_ context.Context, msg domain.Message) (domain.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg.ID == "" {
		msg.ID = newID()
	}
	m.messages[msg.ID] = msg
	return msg, nil
}

// MessagesByChannel returns messages newest-first. cursor is an exclusive upper
// bound on message ID; empty means from the newest. query, if non-empty, keeps
// only messages whose title or body contains it (case-insensitive).
func (m *Memory) MessagesByChannel(_ context.Context, channelID, cursor, query string, limit int) ([]domain.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	var out []domain.Message
	for _, msg := range m.messages {
		if msg.ChannelID != channelID {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(msg.Title), q) && !strings.Contains(strings.ToLower(msg.Body), q) {
			continue
		}
		out = append(out, msg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if cursor != "" {
		filtered := out[:0]
		seen := false
		for _, msg := range out {
			if seen {
				filtered = append(filtered, msg)
			}
			if msg.ID == cursor {
				seen = true
			}
		}
		out = filtered
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) CreateDelivery(_ context.Context, d domain.Delivery) (domain.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID == "" {
		d.ID = newID()
	}
	if d.SentAt.IsZero() {
		d.SentAt = time.Now().UTC()
	}
	m.deliveries[d.ID] = d
	return d, nil
}

func (m *Memory) Close(context.Context) error { return nil }
