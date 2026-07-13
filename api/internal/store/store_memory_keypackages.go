package store

import (
	"context"

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

// ClaimKeyPackage hands out one of a user's KeyPackages. A single-use package is
// removed; the last-resort one is reused rather than consumed, so a caller looping
// on this endpoint can never leave the user without a way to be added to a group.
func (m *Memory) ClaimKeyPackage(_ context.Context, userID string) (domain.MLSKeyPackage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastResort *domain.MLSKeyPackage
	for id, kp := range m.keyPackages {
		if kp.UserID != userID {
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
