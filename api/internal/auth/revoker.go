package auth

import (
	"context"
	"sync"
	"time"
)

// SessionRevocationStore is the persistence a SessionRevoker needs. store.Store satisfies
// it structurally; the revoker names only what it uses so it does not depend on the whole
// store package.
type SessionRevocationStore interface {
	// RevokeSession records that a session is revoked until the given expiry (the point
	// after which its token would be rejected for expiry anyway).
	RevokeSession(ctx context.Context, sessionID string, expiresAt time.Time) error
	// ActiveRevokedSessions lists the session ids still revoked as of now — those whose
	// expiry has not passed. Used to hydrate the in-memory set at startup.
	ActiveRevokedSessions(ctx context.Context, now time.Time) ([]string, error)

	// RevokeUserTokensBefore records that EVERY token this user holds which was issued
	// before cutoff is refused, until expiresAt.
	RevokeUserTokensBefore(ctx context.Context, userID string, cutoff, expiresAt time.Time) error
	// ActiveUserRevocations lists the per-user cutoffs still in force, to hydrate at startup.
	ActiveUserRevocations(ctx context.Context, now time.Time) (map[string]time.Time, error)
}

// SessionRevoker answers "is this session revoked?" from memory, so the auth middleware
// pays a map lookup per request rather than a database round-trip. Revocations are rare
// and few (only terminated devices, and only until their tokens expire), so the whole set
// fits comfortably in memory. It is write-through: a revocation is persisted first, then
// cached, so a restart re-hydrates the same set.
type SessionRevoker struct {
	store SessionRevocationStore
	mu    sync.RWMutex
	// sid -> expiry. An entry past its expiry is stale (the token is rejected on expiry
	// regardless) and is pruned lazily on lookup.
	revoked map[string]time.Time
	// userID -> cutoff. Every token for that user issued BEFORE the cutoff is refused.
	//
	// This exists for the one case a session id cannot cover: a device registered before
	// session ids were recorded has none, so there is nothing for a revocation to match and
	// "terminate this device" could not sign it out at all. Ever. That device kept working API
	// access, which is the difference between a stranded MLS leaf being harmless and being a way
	// to go on reading a group.
	//
	// Blunt on purpose. Unable to name the one session, it ends them all for that user and
	// everything re-authenticates, which is the honest response to "remove a device I cannot
	// individually identify".
	userCutoff map[string]time.Time
}

// NewSessionRevoker builds a revoker backed by store. Call Hydrate before serving.
func NewSessionRevoker(store SessionRevocationStore) *SessionRevoker {
	return &SessionRevoker{
		store:      store,
		revoked:    make(map[string]time.Time),
		userCutoff: make(map[string]time.Time),
	}
}

// Hydrate loads the still-active revocations from the store into memory. Called once at
// startup so revocations survive a restart.
func (r *SessionRevoker) Hydrate(ctx context.Context) error {
	sids, err := r.store.ActiveRevokedSessions(ctx, time.Now())
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Hydration has no per-entry expiry to hand back, only that each is still active; a
	// far-future expiry keeps it in the set until the next restart re-reads the store,
	// which is where the authoritative reap lives.
	far := time.Now().Add(365 * 24 * time.Hour)
	for _, sid := range sids {
		r.revoked[sid] = far
	}
	return nil
}

// HydrateUsers loads the still-active per-user cutoffs. Separate from Hydrate so an older
// deployment with no such records is unaffected.
func (r *SessionRevoker) HydrateUsers(ctx context.Context) error {
	cutoffs, err := r.store.ActiveUserRevocations(ctx, time.Now())
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for userID, cutoff := range cutoffs {
		r.userCutoff[userID] = cutoff
	}
	return nil
}

// RevokeUserBefore refuses every token this user holds that was issued before cutoff.
//
// The heavy hammer, for when the light one cannot reach: a device with no recorded session id
// cannot be signed out individually, so the only way to end its access is to end all of them.
func (r *SessionRevoker) RevokeUserBefore(ctx context.Context, userID string, cutoff, expiresAt time.Time) error {
	if userID == "" {
		return nil
	}
	if err := r.store.RevokeUserTokensBefore(ctx, userID, cutoff, expiresAt); err != nil {
		return err
	}
	r.mu.Lock()
	// Never move a cutoff BACKWARDS: a later revocation must not un-revoke what an earlier one
	// already refused.
	if existing, ok := r.userCutoff[userID]; !ok || cutoff.After(existing) {
		r.userCutoff[userID] = cutoff
	}
	r.mu.Unlock()
	return nil
}

// IsUserRevoked reports whether a token for this user, issued at issuedAt, has been refused by
// a per-user revocation.
func (r *SessionRevoker) IsUserRevoked(userID string, issuedAt time.Time) bool {
	if userID == "" || issuedAt.IsZero() {
		return false
	}
	r.mu.RLock()
	cutoff, ok := r.userCutoff[userID]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	return issuedAt.Before(cutoff)
}

// Revoke marks a session revoked until expiresAt: persisted first (so it outlives a
// restart), then cached. A blank session id is a no-op — nothing to deny.
func (r *SessionRevoker) Revoke(ctx context.Context, sessionID string, expiresAt time.Time) error {
	if sessionID == "" {
		return nil
	}
	if err := r.store.RevokeSession(ctx, sessionID, expiresAt); err != nil {
		return err
	}
	r.mu.Lock()
	r.revoked[sessionID] = expiresAt
	r.mu.Unlock()
	return nil
}

// IsRevoked reports whether a session is currently revoked. A stale entry (its expiry
// passed) is treated as not revoked and dropped, so the set does not grow without bound.
func (r *SessionRevoker) IsRevoked(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	r.mu.RLock()
	exp, ok := r.revoked[sessionID]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		r.mu.Lock()
		delete(r.revoked, sessionID)
		r.mu.Unlock()
		return false
	}
	return true
}
