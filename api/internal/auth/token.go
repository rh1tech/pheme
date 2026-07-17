package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenType distinguishes access tokens from refresh tokens.
type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"
)

// ErrInvalidToken is returned when a token is malformed, expired, or signed with
// the wrong key.
var ErrInvalidToken = errors.New("invalid token")

// Claims is the JWT payload Pheme issues.
type Claims struct {
	Type TokenType `json:"typ"`
	Role string    `json:"role,omitempty"`
	// SID identifies the auth session — one login, stable across token refresh (the
	// refresh path re-issues under the same SID). It is what "terminate this device"
	// revokes: the middleware refuses a token whose SID has been revoked. Tokens issued
	// before this field existed simply have no SID and are never matched by a revocation.
	SID string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// sessionChecker answers whether a session id has been revoked. *SessionRevoker satisfies
// it; the field is optional so a TokenManager with no revoker simply never revokes.
type sessionChecker interface {
	IsRevoked(sessionID string) bool
}

// TokenManager issues and verifies signed JWTs.
type TokenManager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	revoker    sessionChecker
}

// NewTokenManager creates a TokenManager with the given signing secret and TTLs.
func NewTokenManager(secret string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}
}

// UseRevoker attaches a session revoker, so the middleware rejects tokens whose session
// has been terminated. Without one, no token is ever revoked (the prior behaviour).
func (m *TokenManager) UseRevoker(r sessionChecker) {
	m.revoker = r
}

// RefreshTTL is how long a refresh token lives — the horizon past which a revoked
// session's entry can be reaped, since the token is rejected on expiry anyway.
func (m *TokenManager) RefreshTTL() time.Duration {
	return m.refreshTTL
}

// Issue starts a NEW session (a fresh login) and returns a signed access/refresh pair
// for it, embedding the user's role and the new session id. The session id is also
// returned so the caller can record it.
func (m *TokenManager) Issue(userID, role string) (access, refresh, sessionID string, err error) {
	sid, err := newSessionID()
	if err != nil {
		return "", "", "", err
	}
	access, refresh, err = m.IssueWithSession(userID, role, sid)
	if err != nil {
		return "", "", "", err
	}
	return access, refresh, sid, nil
}

// IssueWithSession returns a signed access/refresh pair under an EXISTING session id.
// The refresh path uses it so a token refresh keeps the same session — otherwise every
// refresh would mint a new session id and "terminate this device" could only ever revoke
// a login the device had already rotated away from.
func (m *TokenManager) IssueWithSession(userID, role, sessionID string) (access, refresh string, err error) {
	access, err = m.sign(userID, role, sessionID, AccessToken, m.accessTTL)
	if err != nil {
		return "", "", err
	}
	refresh, err = m.sign(userID, role, sessionID, RefreshToken, m.refreshTTL)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// newSessionID returns a high-entropy, URL-safe session identifier.
func newSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (m *TokenManager) sign(userID, role, sessionID string, typ TokenType, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		Type: typ,
		Role: role,
		SID:  sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// Parse validates a token, checks its type, and returns the subject (user ID).
func (m *TokenManager) Parse(token string, want TokenType) (string, error) {
	claims, err := m.ParseClaims(token, want)
	if err != nil {
		return "", err
	}
	return claims.Subject, nil
}

// ParseClaims validates a token, checks its type, and returns the full claims
// (including the user ID in Subject and the role).
func (m *TokenManager) ParseClaims(token string, want TokenType) (*Claims, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	if claims.Type != want {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
