package auth

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func hostKey(t *testing.T, seed byte) ed25519.PrivateKey {
	t.Helper()
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed
	}
	return ed25519.NewKeyFromSeed(s)
}

func managerWithKey(t *testing.T, seed byte, issuer string) *TokenManager {
	t.Helper()
	m := NewTokenManager("legacy-secret", time.Minute, time.Hour)
	m.UseHostKey(hostKey(t, seed), issuer)
	return m
}

func TestHostKeySignsEdDSAWithIssuerAudienceAndKid(t *testing.T) {
	m := managerWithKey(t, 1, "a.example")
	tok, err := m.sign("user-1", "member", "sid-1", AccessToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	parsed, _, err := jwt.NewParser().ParseUnverified(tok, &Claims{})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Method.Alg() != "EdDSA" {
		t.Errorf("alg = %q, want EdDSA", parsed.Method.Alg())
	}
	if kid, _ := parsed.Header["kid"].(string); kid != m.KeyID() || kid == "" {
		t.Errorf("kid = %q, want %q", kid, m.KeyID())
	}
	c := parsed.Claims.(*Claims)
	if c.Issuer != "a.example" {
		t.Errorf("iss = %q", c.Issuer)
	}
	if len(c.Audience) != 1 || c.Audience[0] != "a.example" {
		t.Errorf("aud = %v", c.Audience)
	}
}

func TestHostKeyTokensVerify(t *testing.T) {
	m := managerWithKey(t, 1, "a.example")
	tok, err := m.sign("user-1", "member", "sid-1", AccessToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Parse(tok, AccessToken)
	if err != nil {
		t.Fatalf("own token rejected: %v", err)
	}
	if got != "user-1" {
		t.Errorf("subject = %q", got)
	}
}

// The failure this whole change exists to prevent. The subject of a token is a
// BARE user id, so under a shared secret two hosts would each accept the
// other's tokens and authenticate as whichever local user held the same id —
// signature valid, nothing visibly wrong. With per-host keys the other host's
// token simply does not verify.
func TestAnotherHostsTokenIsRefused(t *testing.T) {
	a := managerWithKey(t, 1, "a.example")
	b := managerWithKey(t, 2, "b.example")

	tok, err := b.sign("user-1", "admin", "sid-1", AccessToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Parse(tok, AccessToken); err == nil {
		t.Fatal("host A accepted a token minted by host B")
	}
}

// Even if two hosts were somehow given the same key, the issuer claim still
// separates them — defence in depth against exactly the operational mistake
// (copying config between hosts) that made the shared secret dangerous.
func TestSameKeyButDifferentIssuerIsRefused(t *testing.T) {
	a := managerWithKey(t, 3, "a.example")
	b := managerWithKey(t, 3, "b.example")

	tok, err := b.sign("user-1", "member", "sid-1", AccessToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Parse(tok, AccessToken); err == nil {
		t.Fatal("a token issued by b.example was accepted by a.example")
	}
}

// Turning on a host key must not sign everybody out: tokens issued under the
// old shared secret keep working until they expire on their own.
func TestLegacyHS256TokensStillVerifyAfterTheKeyIsTurnedOn(t *testing.T) {
	legacy := NewTokenManager("legacy-secret", time.Minute, time.Hour)
	tok, err := legacy.sign("user-1", "member", "sid-1", AccessToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	upgraded := NewTokenManager("legacy-secret", time.Minute, time.Hour)
	upgraded.UseHostKey(hostKey(t, 1), "a.example")

	got, err := upgraded.Parse(tok, AccessToken)
	if err != nil {
		t.Fatalf("a token issued before the key was configured was rejected: %v", err)
	}
	if got != "user-1" {
		t.Errorf("subject = %q", got)
	}
}

// Without a key configured nothing changes, so a deployment that never sets one
// behaves exactly as it did.
func TestLegacyModeStillSignsHS256(t *testing.T) {
	m := NewTokenManager("legacy-secret", time.Minute, time.Hour)
	tok, err := m.sign("user-1", "member", "sid-1", AccessToken, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, &Claims{})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Method.Alg() != "HS256" {
		t.Errorf("alg = %q, want HS256 with no host key", parsed.Method.Alg())
	}
	if _, err := m.Parse(tok, AccessToken); err != nil {
		t.Errorf("legacy token rejected in legacy mode: %v", err)
	}
}

// A token naming a key id this host does not have is not this host's token,
// however it verifies. This is what lets a rotated key be told apart from a
// forgery instead of both failing the same way.
func TestTokenWithAnUnknownKidIsRefused(t *testing.T) {
	m := managerWithKey(t, 1, "a.example")
	claims := Claims{
		Type: AccessToken,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			Issuer:    "a.example",
			Audience:  jwt.ClaimStrings{"a.example"},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = "not-our-key-id"
	signed, err := tok.SignedString(hostKey(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Parse(signed, AccessToken); err == nil {
		t.Fatal("a token with an unknown kid was accepted")
	}
}

// alg=none is the oldest JWT attack there is. The keyfunc refuses anything that
// is not HMAC or Ed25519 rather than leaving it to library defaults.
func TestUnsignedTokenIsRefused(t *testing.T) {
	m := managerWithKey(t, 1, "a.example")
	claims := Claims{
		Type: AccessToken,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Parse(signed, AccessToken); err == nil {
		t.Fatal("an unsigned token was accepted")
	}
}

// A token addressed to another host must not authorise anything here, even
// when this host signed it — which is the case a future federated endpoint
// minting scoped tokens would otherwise get wrong.
func TestTokenForAnotherAudienceIsRefused(t *testing.T) {
	m := managerWithKey(t, 1, "a.example")
	claims := Claims{
		Type: AccessToken,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			Issuer:    "a.example",
			Audience:  jwt.ClaimStrings{"b.example"},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = m.KeyID()
	signed, err := tok.SignedString(hostKey(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Parse(signed, AccessToken); err == nil {
		t.Fatal("a token for another audience was accepted")
	}
}

func TestKeyIDIsStableAndRotatesWithTheKey(t *testing.T) {
	one := managerWithKey(t, 1, "a.example")
	same := managerWithKey(t, 1, "a.example")
	other := managerWithKey(t, 2, "a.example")

	if one.KeyID() != same.KeyID() {
		t.Error("the same key produced two different ids")
	}
	if one.KeyID() == other.KeyID() {
		t.Error("different keys produced the same id")
	}
	if strings.TrimSpace(one.KeyID()) == "" {
		t.Error("key id is empty")
	}
}

// The public half is what goes in the host's nodelist entry, so it has to be
// reachable and has to be the counterpart of what signs.
func TestPublicKeyMatchesTheSigningKey(t *testing.T) {
	key := hostKey(t, 7)
	m := NewTokenManager("s", time.Minute, time.Hour)
	m.UseHostKey(key, "a.example")

	if !m.PublicKey().Equal(key.Public()) {
		t.Error("PublicKey is not the signing key's public half")
	}
}
