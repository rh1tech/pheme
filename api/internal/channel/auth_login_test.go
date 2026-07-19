package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
)

// Signing in and staying signed in. Both endpoints were uncovered, which is a strange place for a
// gap: everything else in the product is behind them.
//
// The properties that matter are the ones an attacker probes. A login that distinguishes "no such
// account" from "wrong password" hands over a list of who has an account here. One that ignores a
// blocked status lets a removed person back in. And a refresh that accepts an ACCESS token, or a
// revoked session, quietly undoes the session control it exists to enforce.

// seedLogin creates a user with a real password hash and returns the handler set up to serve them.
func seedLogin(t *testing.T, email, password string) (*AuthHandler, *http.ServeMux, domain.User) {
	t.Helper()
	h, _, mux := newTestAuth()

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := h.Store.CreateUser(context.Background(), mustUser(email, hash))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return h, mux, u
}

func decodeTokens(t *testing.T, body []byte) (access, refresh string) {
	t.Helper()
	var out struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode tokens: %v (%s)", err, body)
	}
	return out.AccessToken, out.RefreshToken
}

func TestLoginSucceedsAndIssuesAUsablePair(t *testing.T) {
	h, mux, u := seedLogin(t, "login@pheme.test", "Correct12345")

	rec := post(mux, "/v1/auth/login", map[string]any{
		"email": "login@pheme.test", "password": "Correct12345",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d, body %s", rec.Code, rec.Body)
	}
	access, refresh := decodeTokens(t, rec.Body.Bytes())
	if access == "" || refresh == "" {
		t.Fatal("login returned an empty token")
	}

	// Both must actually parse as what they claim to be, and name the right person.
	claims, err := h.Tokens.ParseClaims(access, auth.AccessToken)
	if err != nil {
		t.Fatalf("the access token does not parse: %v", err)
	}
	if claims.Subject != u.ID {
		t.Errorf("access token subject = %q, want %q", claims.Subject, u.ID)
	}
	if _, err := h.Tokens.ParseClaims(refresh, auth.RefreshToken); err != nil {
		t.Errorf("the refresh token does not parse: %v", err)
	}
	// An access token must NOT be usable where a refresh token is expected, or its short life
	// stops meaning anything.
	if _, err := h.Tokens.ParseClaims(access, auth.RefreshToken); err == nil {
		t.Error("an access token parsed as a refresh token")
	}
}

// The email is normalised, because people type their address the way they feel like typing it and
// an account they cannot sign into is indistinguishable from a lost account.
func TestLoginNormalisesTheEmail(t *testing.T) {
	_, mux, _ := seedLogin(t, "case@pheme.test", "Correct12345")

	for _, typed := range []string{"CASE@pheme.test", "  case@pheme.test  ", "Case@Pheme.Test"} {
		rec := post(mux, "/v1/auth/login", map[string]any{"email": typed, "password": "Correct12345"})
		if rec.Code != http.StatusOK {
			t.Errorf("login with %q = %d, want 200", typed, rec.Code)
		}
	}
}

// A wrong password and a non-existent account must be INDISTINGUISHABLE. Any difference — status,
// message, or a materially faster answer — turns the login form into a way to enumerate who has an
// account here.
func TestLoginDoesNotRevealWhetherAnAccountExists(t *testing.T) {
	_, mux, _ := seedLogin(t, "real@pheme.test", "Correct12345")

	wrongPassword := post(mux, "/v1/auth/login", map[string]any{
		"email": "real@pheme.test", "password": "Wrong12345",
	})
	noSuchUser := post(mux, "/v1/auth/login", map[string]any{
		"email": "ghost@pheme.test", "password": "Wrong12345",
	})

	if wrongPassword.Code != http.StatusUnauthorized {
		t.Errorf("wrong password = %d, want 401", wrongPassword.Code)
	}
	if noSuchUser.Code != http.StatusUnauthorized {
		t.Errorf("unknown account = %d, want 401", noSuchUser.Code)
	}
	if wrongPassword.Body.String() != noSuchUser.Body.String() {
		t.Errorf("the two answers differ:\n  wrong password: %s\n  unknown account: %s\n"+
			"that difference enumerates who has an account here",
			wrongPassword.Body, noSuchUser.Body)
	}
}

// A blocked account is refused with its own status, and never handed tokens. This is the check that
// makes blocking mean anything at all.
func TestLoginRefusesABlockedAccount(t *testing.T) {
	h, mux, u := seedLogin(t, "blocked@pheme.test", "Correct12345")

	if err := h.Store.UpdateUserStatus(context.Background(), u.ID, domain.UserBlocked); err != nil {
		t.Fatalf("block: %v", err)
	}
	rec := post(mux, "/v1/auth/login", map[string]any{
		"email": "blocked@pheme.test", "password": "Correct12345",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("blocked login = %d, want 403; body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "accessToken") {
		t.Error("a blocked account was issued tokens")
	}
}

// The allowlist guarantees its addresses are admins, and the promotion is PERSISTED — otherwise it
// would be re-decided on every login and any demotion elsewhere would silently flap.
func TestLoginPromotesAnAllowlistedAdminAndRemembersIt(t *testing.T) {
	h, mux, u := seedLogin(t, "boss@pheme.test", "Correct12345")
	h.AdminEmails = map[string]bool{"boss@pheme.test": true}

	rec := post(mux, "/v1/auth/login", map[string]any{
		"email": "boss@pheme.test", "password": "Correct12345",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d, body %s", rec.Code, rec.Body)
	}
	access, _ := decodeTokens(t, rec.Body.Bytes())
	claims, err := h.Tokens.ParseClaims(access, auth.AccessToken)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Role != string(domain.RoleAdmin) {
		t.Errorf("token role = %q, want admin", claims.Role)
	}

	stored, err := h.Store.UserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Role != domain.RoleAdmin {
		t.Errorf("stored role = %q, want the promotion persisted", stored.Role)
	}
}

// An admin who was deliberately demoted must not be promoted back by logging in. Only the
// allowlist promotes, and it never auto-demotes anyone either.
func TestLoginDoesNotDemoteAStoredAdmin(t *testing.T) {
	h, mux, u := seedLogin(t, "kept@pheme.test", "Correct12345")
	if err := h.Store.UpdateUserRole(context.Background(), u.ID, domain.RoleAdmin); err != nil {
		t.Fatalf("promote: %v", err)
	}

	rec := post(mux, "/v1/auth/login", map[string]any{
		"email": "kept@pheme.test", "password": "Correct12345",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	access, _ := decodeTokens(t, rec.Body.Bytes())
	claims, _ := h.Tokens.ParseClaims(access, auth.AccessToken)
	if claims.Role != string(domain.RoleAdmin) {
		t.Errorf("a stored admin was demoted by logging in: role = %q", claims.Role)
	}
}

func TestRefreshIssuesANewPairForTheSameSession(t *testing.T) {
	h, mux, _ := seedLogin(t, "refresh@pheme.test", "Correct12345")

	rec := post(mux, "/v1/auth/login", map[string]any{
		"email": "refresh@pheme.test", "password": "Correct12345",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d", rec.Code)
	}
	_, refresh := decodeTokens(t, rec.Body.Bytes())
	first, err := h.Tokens.ParseClaims(refresh, auth.RefreshToken)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	rec = post(mux, "/v1/auth/refresh", map[string]any{"refreshToken": refresh})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d, body %s", rec.Code, rec.Body)
	}
	newAccess, newRefresh := decodeTokens(t, rec.Body.Bytes())
	if newAccess == "" || newRefresh == "" {
		t.Fatal("refresh returned an empty token")
	}

	// The SESSION carries over. It is what "terminate this device" revokes, so a refresh that
	// minted a new session id would hand a terminated device a way back in.
	next, err := h.Tokens.ParseClaims(newRefresh, auth.RefreshToken)
	if err != nil {
		t.Fatalf("parse new: %v", err)
	}
	if next.SID != first.SID {
		t.Errorf("refresh changed the session id (%q -> %q); a terminated device could refresh its "+
			"way back in", first.SID, next.SID)
	}
	if next.Subject != first.Subject {
		t.Errorf("refresh changed the subject (%q -> %q)", first.Subject, next.Subject)
	}
}

// An ACCESS token must not be accepted where a refresh token is required. If it were, the short
// access lifetime would buy nothing.
func TestRefreshRejectsAnAccessToken(t *testing.T) {
	_, mux, _ := seedLogin(t, "swap@pheme.test", "Correct12345")

	rec := post(mux, "/v1/auth/login", map[string]any{
		"email": "swap@pheme.test", "password": "Correct12345",
	})
	access, _ := decodeTokens(t, rec.Body.Bytes())

	rec = post(mux, "/v1/auth/refresh", map[string]any{"refreshToken": access})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("refreshing with an access token = %d, want 401", rec.Code)
	}
}

func TestRefreshRejectsRubbish(t *testing.T) {
	_, mux, _ := seedLogin(t, "junk@pheme.test", "Correct12345")

	for _, token := range []string{"", "not-a-token", "a.b.c", strings.Repeat("x", 500)} {
		rec := post(mux, "/v1/auth/refresh", map[string]any{"refreshToken": token})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("refresh with %q = %d, want 401", token[:min(len(token), 20)], rec.Code)
		}
	}
}

// The whole point of session revocation: a terminated device must not be able to refresh.
func TestRefreshRefusesARevokedSession(t *testing.T) {
	h, mux, _ := seedLogin(t, "revoked-refresh@pheme.test", "Correct12345")
	revoker := auth.NewSessionRevoker(h.Store.(auth.SessionRevocationStore))
	h.Revoker = revoker

	rec := post(mux, "/v1/auth/login", map[string]any{
		"email": "revoked-refresh@pheme.test", "password": "Correct12345",
	})
	_, refresh := decodeTokens(t, rec.Body.Bytes())
	claims, err := h.Tokens.ParseClaims(refresh, auth.RefreshToken)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// It works before revocation...
	if rec := post(mux, "/v1/auth/refresh", map[string]any{"refreshToken": refresh}); rec.Code != http.StatusOK {
		t.Fatalf("refresh before revoking = %d", rec.Code)
	}
	if err := revoker.Revoke(context.Background(), claims.SID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// ...and not after. Without this, terminating a device would sever its crypto and leave it
	// able to mint fresh credentials indefinitely.
	if rec := post(mux, "/v1/auth/refresh", map[string]any{"refreshToken": refresh}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a revoked session refreshed successfully: %d", rec.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
