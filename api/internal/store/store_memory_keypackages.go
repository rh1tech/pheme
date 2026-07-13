package store

import (
	"context"

	"github.com/rh1tech/pheme/api/internal/domain"
)

func (m *Memory) AddKeyPackages(_ context.Context, packages []domain.MLSKeyPackage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, kp := range packages {
		if kp.ID == "" {
			kp.ID = newID()
		}
		// The first KeyPackage a device ever publishes becomes its last resort, so
		// the device can never be drained to zero by repeated claims.
		if !m.hasLastResortLocked(kp.UserID, kp.DeviceID) {
			kp.LastResort = true
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
