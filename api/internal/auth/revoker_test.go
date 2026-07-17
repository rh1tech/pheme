package auth

import (
	"context"
	"testing"
	"time"
)

// fakeRevocationStore is an in-memory stand-in for the persistence a SessionRevoker needs.
type fakeRevocationStore struct {
	revoked map[string]time.Time
}

func newFakeStore() *fakeRevocationStore {
	return &fakeRevocationStore{revoked: map[string]time.Time{}}
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
