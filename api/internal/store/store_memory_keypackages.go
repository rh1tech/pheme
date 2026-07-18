package store

import (
	"context"
	"sort"
	"time"

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

// SetMLSGroupInfo records the latest GroupInfo a joiner can external-join against.
//
// Only kept if it is for the CURRENT group and at least as new as what is stored — an older export
// arriving late (or one for a group since retired) must never overwrite fresher material.
func (m *Memory) SetMLSGroupInfo(
	_ context.Context, conversationID, groupID string, epoch int64, groupInfo []byte,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conversations[conversationID]
	if !ok {
		return ErrNotFound
	}
	if c.MLS.GroupID != groupID {
		return nil // for a group that is not current; ignore rather than store the wrong material
	}
	if cur, ok := m.mlsGroupInfo[conversationID]; ok && cur.GroupID == groupID && cur.Epoch >= epoch {
		return nil
	}
	m.mlsGroupInfo[conversationID] = domain.MLSGroupInfo{
		GroupID: groupID, Epoch: epoch, GroupInfo: groupInfo,
	}
	return nil
}

// MLSGroupInfo returns the latest stored GroupInfo, or ErrNotFound if none — which is a real answer:
// a group whose members have not published one yet cannot be external-joined, and the caller falls
// back to announcing itself.
func (m *Memory) MLSGroupInfo(_ context.Context, conversationID string) (domain.MLSGroupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	gi, ok := m.mlsGroupInfo[conversationID]
	if !ok || len(gi.GroupInfo) == 0 {
		return domain.MLSGroupInfo{}, ErrNotFound
	}
	// If the group has since been retired, the stored info is stale and useless.
	if c, ok := m.conversations[conversationID]; ok && c.MLS.GroupID != gi.GroupID {
		return domain.MLSGroupInfo{}, ErrNotFound
	}
	return gi, nil
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

func mlsDeviceKey(userID, deviceID string) string { return userID + ":" + deviceID }

func (m *Memory) UpsertMLSDevice(_ context.Context, d domain.MLSDevice) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := mlsDeviceKey(d.UserID, d.DeviceID)
	existing, ok := m.mlsDevices[key]
	if ok {
		existing.Label = d.Label
		existing.LastSeenAt = d.LastSeenAt
		// Keep the session id current so a later "terminate this device" revokes the login
		// the device is actually using. Only overwrite when the caller supplied one — a
		// blank must not erase a known session.
		if d.SessionID != "" {
			existing.SessionID = d.SessionID
		}
		m.mlsDevices[key] = existing
		return nil
	}
	if d.ID == "" {
		d.ID = newID()
	}
	m.mlsDevices[key] = d
	return nil
}

func (m *Memory) ListMLSDevices(_ context.Context, userID string) ([]domain.MLSDevice, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.MLSDevice, 0)
	for _, d := range m.mlsDevices {
		if d.UserID == userID {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeenAt.After(out[j].LastSeenAt) })
	return out, nil
}

func (m *Memory) DeleteMLSDevice(_ context.Context, userID, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.mlsDevices, mlsDeviceKey(userID, deviceID))
	return nil
}

func (m *Memory) DeletePushDevicesForMLSDevice(_ context.Context, userID, mlsDeviceID string) (int64, error) {
	if mlsDeviceID == "" {
		// A blank id would match every legacy row for this user: the account, not the one device.
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for id, d := range m.devices {
		if d.UserID == userID && d.MLSDeviceID == mlsDeviceID {
			delete(m.devices, id)
			removed++
		}
	}
	return removed, nil
}

func (m *Memory) RevokeMLSDevice(_ context.Context, userID, deviceID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := mlsDeviceKey(userID, deviceID)
	d, ok := m.mlsDevices[key]
	if !ok {
		return ErrNotFound
	}
	d.RevokedAt = &at
	m.mlsDevices[key] = d
	return nil
}

func (m *Memory) RevokedDeviceIDs(_ context.Context, userIDs []string) (map[string][]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	wanted := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		wanted[id] = struct{}{}
	}
	out := make(map[string][]string, len(userIDs))
	for _, d := range m.mlsDevices {
		if d.RevokedAt == nil {
			continue
		}
		if _, ok := wanted[d.UserID]; !ok {
			continue
		}
		out[d.UserID] = append(out[d.UserID], d.DeviceID)
	}
	return out, nil
}

func (m *Memory) RevokeUserTokensBefore(_ context.Context, userID string, cutoff, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revokedUsers == nil {
		m.revokedUsers = map[string]userRevocation{}
	}
	m.revokedUsers[userID] = userRevocation{Cutoff: cutoff, ExpiresAt: expiresAt}
	return nil
}

func (m *Memory) ActiveUserRevocations(_ context.Context, now time.Time) (map[string]time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]time.Time{}
	for userID, rev := range m.revokedUsers {
		if rev.ExpiresAt.After(now) {
			out[userID] = rev.Cutoff
		}
	}
	return out, nil
}

func (m *Memory) DeleteDevice(_ context.Context, deviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.devices, deviceID)
	return nil
}

func (m *Memory) RevokeSession(_ context.Context, sessionID string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.revokedSessions == nil {
		m.revokedSessions = make(map[string]time.Time)
	}
	m.revokedSessions[sessionID] = expiresAt
	return nil
}

func (m *Memory) ActiveRevokedSessions(_ context.Context, now time.Time) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.revokedSessions))
	for sid, exp := range m.revokedSessions {
		if exp.After(now) {
			out = append(out, sid)
		}
	}
	return out, nil
}

// How many retired groups a conversation remembers. Each one is a group somebody might still
// hold and still be reading history from; a handful is generous, and it stops an abusive
// client growing the document without bound.
const maxPriorGroups = 8

// ResetMLSGroup retires the current group so a new one can be established. See the Store
// interface: the old group is remembered, not deleted, so nothing anyone still holds is lost.
func (m *Memory) ResetMLSGroup(_ context.Context, conversationID string) (domain.MLSGroupState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.conversations[conversationID]
	if !ok {
		return domain.MLSGroupState{}, ErrNotFound
	}
	if c.MLS.GroupID == "" {
		return c.MLS, nil // nothing established; nothing to retire
	}

	prior := append([]string{c.MLS.GroupID}, c.MLS.PriorGroupIDs...)
	if len(prior) > maxPriorGroups {
		prior = prior[:maxPriorGroups]
	}
	c.MLS = domain.MLSGroupState{PriorGroupIDs: prior}
	m.conversations[conversationID] = c
	return c.MLS, nil
}

// userRevocation is a per-user token cutoff: every token issued before Cutoff is refused, until
// ExpiresAt (past which the tokens would be rejected for expiry anyway).
type userRevocation struct {
	Cutoff    time.Time
	ExpiresAt time.Time
}
