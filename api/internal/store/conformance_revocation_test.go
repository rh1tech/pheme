package store

import (
	"context"
	"testing"
	"time"
)

// Session revocation: the deny list behind "terminate this device", run against BOTH stores.
//
// Auth tokens are stateless, which is what makes them cheap and what makes this necessary. Removing
// a device severs its crypto immediately, but the access token in its hands stays validly signed
// and unexpired until it runs out — unless something remembers that its session is over. That
// something is this, and it has to survive a restart, because the in-memory deny set is rebuilt
// from here at startup: a server that forgot on restart would hand a terminated device its access
// back at the next deploy.
//
// Two mechanisms, because one of them cannot reach every device. Per-session revocation is exact,
// and works for anything issued since session ids were recorded. For a device older than that there
// is no session id to deny, so the only thing that reaches it is refusing everything that user held
// before a cutoff.

func TestConformance_ARevokedSessionIsRemembered(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		future := time.Now().Add(time.Hour)

		if err := s.store.RevokeSession(ctx, "sess-1", future); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		active, err := s.store.ActiveRevokedSessions(ctx, time.Now())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !contains(active, "sess-1") {
			t.Errorf("a revoked session is not in the deny list (%v); after the next restart the "+
				"terminated device gets its access back", active)
		}
	})
}

// An entry past its expiry is not returned. The token it denies has expired on its own by then, so
// keeping it would grow the list forever for no benefit.
func TestConformance_ARevocationStopsMatteringWhenTheTokenWouldHaveExpired(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		past := time.Now().Add(-time.Hour)

		if err := s.store.RevokeSession(ctx, "sess-expired", past); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		active, err := s.store.ActiveRevokedSessions(ctx, time.Now())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if contains(active, "sess-expired") {
			t.Errorf("an expired revocation is still listed (%v); the deny list grows without "+
				"bound", active)
		}
	})
}

// Revoking the same session twice is idempotent and extends the denial. A retry after a dropped
// response, or terminating an already-terminated device, must leave it denied — and denied for at
// least as long as the newer call asked.
//
// Note what is deliberately NOT asserted: that a SHORTER second expiry is ignored. Both stores are
// last-write-wins here, and so is the in-memory deny set that actually gates requests
// (SessionRevoker.Revoke assigns rather than taking a maximum). That is consistent, and it is safe
// because the caller always passes now+SessionTTL, so successive calls only ever move the expiry
// forward. Asserting max-semantics would be inventing a requirement the system does not have and
// the caller cannot trigger — the per-USER cutoff is the one that genuinely needs it, because
// moving a cutoff backwards would un-revoke tokens an earlier call already refused, and the
// revoker guards that explicitly.
func TestConformance_RevokingTwiceExtendsTheDenial(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		if err := s.store.RevokeSession(ctx, "sess-2", time.Now().Add(time.Minute)); err != nil {
			t.Fatalf("first revoke: %v", err)
		}
		if err := s.store.RevokeSession(ctx, "sess-2", time.Now().Add(2*time.Hour)); err != nil {
			t.Fatalf("second revoke: %v", err)
		}

		// Denied now, and still denied well past what the first call alone would have covered.
		for _, at := range []time.Time{time.Now(), time.Now().Add(30 * time.Minute)} {
			active, err := s.store.ActiveRevokedSessions(ctx, at)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if !contains(active, "sess-2") {
				t.Errorf("at %v the session is not denied (%v); re-revoking lost the revocation "+
					"entirely", at, active)
			}
			// Once, not twice — a duplicated entry would grow the deny list on every retry.
			n := 0
			for _, id := range active {
				if id == "sess-2" {
					n++
				}
			}
			if n > 1 {
				t.Errorf("the session appears %d times in the deny list; a retry duplicates the "+
					"entry rather than updating it", n)
			}
		}
	})
}

// Revocation is per session. Terminating one device must not sign the person out everywhere.
func TestConformance_RevokingOneSessionLeavesTheOthers(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		future := time.Now().Add(time.Hour)

		if err := s.store.RevokeSession(ctx, "phone-session", future); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		active, err := s.store.ActiveRevokedSessions(ctx, time.Now())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if contains(active, "laptop-session") {
			t.Error("revoking one session denied another that was never revoked; terminating one " +
				"device signs the person out everywhere")
		}
	})
}

// THE ONE FOR DEVICES TOO OLD TO HAVE A SESSION ID. Refusing everything a user held before a cutoff
// is the only thing that reaches them.
func TestConformance_EveryTokenAUserHeldBeforeACutoffCanBeRefused(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()
		cutoff := time.Now()
		expires := time.Now().Add(time.Hour)

		if err := s.store.RevokeUserTokensBefore(ctx, "user-1", cutoff, expires); err != nil {
			t.Fatalf("revoke user: %v", err)
		}

		revocations, err := s.store.ActiveUserRevocations(ctx, time.Now())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		got, ok := revocations["user-1"]
		if !ok {
			t.Fatalf("the user's cutoff is not in force (%v); a device with no recorded session "+
				"could never be signed out", revocations)
		}
		// Within a second: the two stores round times differently, and the cutoff only has to be
		// accurate enough to separate tokens issued before it from tokens issued after.
		if got.Sub(cutoff) > time.Second || cutoff.Sub(got) > time.Second {
			t.Errorf("cutoff came back as %v, want about %v", got, cutoff)
		}

		// Somebody else is unaffected.
		if _, ok := revocations["user-2"]; ok {
			t.Error("revoking one user's tokens affected another")
		}
	})
}

// An expired user-wide revocation stops applying, for the same reason a session one does.
func TestConformance_AnExpiredUserRevocationStopsApplying(t *testing.T) {
	eachStore(t, func(t *testing.T, s storeUnderTest) {
		ctx := context.Background()

		if err := s.store.RevokeUserTokensBefore(ctx, "user-old", time.Now().Add(-2*time.Hour),
			time.Now().Add(-time.Hour)); err != nil {
			t.Fatalf("revoke user: %v", err)
		}

		revocations, err := s.store.ActiveUserRevocations(ctx, time.Now())
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if _, ok := revocations["user-old"]; ok {
			t.Errorf("an expired user revocation is still in force (%v)", revocations)
		}
	})
}

// contains is a small local helper; the store package has no test utilities of its own.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
