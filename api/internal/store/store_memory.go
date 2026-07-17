package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rh1tech/pheme/api/internal/blob"
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
	members       map[string]domain.ChannelMember
	messages      map[string]domain.Message
	deliveries    map[string]domain.Delivery
	comments      map[string]domain.Comment
	conversations map[string]domain.Conversation
	convMembers   map[string]domain.ConversationMember
	chatMessages  map[string]domain.ChatMessage
	attachments   map[string]domain.Attachment
	keyPackages   map[string]domain.MLSKeyPackage
	keyBackups    map[string]domain.MLSKeyBackup // by userId (one per user)
	mlsDevices      map[string]domain.MLSDevice    // by userId + ":" + deviceId
	revokedSessions map[string]time.Time           // sid -> expiry (terminated sessions)
	mlsGroupInfo    map[string]domain.MLSGroupInfo // by conversationId (one per group)
	blobs           blob.Store
}

// NewMemory returns an initialised in-memory store. The blob store (may be nil)
// is used to remove a deleted message's images during cascade deletes.
func NewMemory(blobs blob.Store) *Memory {
	return &Memory{
		users:         map[string]domain.User{},
		channels:      map[string]domain.Channel{},
		apiKeys:       map[string]domain.APIKey{},
		devices:       map[string]domain.Device{},
		subscriptions: map[string]domain.Subscription{},
		members:       map[string]domain.ChannelMember{},
		messages:      map[string]domain.Message{},
		deliveries:    map[string]domain.Delivery{},
		comments:      map[string]domain.Comment{},
		conversations: map[string]domain.Conversation{},
		convMembers:   map[string]domain.ConversationMember{},
		chatMessages:  map[string]domain.ChatMessage{},
		attachments:   map[string]domain.Attachment{},
		keyPackages:   map[string]domain.MLSKeyPackage{},
		keyBackups:    map[string]domain.MLSKeyBackup{},
		mlsDevices:      map[string]domain.MLSDevice{},
		revokedSessions: map[string]time.Time{},
		mlsGroupInfo:    map[string]domain.MLSGroupInfo{},
		blobs:           blobs,
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

func (m *Memory) UserByID(_ context.Context, id string) (domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if u, ok := m.users[id]; ok {
		return u, nil
	}
	return domain.User{}, ErrNotFound
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

func (m *Memory) UserByUsername(_ context.Context, usernameLower string) (domain.User, error) {
	if usernameLower == "" {
		return domain.User{}, ErrNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.UsernameLower == usernameLower {
			return u, nil
		}
	}
	return domain.User{}, ErrNotFound
}

func (m *Memory) UsersByIDs(_ context.Context, ids []string) (map[string]domain.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]domain.User, len(ids))
	for _, id := range ids {
		if u, ok := m.users[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

func (m *Memory) UpdateUserProfile(_ context.Context, userID string, p domain.UserProfileUpdate) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return domain.User{}, ErrNotFound
	}
	lower := strings.ToLower(strings.TrimSpace(p.Username))
	if lower != "" {
		for id, other := range m.users {
			if id != userID && other.UsernameLower == lower {
				return domain.User{}, ErrUsernameTaken
			}
		}
	}
	u.Username = strings.TrimSpace(p.Username)
	u.UsernameLower = lower
	u.DisplayName = strings.TrimSpace(p.DisplayName)
	u.Bio = strings.TrimSpace(p.Bio)
	u.Phone = strings.TrimSpace(p.Phone)
	u.Website = strings.TrimSpace(p.Website)
	m.users[userID] = u
	return u, nil
}

func (m *Memory) SetUserAvatar(ctx context.Context, userID, avatarID string) (domain.User, error) {
	m.mu.Lock()
	u, ok := m.users[userID]
	if !ok {
		m.mu.Unlock()
		return domain.User{}, ErrNotFound
	}
	old := u.AvatarID
	u.AvatarID = avatarID
	m.users[userID] = u
	m.mu.Unlock()
	if old != "" && old != avatarID {
		deleteBlobs(ctx, m.blobs, []string{old})
	}
	return u, nil
}

func (m *Memory) SetChannelAvatar(ctx context.Context, channelID, avatarID string) (domain.Channel, error) {
	m.mu.Lock()
	ch, ok := m.channels[channelID]
	if !ok {
		m.mu.Unlock()
		return domain.Channel{}, ErrNotFound
	}
	old := ch.AvatarID
	ch.AvatarID = avatarID
	m.channels[channelID] = ch
	m.mu.Unlock()
	if old != "" && old != avatarID {
		deleteBlobs(ctx, m.blobs, []string{old})
	}
	return ch, nil
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

func (m *Memory) UpdateUserPassword(_ context.Context, userID, passwordHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return ErrNotFound
	}
	u.PasswordHash = passwordHash
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

func (m *Memory) SearchUsers(_ context.Context, query string, limit int) ([]domain.User, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.User
	for _, u := range m.users {
		if u.Status != domain.UserActive {
			continue
		}
		if strings.Contains(strings.ToLower(u.Username), q) || strings.Contains(strings.ToLower(u.DisplayName), q) {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) DeleteUser(ctx context.Context, userID string) error {
	m.mu.RLock()
	u, ok := m.users[userID]
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
	// Remove the user's memberships in every channel, not just channels they own.
	for mid, mem := range m.members {
		if mem.UserID == userID {
			delete(m.members, mid)
		}
	}
	// Remove the user's comments across all channels.
	for cid, c := range m.comments {
		if c.UserID == userID {
			delete(m.comments, cid)
		}
	}
	delete(m.users, userID)
	m.mu.Unlock()
	if u.AvatarID != "" {
		deleteBlobs(ctx, m.blobs, []string{u.AvatarID})
	}
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

func (m *Memory) ChannelByAlias(_ context.Context, aliasLower string) (domain.Channel, error) {
	if aliasLower == "" {
		return domain.Channel{}, ErrNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.channels {
		if c.AliasLower == aliasLower {
			return c, nil
		}
	}
	return domain.Channel{}, ErrNotFound
}

func (m *Memory) SetChannelAlias(_ context.Context, channelID, alias string) (domain.Channel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.channels[channelID]
	if !ok {
		return domain.Channel{}, ErrNotFound
	}
	lower := strings.ToLower(strings.TrimSpace(alias))
	if lower != "" {
		for id, other := range m.channels {
			if id != channelID && other.AliasLower == lower {
				return domain.Channel{}, ErrAliasTaken
			}
		}
	}
	c.Alias = strings.TrimSpace(alias)
	c.AliasLower = lower
	m.channels[channelID] = c
	return c, nil
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

func (m *Memory) DeleteChannel(ctx context.Context, id string) error {
	m.mu.Lock()
	ch, ok := m.channels[id]
	if !ok {
		m.mu.Unlock()
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
	for mid, mem := range m.members {
		if mem.ChannelID == id {
			delete(m.members, mid)
		}
	}
	msgIDs := map[string]struct{}{}
	var imageIDs []string
	// The channel's own avatar is a blob like any message image, and would leak
	// if it were not swept up with them.
	if ch.AvatarID != "" {
		imageIDs = append(imageIDs, ch.AvatarID)
	}
	for mid, msg := range m.messages {
		if msg.ChannelID == id {
			msgIDs[mid] = struct{}{}
			for _, img := range msg.Images {
				imageIDs = append(imageIDs, img.ID)
			}
			delete(m.messages, mid)
		}
	}
	for did, d := range m.deliveries {
		if _, ok := msgIDs[d.MessageID]; ok {
			delete(m.deliveries, did)
		}
	}
	for cid, c := range m.comments {
		if c.ChannelID == id {
			delete(m.comments, cid)
		}
	}
	m.mu.Unlock()
	// Remove image blobs outside the lock (the blob store may do I/O).
	deleteBlobs(ctx, m.blobs, imageIDs)
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

func (m *Memory) SetSubscriptionStatusForUser(_ context.Context, channelID, userID string, status domain.SubscriptionStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	deviceSet := map[string]struct{}{}
	for _, d := range m.devices {
		if d.UserID == userID {
			deviceSet[d.ID] = struct{}{}
		}
	}
	for id, s := range m.subscriptions {
		if s.ChannelID != channelID {
			continue
		}
		if _, ok := deviceSet[s.DeviceID]; ok {
			s.Status = status
			m.subscriptions[id] = s
		}
	}
	return nil
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

func (m *Memory) DevicesForUsers(_ context.Context, userIDs []string) ([]domain.Device, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	wanted := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		wanted[id] = struct{}{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.Device
	for _, d := range m.devices {
		if _, ok := wanted[d.UserID]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// --- Channel members ---

func (m *Memory) UpsertMember(_ context.Context, mem domain.ChannelMember) (domain.ChannelMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// One membership per (channel, user); re-joining returns the existing row so
	// an already-active member is never downgraded back to pending.
	for _, existing := range m.members {
		if existing.ChannelID == mem.ChannelID && existing.UserID == mem.UserID {
			return existing, nil
		}
	}
	if mem.ID == "" {
		mem.ID = newID()
	}
	m.members[mem.ID] = mem
	return mem, nil
}

func (m *Memory) MembershipForUser(_ context.Context, channelID, userID string) (domain.ChannelMember, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mem := range m.members {
		if mem.ChannelID == channelID && mem.UserID == userID {
			return mem, nil
		}
	}
	return domain.ChannelMember{}, ErrNotFound
}

func (m *Memory) ListMembers(_ context.Context, channelID string, status domain.MemberStatus, offset, limit int) ([]domain.ChannelMember, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []domain.ChannelMember
	for _, mem := range m.members {
		if mem.ChannelID != channelID {
			continue
		}
		if status != "" && mem.Status != status {
			continue
		}
		all = append(all, mem)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return paginate(all, offset, limit), int64(len(all)), nil
}

func (m *Memory) UpdateMemberStatus(_ context.Context, channelID, userID string, status domain.MemberStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.members {
		if mem.ChannelID == channelID && mem.UserID == userID {
			mem.Status = status
			m.members[id] = mem
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) UpdateMemberRole(_ context.Context, channelID, userID string, role domain.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.members {
		if mem.ChannelID == channelID && mem.UserID == userID {
			mem.Role = role
			m.members[id] = mem
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) RemoveMember(_ context.Context, channelID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.members {
		if mem.ChannelID == channelID && mem.UserID == userID {
			delete(m.members, id)
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) ChannelsForMember(_ context.Context, userID string) ([]domain.Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.Channel
	for _, mem := range m.members {
		if mem.UserID != userID {
			continue
		}
		if c, ok := m.channels[mem.ChannelID]; ok {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
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
func (m *Memory) MessageByID(_ context.Context, id string) (domain.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	msg, ok := m.messages[id]
	if !ok {
		return domain.Message{}, ErrNotFound
	}
	return msg, nil
}

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

func (m *Memory) MessagesAround(_ context.Context, channelID, messageID string, limit int) ([]domain.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []domain.Message
	for _, msg := range m.messages {
		if msg.ChannelID == channelID {
			all = append(all, msg)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })

	centre := -1
	for i, msg := range all {
		if msg.ID == messageID {
			centre = i
			break
		}
	}
	if centre == -1 {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = 50
	}
	half := limit / 2
	start := max(0, centre-half)
	end := min(len(all), centre+half+1)
	return all[start:end], nil
}

func (m *Memory) LastMessagesByChannels(_ context.Context, channelIDs []string) (map[string]domain.Message, error) {
	out := make(map[string]domain.Message, len(channelIDs))
	if len(channelIDs) == 0 {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(channelIDs))
	for _, id := range channelIDs {
		wanted[id] = struct{}{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, msg := range m.messages {
		if _, ok := wanted[msg.ChannelID]; !ok {
			continue
		}
		if cur, ok := out[msg.ChannelID]; ok && !msg.CreatedAt.After(cur.CreatedAt) {
			continue
		}
		out[msg.ChannelID] = msg
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

// --- Comments ---

func (m *Memory) CreateComment(_ context.Context, c domain.Comment) (domain.Comment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == "" {
		c.ID = newID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	m.comments[c.ID] = c
	return c, nil
}

func (m *Memory) CommentByID(_ context.Context, id string) (domain.Comment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if c, ok := m.comments[id]; ok {
		return c, nil
	}
	return domain.Comment{}, ErrNotFound
}

// CommentsByMessage returns a message's comments newest-first. cursor is an
// exclusive anchor comment ID; empty means from the newest.
func (m *Memory) CommentsByMessage(_ context.Context, messageID, cursor string, limit int) ([]domain.Comment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.Comment
	for _, c := range m.comments {
		if c.MessageID == messageID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if cursor != "" {
		filtered := out[:0]
		seen := false
		for _, c := range out {
			if seen {
				filtered = append(filtered, c)
			}
			if c.ID == cursor {
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

func (m *Memory) CommentCountsByMessages(_ context.Context, messageIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(messageIDs))
	if len(messageIDs) == 0 {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(messageIDs))
	for _, id := range messageIDs {
		wanted[id] = struct{}{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.comments {
		if _, ok := wanted[c.MessageID]; ok {
			out[c.MessageID]++
		}
	}
	return out, nil
}

func (m *Memory) DeleteMessage(ctx context.Context, id string) error {
	m.mu.Lock()
	msg, ok := m.messages[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	delete(m.messages, id)

	imageIDs := make([]string, 0, len(msg.Images))
	for _, img := range msg.Images {
		imageIDs = append(imageIDs, img.ID)
	}
	for cid, c := range m.comments {
		if c.MessageID == id {
			delete(m.comments, cid)
		}
	}
	for did, d := range m.deliveries {
		if d.MessageID == id {
			delete(m.deliveries, did)
		}
	}
	m.mu.Unlock()
	// Outside the lock: the blob store may do I/O.
	deleteBlobs(ctx, m.blobs, imageIDs)
	return nil
}

func (m *Memory) DeleteComment(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.comments[id]; !ok {
		return ErrNotFound
	}
	delete(m.comments, id)
	return nil
}

func (m *Memory) AdminListComments(_ context.Context, query string, offset, limit int) ([]domain.Comment, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	var all []domain.Comment
	for _, c := range m.comments {
		if q == "" || strings.Contains(strings.ToLower(c.Body), q) {
			all = append(all, c)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	return paginate(all, offset, limit), int64(len(all)), nil
}

func (m *Memory) Close(context.Context) error { return nil }
