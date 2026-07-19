package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The gate every protected request in this API passes through, and it had no tests.
//
// Everything behind it — every conversation, every channel, every admin action — is reachable only
// if this function decides a request is authenticated. It has to reject four different kinds of
// "no" (no header, wrong scheme, unparseable token, wrong token type), two kinds of "not any more"
// (this session was terminated, every session this user had before a cutoff was), and on the way
// through it has to hand the handler the right identity. A handler that receives somebody else's
// user id is not an authentication bug, it is every authorisation check in the product silently
// pointing at the wrong person.

// probe records what the middleware handed the handler behind it.
type probe struct {
	called    bool
	userID    string
	role      string
	sessionID string
}

func (p *probe) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.called = true
		p.userID, _ = UserIDFromContext(r.Context())
		p.role, _ = RoleFromContext(r.Context())
		p.sessionID, _ = SessionIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func newManager() *TokenManager {
	return NewTokenManager("middleware-test-secret", 15*time.Minute, 24*time.Hour)
}

// call runs a request with the given Authorization header through the middleware.
func call(m *TokenManager, header string) (*httptest.ResponseRecorder, *probe) {
	p := &probe{}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	m.Middleware(p.handler()).ServeHTTP(rec, req)
	return rec, p
}

func TestMiddlewarePassesTheIdentityThrough(t *testing.T) {
	m := newManager()
	access, _, _, err := m.Issue("user-1", "admin")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	rec, p := call(m, "Bearer "+access)
	if rec.Code != http.StatusOK {
		t.Fatalf("a valid token was refused: %d (%s)", rec.Code, rec.Body)
	}
	if !p.called {
		t.Fatal("the handler behind the middleware never ran")
	}
	if p.userID != "user-1" {
		t.Errorf("the handler saw user %q, want user-1; every authorisation check behind this "+
			"points at the wrong person", p.userID)
	}
	if p.role != "admin" {
		t.Errorf("the handler saw role %q, want admin", p.role)
	}
	if p.sessionID == "" {
		t.Error("the handler saw no session id; nothing can record which device a request came from")
	}
}

// Every shape of missing or malformed credential is a 401, and the handler must never run.
func TestMiddlewareRefusesAnythingThatIsNotAValidBearerToken(t *testing.T) {
	m := newManager()
	access, _, _, err := m.Issue("user-1", "user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	for _, tc := range []struct{ name, header string }{
		{"no header at all", ""},
		{"empty header", ""},
		{"no scheme", access},
		{"wrong scheme", "Basic " + access},
		{"lowercase scheme", "bearer " + access}, // CutPrefix is case-sensitive, deliberately
		{"scheme with no token", "Bearer "},
		{"scheme with whitespace only", "Bearer    "},
		{"rubbish token", "Bearer not-a-token"},
		{"three dots", "Bearer a.b.c"},
		{"token with the signature stripped", "Bearer " + strings.Join(strings.Split(access, ".")[:2], ".")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, p := call(m, tc.header)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if p.called {
				t.Error("the handler ran for a request that was not authenticated")
			}
		})
	}
}

// A token signed by somebody else must not be accepted. This is the whole point of the signature.
func TestMiddlewareRefusesATokenSignedWithAnotherSecret(t *testing.T) {
	attacker := NewTokenManager("a-different-secret", 15*time.Minute, 24*time.Hour)
	forged, _, _, err := attacker.Issue("user-1", "admin")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	rec, p := call(newManager(), "Bearer "+forged)
	if rec.Code != http.StatusUnauthorized || p.called {
		t.Errorf("a token signed with another secret was accepted (status %d, handler ran %v); "+
			"anyone able to mint one is an admin", rec.Code, p.called)
	}
}

