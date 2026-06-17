package auth

import (
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
	jwt.RegisteredClaims
}

// TokenManager issues and verifies signed JWTs.
type TokenManager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokenManager creates a TokenManager with the given signing secret and TTLs.
func NewTokenManager(secret string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}
}

// Issue returns a signed access and refresh token pair for the given user ID,
// embedding the user's role.
func (m *TokenManager) Issue(userID, role string) (access, refresh string, err error) {
	access, err = m.sign(userID, role, AccessToken, m.accessTTL)
	if err != nil {
		return "", "", err
	}
	refresh, err = m.sign(userID, role, RefreshToken, m.refreshTTL)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

func (m *TokenManager) sign(userID, role string, typ TokenType, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		Type: typ,
		Role: role,
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
