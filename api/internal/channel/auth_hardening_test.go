package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/ratelimit"
)

// The finding: login had no throttle at all, so password guessing was unlimited —
// and each guess costs an Argon2id hash, so it was also a way to starve honest
// logins of the hash-slot pool.
func TestLoginIsRateLimited(t *testing.T) {
	h, _, mux := newTestAuth()
	// Three attempts, then a very slow drip.
	h.Limiter = ratelimit.NewTokenBucket(1.0/600.0, 3)

	if _, err := h.Store.CreateUser(context.Background(), activeUser(t, "a@b.com")); err != nil {
		t.Fatal(err)
	}

	var throttled bool
	for i := 0; i < 10; i++ {
		rec := post(mux, "/v1/auth/login", map[string]string{"email": "a@b.com", "password": "wrong-guess"})
		if rec.Code == http.StatusTooManyRequests {
			throttled = true
			break
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401 or 429; body=%s", i, rec.Code, rec.Body)
		}
	}
	if !throttled {
		t.Fatal("ten wrong passwords in a row were all served — login is not throttled")
	}
}

// The per-account bucket is the one that bounds guessing against a target, so it
// must bite regardless of where the guesses come from. A per-IP-only limit is
// defeated by any attacker with more than one address.
func TestLoginThrottleFollowsTheAccountNotTheAddress(t *testing.T) {
	h, _, _ := newTestAuth()
	h.Limiter = ratelimit.NewTokenBucket(1.0/600.0, 3)
	mux := http.NewServeMux()
	h.Routes(mux)

	if _, err := h.Store.CreateUser(context.Background(), activeUser(t, "a@b.com")); err != nil {
		t.Fatal(err)
	}

	// Every attempt from a DIFFERENT source address.
	var throttled bool
	for i := 0; i < 10; i++ {
		rec := postFrom(mux, "/v1/auth/login",
			map[string]string{"email": "a@b.com", "password": "wrong-guess"},
			"203.0.113."+itoa(i)+":5000")
		if rec.Code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("guesses from rotating addresses were never throttled — the account bucket is not applied")
	}
}

// A throttle must not become an oracle: the response to a limited request must
// not reveal whether the account exists.
func TestThrottleDoesNotRevealAccountExistence(t *testing.T) {
	h, _, mux := newTestAuth()
	h.Limiter = ratelimit.NewTokenBucket(1.0/600.0, 1)
	if _, err := h.Store.CreateUser(context.Background(), activeUser(t, "real@b.com")); err != nil {
		t.Fatal(err)
	}

	drain := func(email string) *httptest.ResponseRecorder {
		var last *httptest.ResponseRecorder
		for i := 0; i < 4; i++ {
			last = post(mux, "/v1/auth/login", map[string]string{"email": email, "password": "nope"})
		}
		return last
	}
	real, ghost := drain("real@b.com"), drain("ghost@b.com")
	if real.Code != ghost.Code || real.Body.String() != ghost.Body.String() {
		t.Errorf("a throttled real account and a throttled unknown one differ:\n real=%d %s\nghost=%d %s",
			real.Code, real.Body, ghost.Code, ghost.Body)
	}
}

// The finding: refresh checked only IsRevoked(sid). A device with no session id —
// the exact case the per-user cutoff exists for — could trade its still-valid
// refresh token for an access token stamped AFTER the cutoff, which the middleware
// then admitted. Terminating such a device did nothing.
func TestRefreshHonoursThePerUserRevocationCutoff(t *testing.T) {
	h, _, mux := newTestAuth()
	u, err := h.Store.CreateUser(context.Background(), activeUser(t, "cutoff@b.com"))
	if err != nil {
		t.Fatal(err)
	}

	// A device with NO session id — the only kind the per-user cutoff can reach.
	_, refresh, err := h.Tokens.IssueWithSession(u.ID, "user", "")
	if err != nil {
		t.Fatal(err)
	}
	// The operator terminates it: every token issued before now is refused.
	h.Revoker = &cutoffRevoker{cutoff: time.Now().Add(2 * time.Second)}

	rec := post(mux, "/v1/auth/refresh", map[string]string{"refreshToken": refresh})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — a revoked device refreshed its way back in; body=%s",
			rec.Code, rec.Body)
	}
}