// A REFRESH token must not open a protected route. If it did, the short access lifetime would buy
// nothing — a refresh token lives for a day.
func TestMiddlewareRefusesARefreshTokenAsCredentials(t *testing.T) {
	m := newManager()
	_, refresh, _, err := m.Issue("user-1", "user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	rec, p := call(m, "Bearer "+refresh)
	if rec.Code != http.StatusUnauthorized || p.called {
		t.Errorf("a refresh token authenticated a request (status %d, handler ran %v); the short "+
			"access lifetime would mean nothing", rec.Code, p.called)
	}
}

// An expired token is refused. Tested through a manager with a zero lifetime rather than by waiting.
func TestMiddlewareRefusesAnExpiredToken(t *testing.T) {
	m := NewTokenManager("middleware-test-secret", -time.Minute, 24*time.Hour)
	expired, _, _, err := m.Issue("user-1", "user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	rec, p := call(newManager(), "Bearer "+expired)
	if rec.Code != http.StatusUnauthorized || p.called {
		t.Errorf("an expired token was accepted (status %d, handler ran %v)", rec.Code, p.called)
	}
}

// stubRevoker answers whatever it is told.
type stubRevoker struct {
	revokedSID   string
	revokedUser  string
	userCutoff   time.Time
	sidCalls     int
	userCalls    int
	revokeAllFor bool
}

func (s *stubRevoker) IsRevoked(sessionID string) bool {
	s.sidCalls++
	return sessionID != "" && sessionID == s.revokedSID
}

func (s *stubRevoker) IsUserRevoked(userID string, issuedAt time.Time) bool {
	s.userCalls++
	if s.revokeAllFor && userID == s.revokedUser {
		return true
	}
	return userID == s.revokedUser && issuedAt.Before(s.userCutoff)
}

// THE ONE THAT MAKES TERMINATION MEAN ANYTHING. A validly-signed, unexpired token whose session was
// terminated must be refused.
func TestMiddlewareRefusesATerminatedSession(t *testing.T) {
	m := newManager()
	access, _, _, err := m.Issue("user-1", "user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := m.ParseClaims(access, AccessToken)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Before revoking, it works.
	if rec, _ := call(m, "Bearer "+access); rec.Code != http.StatusOK {
		t.Fatalf("the token did not work before revoking: %d", rec.Code)
	}

	m.UseRevoker(&stubRevoker{revokedSID: claims.SID})

	rec, p := call(m, "Bearer "+access)
	if rec.Code != http.StatusUnauthorized || p.called {
		t.Errorf("a terminated session still authenticates (status %d, handler ran %v); removing a "+
			"device would sever its crypto and leave its token working until it expired",
			rec.Code, p.called)
	}
}

// The other half: a device registered before session ids were recorded has no session to revoke, so
// the only thing that reaches it is refusing everything that user holds from before a cutoff.
func TestMiddlewareRefusesEveryTokenAUserHeldBeforeACutoff(t *testing.T) {
	m := newManager()
	access, _, _, err := m.Issue("user-1", "user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	m.UseRevoker(&stubRevoker{revokedUser: "user-1", revokeAllFor: true})

	rec, p := call(m, "Bearer "+access)
	if rec.Code != http.StatusUnauthorized || p.called {
		t.Errorf("a token issued before the user's cutoff still authenticates (status %d, handler "+
			"ran %v); a device with no recorded session could never be signed out", rec.Code, p.called)
	}
}

// Somebody else's revocation must not lock this user out.
func TestMiddlewareDoesNotRefuseAnUnrelatedUser(t *testing.T) {
	m := newManager()
	access, _, _, err := m.Issue("innocent", "user")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	m.UseRevoker(&stubRevoker{revokedSID: "somebody-elses-session", revokedUser: "somebody-else",
		revokeAllFor: true})

	rec, p := call(m, "Bearer "+access)
	if rec.Code != http.StatusOK || !p.called {
		t.Errorf("revoking another user's session signed this one out too (status %d)", rec.Code)
	}
}

// The 401 body is the same envelope the rest of the API uses, so a client has one error shape to
// parse rather than two.
func TestUnauthorizedUsesTheStandardErrorEnvelope(t *testing.T) {
	rec, _ := call(newManager(), "")

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q, want application/json", ct)
	}
	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("the 401 body is not the error envelope: %s", rec.Body)
	}
	if got.Error.Message == "" {
		t.Error("the 401 carried no message")
	}
}

// The context helpers, which decide what every handler downstream believes about the caller.
func TestContextHelpers(t *testing.T) {
	ctx := context.Background()

	if _, ok := UserIDFromContext(ctx); ok {
		t.Error("an empty context reported a user id")
	}
	if _, ok := RoleFromContext(ctx); ok {
		t.Error("an empty context reported a role")
	}
	if _, ok := SessionIDFromContext(ctx); ok {
		t.Error("an empty context reported a session id")
	}
	if IsAdmin(ctx) {
		t.Error("an empty context is admin; an unauthenticated request would pass an admin check")
	}

	// An EMPTY user id must not count as authenticated. A handler that trusted it would act on
	// behalf of nobody, which in a query scoped by user id matches everything or nothing.
	if _, ok := UserIDFromContext(WithUserID(ctx, "")); ok {
		t.Error("an empty user id was reported as present")
	}
	if _, ok := SessionIDFromContext(WithSessionID(ctx, "")); ok {
		t.Error("an empty session id was reported as present")
	}

	full := WithSessionID(WithRole(WithUserID(ctx, "u1"), "admin"), "sess-1")
	if id, ok := UserIDFromContext(full); !ok || id != "u1" {
		t.Errorf("user id = %q, %v", id, ok)
	}
	if role, ok := RoleFromContext(full); !ok || role != "admin" {
		t.Errorf("role = %q, %v", role, ok)
	}
	if sid, ok := SessionIDFromContext(full); !ok || sid != "sess-1" {
		t.Errorf("session id = %q, %v", sid, ok)
	}
	if !IsAdmin(full) {
		t.Error("an admin context is not admin")
	}
	if IsAdmin(WithRole(ctx, "user")) {
		t.Error("an ordinary user passes an admin check")
	}
	// Near-misses must not pass: the check is exact, not a prefix or a case-insensitive match.
	for _, role := range []string{"Admin", "ADMIN", "administrator", "admin "} {
		if IsAdmin(WithRole(ctx, role)) {
			t.Errorf("role %q passed the admin check", role)
		}
	}
}
