package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey struct{}

// userIDKey is the context key under which the authenticated user ID is stored.
var userIDKey = ctxKey{}

// WithUserID returns a copy of ctx carrying the authenticated user ID.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext returns the authenticated user ID, if present.
func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok && id != ""
}

// Middleware validates the Bearer access token and injects the user ID into the
// request context. Requests without a valid token receive 401.
func (m *TokenManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			unauthorized(w)
			return
		}
		userID, err := m.Parse(strings.TrimSpace(token), AccessToken)
		if err != nil {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), userID)))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
}