// …and a session issued after the cutoff still refreshes, so the blunt instrument
// stays bounded to what it was aimed at.
func TestRefreshStillWorksAfterTheCutoffPasses(t *testing.T) {
	h, _, mux := newTestAuth()
	h.Revoker = &cutoffRevoker{cutoff: time.Now().Add(-time.Hour)}
	u, err := h.Store.CreateUser(context.Background(), activeUser(t, "live-cutoff@b.com"))
	if err != nil {
		t.Fatal(err)
	}

	_, refresh, err := h.Tokens.IssueWithSession(u.ID, "user", "sid-1")
	if err != nil {
		t.Fatal(err)
	}
	rec := post(mux, "/v1/auth/refresh", map[string]string{"refreshToken": refresh})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a live session was refused; body=%s", rec.Code, rec.Body)
	}
}

type cutoffRevoker struct{ cutoff time.Time }

func (c *cutoffRevoker) IsRevoked(string) bool { return false }
func (c *cutoffRevoker) IsUserRevoked(_ string, issuedAt time.Time) bool {
	return issuedAt.Before(c.cutoff)
}
func (c *cutoffRevoker) RevokeUserBefore(
	context.Context,
	string,
	time.Time,
	time.Time,
) error {
	return nil
}

// --- helpers ---

func activeUser(t *testing.T, email string) domain.User {
	t.Helper()
	hash, err := auth.HashPassword("abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	return mustUser(email, hash)
}

func postFrom(mux *http.ServeMux, path string, body any, remoteAddr string) *httptest.ResponseRecorder {
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A per-address limit is only a limit when the address distinguishes callers.
// Behind a proxy with TrustProxyHeaders off, every request carries the proxy's
// address — so keying a bucket on it would lock the whole instance out after one
// user's wrong passwords, which is worse than not limiting by address at all.
func TestProxiedRequestsAreNotOneSharedAddressBucket(t *testing.T) {
	h, _, mux := newTestAuth()
	h.Limiter = ratelimit.NewTokenBucket(1.0/600.0, 2)
	h.TrustProxyHeaders = false // nothing tells us who the real client is

	for _, email := range []string{"a@b.com", "c@d.com", "e@f.com", "g@h.com"} {
		if _, err := h.Store.CreateUser(context.Background(), activeUser(t, email)); err != nil {
			t.Fatal(err)
		}
	}

	// Two users burn the shared address bucket from behind the proxy...
	for i := 0; i < 4; i++ {
		postFrom(mux, "/v1/auth/login", map[string]string{"email": "a@b.com", "password": "nope"}, "127.0.0.1:9000")
	}
	// ...an unrelated user must still be able to sign in.
	rec := postFrom(mux, "/v1/auth/login",
		map[string]string{"email": "c@d.com", "password": "abcd1234"}, "127.0.0.1:9001")
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("one user's failures locked another out — the proxy address is being used as a bucket key")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

// With a trusted proxy header the address IS meaningful, so the bucket applies.
func TestTrustedProxyHeaderRestoresThePerAddressBucket(t *testing.T) {
	h, _, mux := newTestAuth()
	h.Limiter = ratelimit.NewTokenBucket(1.0/600.0, 3)
	h.TrustProxyHeaders = true

	var throttled bool
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login",
			bytes.NewReader(encodeJSON(map[string]string{"email": "ghost" + itoa(i) + "@b.com", "password": "x"})))
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		req.RemoteAddr = "127.0.0.1:9000"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("one forwarded-for address sprayed ten accounts without being throttled")
	}
}

func encodeJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
