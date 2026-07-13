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
		m.keyPackages[kp.ID] = kp
	}
	return nil
}

func (m *Memory) ClaimKeyPackage(_ context.Context, userID string) (domain.MLSKeyPackage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Any of the user's packages; each is single-use, so remove the one returned.
	for id, kp := range m.keyPackages {
		if kp.UserID == userID {
			delete(m.keyPackages, id)
			return kp, nil
		}
	}
	return domain.MLSKeyPackage{}, ErrNotFound
}

func (m *Memory) CountKeyPackages(_ context.Context, userID, deviceID string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var n int64
	for _, kp := range m.keyPackages {
		if kp.UserID == userID && kp.DeviceID == deviceID {
			n++
		}
	}
	return n, nil
}
