package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey int

const (
	userIDKey ctxKey = iota
	roleKey
	sessionIDKey
)

// WithUserID returns a copy of ctx carrying the authenticated user ID.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext returns the authenticated user ID, if present.
func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok && id != ""
}

// WithRole returns a copy of ctx carrying the authenticated user's role.
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, roleKey, role)
}

// RoleFromContext returns the authenticated user's role, if present.
func RoleFromContext(ctx context.Context) (string, bool) {
	role, ok := ctx.Value(roleKey).(string)
	return role, ok
}

// IsAdmin reports whether the request context belongs to an admin.
func IsAdmin(ctx context.Context) bool {
	role, ok := RoleFromContext(ctx)
	return ok && role == "admin"
}

// WithSessionID returns a copy of ctx carrying the authenticated session id (the token's
// `sid` claim), so a handler can record which session a request belongs to.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey, sessionID)
}

// SessionIDFromContext returns the authenticated session id, if present.
func SessionIDFromContext(ctx context.Context) (string, bool) {
	sid, ok := ctx.Value(sessionIDKey).(string)
	return sid, ok && sid != ""
}

// Middleware validates the Bearer access token and injects the user ID, role and session
// id into the request context. Requests without a valid token — or with one whose session
// has been revoked (a terminated device) — receive 401.
func (m *TokenManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			unauthorized(w)
			return
		}
		claims, err := m.ParseClaims(strings.TrimSpace(token), AccessToken)
		if err != nil {
			unauthorized(w)
			return
		}
		// A validly-signed, unexpired token still loses if its session was terminated.
		if m.revoker != nil && m.revoker.IsRevoked(claims.SID) {
			unauthorized(w)
			return
		}
		// ...or if every token this user holds from before a cutoff was refused. That is the only
		// thing that reaches a device whose session id was never recorded, which no per-session
		// revocation can match.
		if m.revoker != nil && claims.IssuedAt != nil &&
			m.revoker.IsUserRevoked(claims.Subject, claims.IssuedAt.Time) {
			unauthorized(w)
			return
		}
		ctx := WithUserID(r.Context(), claims.Subject)
		ctx = WithRole(ctx, claims.Role)
		ctx = WithSessionID(ctx, claims.SID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
}
