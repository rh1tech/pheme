package store

import (
	"context"
	"sort"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func (m *Memory) CreateConversation(_ context.Context, c domain.Conversation, members []domain.ConversationMember) (domain.Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.ID == "" {
		c.ID = newID()
	}
	m.conversations[c.ID] = c
	for _, mem := range members {
		if mem.ID == "" {
			mem.ID = newID()
		}
		mem.ConversationID = c.ID
		m.convMembers[mem.ID] = withReceiptFloor(mem)
	}
	return c, nil
}

func (m *Memory) ConversationByID(_ context.Context, id string) (domain.Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.conversations[id]
	if !ok {
		return domain.Conversation{}, ErrNotFound
	}
	return c, nil
}

func (m *Memory) ConversationByDirectKey(_ context.Context, directKey string) (domain.Conversation, error) {
	if directKey == "" {
		return domain.Conversation{}, ErrNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.conversations {
		if c.DirectKey == directKey {
			return c, nil
		}
	}
	return domain.Conversation{}, ErrNotFound
}

func (m *Memory) ConversationsForUser(_ context.Context, userID string) ([]domain.Conversation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mine := map[string]struct{}{}
	for _, mem := range m.convMembers {
		if mem.UserID == userID {
			mine[mem.ConversationID] = struct{}{}
		}
	}
	// Newest message time per conversation, for ordering.
	lastAt := map[string]int64{}
	for _, msg := range m.chatMessages {
		if _, ok := mine[msg.ConversationID]; !ok {
			continue
		}
		if t := msg.CreatedAt.UnixNano(); t > lastAt[msg.ConversationID] {
			lastAt[msg.ConversationID] = t
		}
	}
	out := make([]domain.Conversation, 0, len(mine))
	for id := range mine {
		out = append(out, m.conversations[id])
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := lastAt[out[i].ID], lastAt[out[j].ID]
		if ai == 0 {
			ai = out[i].CreatedAt.UnixNano()
		}
		if aj == 0 {
			aj = out[j].CreatedAt.UnixNano()
		}
		return ai > aj
	})
	return out, nil
}

func (m *Memory) ConversationMembers(_ context.Context, conversationID string) ([]domain.ConversationMember, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.ConversationMember
	for _, mem := range m.convMembers {
		if mem.ConversationID == conversationID {
			out = append(out, mem)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JoinedAt.Before(out[j].JoinedAt) })
	return out, nil
}

func (m *Memory) ConversationMembership(_ context.Context, conversationID, userID string) (domain.ConversationMember, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mem := range m.convMembers {
		if mem.ConversationID == conversationID && mem.UserID == userID {
			return mem, nil
		}
	}
	return domain.ConversationMember{}, ErrNotFound
}

func (m *Memory) AddConversationMember(_ context.Context, mem domain.ConversationMember) (domain.ConversationMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Idempotent: re-adding an existing member returns the existing row.
	for _, existing := range m.convMembers {
		if existing.ConversationID == mem.ConversationID && existing.UserID == mem.UserID {
			return existing, nil
		}
	}
	if mem.ID == "" {
		mem.ID = newID()
	}
	mem = withReceiptFloor(mem)
	m.convMembers[mem.ID] = mem
	return mem, nil
}

func (m *Memory) RemoveConversationMember(_ context.Context, conversationID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.convMembers {
		if mem.ConversationID == conversationID && mem.UserID == userID {
			delete(m.convMembers, id)
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) SetConversationMemberRole(_ context.Context, conversationID, userID string, role domain.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.convMembers {
		if mem.ConversationID == conversationID && mem.UserID == userID {
			mem.Role = role
			m.convMembers[id] = mem
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) DeleteConversation(_ context.Context, conversationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.conversations, conversationID)
	for id, mem := range m.convMembers {
		if mem.ConversationID == conversationID {
			delete(m.convMembers, id)
		}
	}
	for id, msg := range m.chatMessages {
		if msg.ConversationID == conversationID {
			delete(m.chatMessages, id)
		}
	}
	return nil
}

func (m *Memory) AppendChatMessage(_ context.Context, msg domain.ChatMessage) (domain.ChatMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appendChatMessageLocked(msg), nil
}

// The append itself, for callers that already hold the write lock — CommitMLSGroup
// relays its control messages inside the same critical section as the epoch
// compare-and-set, and taking the lock twice would deadlock.
func (m *Memory) appendChatMessageLocked(msg domain.ChatMessage) domain.ChatMessage {
	if msg.ID == "" {
		msg.ID = newID()
	}
	// Assign a per-conversation sequence only to a message authored here; one that
	// arrived over a relay already carries the hub's, and reassigning it would fork
	// the order. The next value is one past the highest this conversation holds.
	if msg.Seq == 0 {
		var max int64
		for _, existing := range m.chatMessages {
			if existing.ConversationID == msg.ConversationID && existing.Seq > max {
				max = existing.Seq
			}
		}
		msg.Seq = max + 1
	}
	m.chatMessages[msg.ID] = msg
	return msg
}

func (m *Memory) ClearConversationHistory(_ context.Context, conversationID, userID string, before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.convMembers {
		if mem.ConversationID == conversationID && mem.UserID == userID {
			mem.ClearedAt = before
			m.convMembers[id] = mem
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) SetConversationReceipt(_ context.Context, conversationID, userID string, delivered, read time.Time) (domain.ConversationReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mem := range m.convMembers {
		if mem.ConversationID != conversationID || mem.UserID != userID {
			continue
		}
		// Forward only: an out-of-order report must not un-read what was read.
		if delivered.After(mem.DeliveredAt) {
			mem.DeliveredAt = delivered
		}
		if read.After(mem.ReadAt) {
			mem.ReadAt = read
		}
		m.convMembers[id] = mem
		return domain.ConversationReceipt{
			UserID:      userID,
			DeliveredAt: mem.DeliveredAt,
			ReadAt:      mem.ReadAt,
		}, nil
	}
	return domain.ConversationReceipt{}, ErrNotFound
}

func (m *Memory) ChatMessagesByConversation(_ context.Context, conversationID, cursor string, limit int, after time.Time) ([]domain.ChatMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.ChatMessage
	for _, msg := range m.chatMessages {
		if msg.ConversationID != conversationID {
			continue
		}
		// The transcript, not the raw log — MLS protocol traffic is not part of it. See
		// domain.MLSProtocolContentTypes.
		if domain.IsMLSProtocol(msg.ContentType) {
			continue
		}
		// Respect the caller's clear-history watermark: hide messages at or before it.
		if !after.IsZero() && !msg.CreatedAt.After(after) {
			continue
		}
		out = append(out, msg)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].Seq > out[j].Seq // deterministic tiebreak within a timestamp
	})
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

func (m *Memory) LastChatMessagesByConversations(_ context.Context, conversationIDs []string) (map[string]domain.ChatMessage, error) {
	out := make(map[string]domain.ChatMessage, len(conversationIDs))
	if len(conversationIDs) == 0 {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(conversationIDs))
	for _, id := range conversationIDs {
		wanted[id] = struct{}{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, msg := range m.chatMessages {
		if _, ok := wanted[msg.ConversationID]; !ok {
			continue
		}
		// The last thing SAID, not the last row written — protocol traffic is not a message.
		// See domain.MLSProtocolContentTypes.
		if domain.IsMLSProtocol(msg.ContentType) {
			continue
		}
		if cur, ok := out[msg.ConversationID]; ok && !msg.CreatedAt.After(cur.CreatedAt) {
			continue
		}
		out[msg.ConversationID] = msg
	}
	return out, nil
}

// --- attachments ---

func (m *Memory) CreateAttachment(_ context.Context, a domain.Attachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	m.attachments[a.ID] = a
	return nil
}

func (m *Memory) GetAttachment(_ context.Context, id string) (domain.Attachment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.attachments[id]
	if !ok {
		return domain.Attachment{}, ErrNotFound
	}
	return a, nil
}

func (m *Memory) ListAttachmentIDs(_ context.Context, conversationID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var ids []string
	for id, a := range m.attachments {
		if a.ConversationID == conversationID {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *Memory) DeleteAttachments(_ context.Context, conversationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, a := range m.attachments {
		if a.ConversationID == conversationID {
			delete(m.attachments, id)
		}
	}
	return nil
}
