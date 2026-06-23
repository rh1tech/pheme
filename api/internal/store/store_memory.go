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

func (m *Memory) UpdateUserRole(_ context.Context, userID string, role domain.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	u.Role = role
	m.users[userID] = u
	return nil
}

func (m *Memory) UpdateUserStatus(_ context.Context, userID string, status domain.UserStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	u.Status = status
	m.users[userID] = u
	return nil
}

func (m *Memory) ListUsers(_ context.Context) ([]domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) AdminListUsers(_ context.Context, query string, offset, limit int) ([]domain.User, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	var all []domain.User
	for _, u := range m.users {
		if q == "" || strings.Contains(strings.ToLower(u.Email), q) {
			all = append(all, u)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return paginate(all, offset, limit), int64(len(all)), nil
}

func (m *Memory) DeleteUser(ctx context.Context, userID string) error {
	m.mu.RLock()
	_, ok := m.users[userID]
	var channelIDs, deviceIDs []string
	for _, c := range m.channels {
		if c.OwnerID == userID {
			channelIDs = append(channelIDs, c.ID)
		}
	}
	for _, d := range m.devices {
		if d.UserID == userID {
			deviceIDs = append(deviceIDs, d.ID)
		}
	}
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}

	for _, cid := range channelIDs {
		if err := m.DeleteChannel(ctx, cid); err != nil {
			return err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	deviceSet := map[string]struct{}{}
	for _, did := range deviceIDs {
		deviceSet[did] = struct{}{}
		delete(m.devices, did)
	}
	for sid, s := range m.subscriptions {
		if _, ok := deviceSet[s.DeviceID]; ok {
			delete(m.subscriptions, sid)
		}
	}
	delete(m.users, userID)
	return nil
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

func (m *Memory) UpdateChannelStatus(_ context.Context, id string, status domain.ChannelStatus) (domain.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.channels[id]
	if !ok {
		return domain.Channel{}, ErrNotFound
	}
	c.Status = status
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

func (m *Memory) ListAllChannels(_ context.Context) ([]domain.Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.Channel, 0, len(m.channels))
	for _, c := range m.channels {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) AdminListChannels(_ context.Context, query string, offset, limit int) ([]domain.Channel, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	var all []domain.Channel
	for _, c := range m.channels {
		if q == "" || strings.Contains(strings.ToLower(c.Name), q) {
			all = append(all, c)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return paginate(all, offset, limit), int64(len(all)), nil
}

// paginate returns the slice window [offset, offset+limit). A non-positive limit
// returns everything from offset.
func paginate[T any](items []T, offset, limit int) []T {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(items) {
		return []T{}
	}
	end := len(items)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return items[offset:end]
}

func (m *Memory) AdminStats(_ context.Context, topN, recentN int) (domain.AdminStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := domain.AdminStats{
		Users:      int64(len(m.users)),
		Channels:   int64(len(m.channels)),
		Messages:   int64(len(m.messages)),
		Deliveries: int64(len(m.deliveries)),
		Devices:    int64(len(m.devices)),
	}

	// Top channels by message volume.
	counts := map[string]int64{}
	for _, msg := range m.messages {
		counts[msg.ChannelID]++
	}
	for cid, n := range counts {
		name := cid
		if c, ok := m.channels[cid]; ok {
			name = c.Name
		}
		stats.TopChannels = append(stats.TopChannels, domain.ChannelVolume{ChannelID: cid, Name: name, Count: n})
	}
	sort.Slice(stats.TopChannels, func(i, j int) bool { return stats.TopChannels[i].Count > stats.TopChannels[j].Count })
	if topN > 0 && len(stats.TopChannels) > topN {
		stats.TopChannels = stats.TopChannels[:topN]
	}

	// Most recent messages across all channels.
	all := make([]domain.Message, 0, len(m.messages))
	for _, msg := range m.messages {
		all = append(all, msg)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	if recentN > 0 && len(all) > recentN {
		all = all[:recentN]
	}
	stats.RecentMessages = all
	return stats, nil
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
	if endpoint := webPushEndpoint(d.WebPushSub); endpoint != "" {
		for id, existing := range m.devices {
			if existing.UserID == d.UserID && existing.WebPushEndpoint == endpoint {
				existing.WebPushSub = d.WebPushSub
				existing.LastSeenAt = d.LastSeenAt
				m.devices[id] = existing
				return existing, nil
			}
		}
		d.WebPushEndpoint = endpoint
	}
	if d.ID == "" {
		d.ID = newID()
	}
	m.devices[d.ID] = d
	return d, nil
}

func (m *Memory) Subscribe(_ context.Context, s domain.Subscription) (domain.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Upsert: if this device already has a subscription to the channel, update it
	// rather than creating a duplicate.
	for id, existing := range m.subscriptions {
		if existing.ChannelID == s.ChannelID && existing.DeviceID == s.DeviceID {
			existing.Status = s.Status
			m.subscriptions[id] = existing
			return existing, nil
		}
	}
	if s.ID == "" {
		s.ID = newID()
	}
	m.subscriptions[s.ID] = s
	return s, nil
}

func (m *Memory) SubscriptionForDevice(_ context.Context, channelID, deviceID string) (domain.Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.subscriptions {
		if s.ChannelID == channelID && s.DeviceID == deviceID {
			return s, nil
		}
	}
	return domain.Subscription{}, ErrNotFound
}

func (m *Memory) Unsubscribe(_ context.Context, channelID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.subscriptions {
		if s.ChannelID == channelID && s.DeviceID == deviceID {
			delete(m.subscriptions, id)
			return nil
		}
	}
	return ErrNotFound
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
