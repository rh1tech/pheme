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

// Issue returns a signed access and refresh token pair for the given user ID.
func (m *TokenManager) Issue(userID string) (access, refresh string, err error) {
	access, err = m.sign(userID, AccessToken, m.accessTTL)
	if err != nil {
		return "", "", err
	}
	refresh, err = m.sign(userID, RefreshToken, m.refreshTTL)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

func (m *TokenManager) sign(userID string, typ TokenType, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		Type: typ,
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
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})
	if err != nil || !parsed.Valid {
		return "", ErrInvalidToken
	}
	if claims.Type != want {
		return "", ErrInvalidToken
	}
	return claims.Subject, nil
}
