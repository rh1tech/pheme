package store

import (
	"context"
	"sort"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// AddKeyPackages stores exactly what it is given. Whether a package is last-resort
// is decided by the client that built it (an RFC 9420 extension in the bytes) — the
// server only records the fact, it cannot confer it.
func (m *Memory) AddKeyPackages(_ context.Context, packages []domain.MLSKeyPackage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, kp := range packages {
		// One last-resort package per device, enforced here rather than trusting the
		// handler's check-then-insert — two concurrent publishes can both pass that.
		// Mirrors the partial unique index the Mongo store relies on.
		if kp.LastResort && m.hasLastResortLocked(kp.UserID, kp.DeviceID) {
			continue
		}
		if kp.ID == "" {
			kp.ID = newID()
		}
		m.keyPackages[kp.ID] = kp
	}
	return nil
}

func (m *Memory) hasLastResortLocked(userID, deviceID string) bool {
	for _, kp := range m.keyPackages {
		if kp.UserID == userID && kp.DeviceID == deviceID && kp.LastResort {
			return true
		}
	}
	return false
}

func (m *Memory) DeleteKeyPackages(_ context.Context, userID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, kp := range m.keyPackages {
		if kp.UserID == userID && kp.DeviceID == deviceID {
			delete(m.keyPackages, id)
		}
	}
	return nil
}

func (m *Memory) HasLastResortKeyPackage(_ context.Context, userID, deviceID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, kp := range m.keyPackages {
		if kp.UserID == userID && kp.DeviceID == deviceID && kp.LastResort {
			return true, nil
		}
	}
	return false, nil
}

// ClaimKeyPackage hands out one KeyPackage belonging to ONE DEVICE of a user. A
// single-use package is removed; the last-resort one is reused rather than consumed,
// so a caller looping on this endpoint can never leave a device unreachable.
//
// Scoped to a device, because an MLS leaf is a device: a claim that ignores deviceId
// returns some arbitrary device of that user, and the group built from it locks every
// other device they own out of the conversation.
func (m *Memory) ClaimKeyPackage(_ context.Context, userID, deviceID string) (domain.MLSKeyPackage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastResort *domain.MLSKeyPackage
	for id, kp := range m.keyPackages {
		if kp.UserID != userID || kp.DeviceID != deviceID {
			continue
		}
		if kp.LastResort {
			found := kp
			lastResort = &found
			continue
		}
		delete(m.keyPackages, id) // single-use: consumed
		return kp, nil
	}
	if lastResort != nil {
		return *lastResort, nil
	}
	return domain.MLSKeyPackage{}, ErrNotFound
}

// DevicesWithKeyPackages lists each user's publishing devices, consuming nothing —
// the question "which devices are missing from this group?" cannot be answered by
// claiming, because claiming destroys what it returns.
func (m *Memory) DevicesWithKeyPackages(_ context.Context, userIDs []string) (map[string][]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	want := make(map[string]bool, len(userIDs))
	for _, id := range userIDs {
		want[id] = true
	}
	seen := make(map[string]map[string]bool)
	for _, kp := range m.keyPackages {
		if !want[kp.UserID] || kp.DeviceID == "" {
			continue
		}
		if seen[kp.UserID] == nil {
			seen[kp.UserID] = make(map[string]bool)
		}
		seen[kp.UserID][kp.DeviceID] = true
	}

	out := make(map[string][]string, len(seen))
	for userID, devices := range seen {
		list := make([]string, 0, len(devices))
		for deviceID := range devices {
			list = append(list, deviceID)
		}
		sort.Strings(list) // map iteration is random; callers diff these
		out[userID] = list
	}
	return out, nil
}

// MLSControlMessagesSince returns the Welcomes and Commits past `sinceEpoch`, oldest
// first — the order a member catching up must apply them in.
func (m *Memory) MLSControlMessagesSince(_ context.Context, conversationID string, sinceEpoch int64) ([]domain.ChatMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []domain.ChatMessage
	for _, msg := range m.chatMessages {
		if msg.ConversationID != conversationID || msg.MLSEpoch <= sinceEpoch {
			continue
		}
		out = append(out, msg)
	}
	// By epoch, then by type: within one Commit the Welcome must come first, or a device
	// being admitted would see the Commit for a group it has not joined yet.
	sort.Slice(out, func(i, j int) bool {
		if out[i].MLSEpoch != out[j].MLSEpoch {
			return out[i].MLSEpoch < out[j].MLSEpoch
		}
		return out[i].ContentType == domain.ContentTypeMLSWelcome
	})
	return out, nil
}

// MLSGroupState reads the conversation's MLS group id and epoch.
func (m *Memory) MLSGroupState(_ context.Context, conversationID string) (domain.MLSGroupState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.conversations[conversationID]
	if !ok {
		return domain.MLSGroupState{}, ErrNotFound
	}
	return c.MLS, nil
}

// CommitMLSGroup is the compare-and-set that keeps every member on one group history,
// and relays the Commit in the same step. See the Store interface for why the two
// cannot be separated.
//
// The whole read-decide-write-append happens under one write lock, which is what makes
// it a compare-and-set rather than a check followed by a hopeful update.
func (m *Memory) CommitMLSGroup(
	_ context.Context, conversationID, groupID string, baseEpoch int64, msgs []domain.ChatMessage,
) (domain.MLSGroupState, []domain.ChatMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.conversations[conversationID]
	if !ok {
		return domain.MLSGroupState{}, nil, ErrNotFound
	}

	establishing := baseEpoch == 0 && c.MLS.GroupID == ""
	advancing := c.MLS.GroupID == groupID && c.MLS.Epoch == baseEpoch
	if !establishing && !advancing {
		// Somebody else got there first. Hand back what they left, so the caller can
		// catch up and re-propose rather than forcing a Commit the group has refused.
		return c.MLS, nil, ErrEpochConflict
	}

	c.MLS = domain.MLSGroupState{GroupID: groupID, Epoch: baseEpoch + 1}
	m.conversations[conversationID] = c

	stored := make([]domain.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		stored = append(stored, m.appendChatMessageLocked(msg))
	}
	return c.MLS, stored, nil
}

// CountKeyPackages counts only the consumable packages. The last-resort one is
// never used up, so counting it would tell the client it has stock it does not
// have, and it would stop replenishing.
func (m *Memory) CountKeyPackages(_ context.Context, userID, deviceID string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var n int64
	for _, kp := range m.keyPackages {
		if kp.UserID == userID && kp.DeviceID == deviceID && !kp.LastResort {
			n++
		}
	}
	return n, nil
}

func (m *Memory) PutKeyBackup(_ context.Context, backup domain.MLSKeyBackup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if backup.ID == "" {
		backup.ID = newID()
	}
	m.keyBackups[backup.UserID] = backup // one per user; the latest wins
	return nil
}

func (m *Memory) GetKeyBackup(_ context.Context, userID string) (domain.MLSKeyBackup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.keyBackups[userID]
	if !ok {
		return domain.MLSKeyBackup{}, ErrNotFound
	}
	return b, nil
}
