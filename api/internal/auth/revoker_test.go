package auth

import (
	"context"
	"testing"
	"time"
)

// fakeRevocationStore is an in-memory stand-in for the persistence a SessionRevoker needs.
type fakeRevocationStore struct {
	revoked   map[string]time.Time
	userCut   map[string]time.Time
	userUntil map[string]time.Time
}

func newFakeStore() *fakeRevocationStore {
	return &fakeRevocationStore{
		revoked:   map[string]time.Time{},
		userCut:   map[string]time.Time{},
		userUntil: map[string]time.Time{},
	}
}

func (s *fakeRevocationStore) RevokeUserTokensBefore(_ context.Context, userID string, cutoff, exp time.Time) error {
	s.userCut[userID] = cutoff
	s.userUntil[userID] = exp
	return nil
}

func (s *fakeRevocationStore) ActiveUserRevocations(_ context.Context, now time.Time) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	for uid, cutoff := range s.userCut {
		if s.userUntil[uid].After(now) {
			out[uid] = cutoff
		}
	}
	return out, nil
}

func (s *fakeRevocationStore) RevokeSession(_ context.Context, sid string, exp time.Time) error {
	s.revoked[sid] = exp
	return nil
}

func (s *fakeRevocationStore) ActiveRevokedSessions(_ context.Context, now time.Time) ([]string, error) {
	out := []string{}
	for sid, exp := range s.revoked {
		if exp.After(now) {
			out = append(out, sid)
		}
	}
	return out, nil
}

func TestRevokerRevokeThenIsRevoked(t *testing.T) {
	store := newFakeStore()
	r := NewSessionRevoker(store)
	if r.IsRevoked("sid-1") {
		t.Fatal("nothing revoked yet")
	}
	if err := r.Revoke(context.Background(), "sid-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !r.IsRevoked("sid-1") {
		t.Fatal("sid-1 should be revoked")
	}
	// Persisted, so a fresh revoker hydrated from the same store sees it too.
	if _, ok := store.revoked["sid-1"]; !ok {
		t.Fatal("revocation should be written through to the store")
	}
}

func TestRevokerBlankSessionIsNoop(t *testing.T) {
	r := NewSessionRevoker(newFakeStore())
	if err := r.Revoke(context.Background(), "", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("revoke blank: %v", err)
	}
	if r.IsRevoked("") {
		t.Fatal("a blank session id is never revoked")
	}
}

func TestRevokerHydratesActiveOnly(t *testing.T) {
	store := newFakeStore()
	store.revoked["live"] = time.Now().Add(time.Hour)
	store.revoked["expired"] = time.Now().Add(-time.Hour)

	r := NewSessionRevoker(store)
	if err := r.Hydrate(context.Background()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if !r.IsRevoked("live") {
		t.Fatal("a still-active revocation should hydrate")
	}
	if r.IsRevoked("expired") {
		t.Fatal("an expired revocation should not hydrate — its token is rejected on expiry anyway")
	}
}

func TestRevokerDropsStaleEntryOnLookup(t *testing.T) {
	r := NewSessionRevoker(newFakeStore())
	// Revoke with an expiry already in the past: the lookup must treat it as not revoked and
	// drop it, so the set does not grow without bound.
	if err := r.Revoke(context.Background(), "stale", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if r.IsRevoked("stale") {
		t.Fatal("an entry past its expiry must read as not revoked")
	}
}

// A token refresh must keep the same session id, so terminating the device still revokes the
// login the device has refreshed into.
func TestRefreshPreservesSessionID(t *testing.T) {
	m := NewTokenManager("secret", time.Minute, time.Hour)
	_, refresh, sid, err := m.Issue("user-1", "user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := m.ParseClaims(refresh, RefreshToken)
	if err != nil {
		t.Fatalf("parse refresh: %v", err)
	}
	if claims.SID != sid {
		t.Fatalf("issued refresh SID %q != returned sid %q", claims.SID, sid)
	}
	// Re-issue under the same session (what the refresh handler does) and confirm the new
	// access token carries the SAME session id.
	access2, _, err := m.IssueWithSession("user-1", "user", sid)
	if err != nil {
		t.Fatalf("re-issue: %v", err)
	}
	c2, err := m.ParseClaims(access2, AccessToken)
	if err != nil {
		t.Fatalf("parse access2: %v", err)
	}
	if c2.SID != sid {
		t.Fatalf("refreshed access SID %q != original %q", c2.SID, sid)
	}
}

// Two logins get two distinct sessions, so terminating one cannot take out the other.
func TestIssueMintsDistinctSessions(t *testing.T) {
	m := NewTokenManager("secret", time.Minute, time.Hour)
	_, _, sid1, _ := m.Issue("user-1", "user")
	_, _, sid2, _ := m.Issue("user-1", "user")
	if sid1 == "" || sid2 == "" {
		t.Fatal("sessions must have ids")
	}
	if sid1 == sid2 {
		t.Fatal("two logins must get distinct session ids")
	}
}

// The case a session id cannot reach.
//
// A device registered before session ids were recorded has none, so no per-session revocation can
// ever match it — "terminate this device" could not sign it out at all, and it kept working API
// access indefinitely. That is the difference between a stranded MLS leaf being inert and being a
// way to go on reading a group.
func TestUserRevocationEndsTokensWithNoSessionID(t *testing.T) {
	r := NewSessionRevoker(newFakeStore())
	now := time.Now()
	issued := now.Add(-time.Hour)

	if r.IsUserRevoked("u1", issued) {
		t.Fatal("revoked before anything was revoked")
	}
	if err := r.RevokeUserBefore(context.Background(), "u1", now, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if !r.IsUserRevoked("u1", issued) {
		t.Error("a token issued before the cutoff was still accepted")
	}
	// A token issued AFTER the cutoff is the user signing back in, and must work.
	if r.IsUserRevoked("u1", now.Add(time.Minute)) {
		t.Error("a token issued after the cutoff was refused; the user could never sign in again")
	}
	// Nobody else is affected.
	if r.IsUserRevoked("u2", issued) {
		t.Error("another user's tokens were caught by this revocation")
	}
}

// A later revocation must never move the cutoff backwards, or it would un-revoke tokens an
// earlier one had already refused.
func TestUserRevocationCutoffNeverMovesBackwards(t *testing.T) {
	r := NewSessionRevoker(newFakeStore())
	now := time.Now()
	ctx := context.Background()

	_ = r.RevokeUserBefore(ctx, "u1", now, now.Add(24*time.Hour))
	_ = r.RevokeUserBefore(ctx, "u1", now.Add(-time.Hour), now.Add(24*time.Hour))

	if !r.IsUserRevoked("u1", now.Add(-time.Minute)) {
		t.Error("an earlier revocation was undone by a later one with a lower cutoff")
	}
}

// Revocations have to survive a restart, or terminating a device is undone by a deploy.
func TestUserRevocationSurvivesHydrate(t *testing.T) {
	store := newFakeStore()
	now := time.Now()
	_ = NewSessionRevoker(store).RevokeUserBefore(context.Background(), "u1", now, now.Add(24*time.Hour))

	fresh := NewSessionRevoker(store)
	if err := fresh.HydrateUsers(context.Background()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if !fresh.IsUserRevoked("u1", now.Add(-time.Hour)) {
		t.Error("the revocation did not survive a restart")
	}
}
