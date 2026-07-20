package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
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
	// IsUserRevoked covers the device that IsRevoked cannot: one registered before session ids
	// existed has none, so no revocation could ever match it. See SessionRevoker.userCutoff.
	IsUserRevoked(userID string, issuedAt time.Time) bool
}

// TokenManager issues and verifies signed JWTs.
//
// Two signing modes. With a host key configured it signs EdDSA and stamps the
// issuer, the audience and a key id; without one it signs HS256 with a shared
// secret, which is what every deployment did before federation work began.
//
// The shared secret is the mode that cannot survive federation, and it is worth
// being precise about why: the subject of a token is a BARE user id, so two
// hosts that happened to share a secret would each accept the other's tokens
// and silently authenticate as whichever local user held the same id. The
// signature would verify. Nothing would look wrong. Asymmetric signing plus an
// issuer claim removes the possibility rather than relying on nobody copying a
// secret between hosts.
//
// Both modes VERIFY throughout the transition: a deployment that turns on a
// host key keeps honouring tokens it issued yesterday, so nobody is signed out
// by a deploy. Refresh tokens live 30 days, so the secret can be dropped a
// month after the key is turned on.
type TokenManager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	revoker    sessionChecker

	// Optional; nil means legacy HS256 signing.
	signKey   ed25519.PrivateKey
	verifyKey ed25519.PublicKey
	keyID     string
	issuer    string
}

// NewTokenManager creates a TokenManager with the given signing secret and TTLs.
func NewTokenManager(secret string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}
}

// UseRevoker attaches a session revoker, so the middleware rejects tokens whose session
// has been terminated. Without one, no token is ever revoked (the prior behaviour).
// UseHostKey switches signing to EdDSA under this host's key, stamping issuer
// and audience with the host's own domain.
//
// The key id is a fingerprint of the public half, so rotating the key rotates
// the id and a verifier can tell "signed by a key I do not have" apart from
// "signature does not verify" -- which is the difference between a host that
// has rotated and a forgery.
func (m *TokenManager) UseHostKey(key ed25519.PrivateKey, issuer string) {
	m.signKey = key
	m.verifyKey = key.Public().(ed25519.PublicKey)
	m.issuer = issuer
	sum := sha256.Sum256(m.verifyKey)
	m.keyID = base64.RawURLEncoding.EncodeToString(sum[:12])
}

// KeyID is this host's current signing key id, empty in legacy mode.
func (m *TokenManager) KeyID() string { return m.keyID }

// PublicKey is the half a peer needs to verify this host's tokens. It is the
// value that goes in the host's nodelist entry.
func (m *TokenManager) PublicKey() ed25519.PublicKey { return m.verifyKey }

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
	if m.issuer != "" {
		claims.Issuer = m.issuer
		// Audience is this host too: these are tokens for our own API, and a
		// federated peer must never accept one as authorising anything of its
		// own. Stamping it makes that a check rather than an assumption.
		claims.Audience = jwt.ClaimStrings{m.issuer}
	}
	if m.signKey == nil {
		return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = m.keyID
	return tok.SignedString(m.signKey)
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
		switch t.Method.(type) {
		case *jwt.SigningMethodHMAC:
			// Legacy. Still honoured so that turning on a host key does not sign
			// out every session; drop the secret once the longest refresh token
			// issued under it has expired.
			return m.secret, nil
		case *jwt.SigningMethodEd25519:
			if m.verifyKey == nil {
				return nil, ErrInvalidToken
			}
			// A token naming a key id we do not have is not ours, whatever it
			// verifies against.
			if kid, _ := t.Header["kid"].(string); kid != m.keyID {
				return nil, ErrInvalidToken
			}
			return m.verifyKey, nil
		default:
			// Anything else, "none" above all, is refused outright rather than
			// left to the library's defaults.
			return nil, ErrInvalidToken
		}
	})
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	if claims.Type != want {
		return nil, ErrInvalidToken
	}
	// Checked only when present, so tokens issued before these claims existed
	// keep working. Once issued, they are binding: a token from another issuer,
	// or meant for another audience, is refused even with a good signature.
	if claims.Issuer != "" && m.issuer != "" && claims.Issuer != m.issuer {
		return nil, ErrInvalidToken
	}
	if len(claims.Audience) > 0 && m.issuer != "" && !contains(claims.Audience, m.issuer) {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func contains(list jwt.ClaimStrings, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ParseHostKey decodes a base64url-encoded 32-byte Ed25519 seed into a private
// key. An empty string yields (nil, nil), meaning "no key configured" — the
// caller then stays in legacy HS256 mode.
//
// A seed rather than a full private key: it is half the length to handle, and
// it cannot encode a public half that disagrees with the private one.
func ParseHostKey(encoded string) (ed25519.PrivateKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, nil
	}
	seed, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil {
		return nil, fmt.Errorf("host key is not base64url: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("host key is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// NewHostKey generates a host signing key and returns it with its encoded seed.
func NewHostKey() (ed25519.PrivateKey, string, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, "", err
	}
	return ed25519.NewKeyFromSeed(seed), base64.RawURLEncoding.EncodeToString(seed), nil
}
