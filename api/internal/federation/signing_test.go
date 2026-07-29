package federation

import (
	"bytes"
	"crypto/ed25519"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func hostKey(t *testing.T, seed byte) ed25519.PrivateKey {
	t.Helper()
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed
	}
	return ed25519.NewKeyFromSeed(s)
}

// fakeLookup is a nodelist stand-in: a fixed domain->key map.
type fakeLookup map[string]ed25519.PublicKey

func (f fakeLookup) KeyFor(domain string) (ed25519.PublicKey, error) {
	return f[domain], nil // nil means "not a peer", matching the Store contract
}

// sign is Sign with the error surfaced as a test failure: every call here uses a
// good key, so a signing error means the test setup is wrong, not the subject.
func sign(t *testing.T, req *http.Request, origin, destination, keyID string, key ed25519.PrivateKey, body []byte, now time.Time) {
	t.Helper()
	if err := Sign(req, origin, destination, keyID, key, body, now); err != nil {
		t.Fatalf("signing failed: %v", err)
	}
}

func request(t *testing.T, method, url string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestSignedRequestVerifies(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"query":"alice"}`)

	req := request(t, http.MethodPost, "https://b.example/federation/v1/user-exists", body)
	sign(t, req, "a.example", "b.example", "key-1", key, body, now)

	got, err := Verify(req, fakeLookup{"a.example": pub}, body, now, "b.example", nil)
	if err != nil {
		t.Fatalf("a validly signed request was refused: %v", err)
	}
	if got.Origin != "a.example" || got.KeyID != "key-1" {
		t.Errorf("verified = %+v", got)
	}
}

// A verified origin is a PROVEN one: the signature is over the origin field and
// checked against the key the nodelist vouches for that origin. Claiming to be
// a.example while signing with b.example's key must fail.
func TestForgedOriginIsRefused(t *testing.T) {
	attacker := hostKey(t, 2)
	victimPub := hostKey(t, 1).Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")

	req := request(t, http.MethodPost, "https://b.example/f", body)
	// Attacker signs with its own key but claims to be a.example.
	sign(t, req, "a.example", "b.example", "key-1", attacker, body, now)

	if _, err := Verify(req, fakeLookup{"a.example": victimPub}, body, now, "b.example", nil); err != ErrBadSignature {
		t.Fatalf("err = %v, want ErrBadSignature — a host impersonated another", err)
	}
}

// A host not in the nodelist is refused, and with the SAME error as a bad
// signature: a stranger and a forger should not be able to tell each other apart.
func TestNonPeerIsRefused(t *testing.T) {
	key := hostKey(t, 3)
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")

	req := request(t, http.MethodPost, "https://b.example/f", body)
	sign(t, req, "stranger.example", "b.example", "key-1", key, body, now)

	// Empty nodelist: nobody is a peer.
	if _, err := Verify(req, fakeLookup{}, body, now, "b.example", nil); err != ErrBadSignature {
		t.Fatalf("err = %v, want ErrBadSignature for a non-peer", err)
	}
}

// The body is bound by hash, so changing it after signing breaks verification —
// otherwise a signature would authorise a request whose body could be swapped.
func TestTamperedBodyIsRefused(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)

	req := request(t, http.MethodPost, "https://b.example/f", []byte("original"))
	sign(t, req, "a.example", "b.example", "key-1", key, []byte("original"), now)

	// Verify against a different body than was signed.
	if _, err := Verify(req, fakeLookup{"a.example": pub}, []byte("swapped!!"), now, "b.example", nil); err != ErrBadSignature {
		t.Fatalf("err = %v, want ErrBadSignature for a swapped body", err)
	}
}

// Method and path are bound, so a signature captured for one endpoint cannot be
// replayed against another.
func TestReplayAgainstADifferentPathIsRefused(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")

	signed := request(t, http.MethodPost, "https://b.example/federation/v1/user-exists", body)
	sign(t, signed, "a.example", "b.example", "key-1", key, body, now)

	// Move the signed headers onto a request for a different path.
	attack := request(t, http.MethodPost, "https://b.example/federation/v1/admin", body)
	for _, h := range []string{HeaderOrigin, HeaderKeyID, HeaderTimestamp, HeaderNonce, HeaderSignature} {
		attack.Header.Set(h, signed.Header.Get(h))
	}
	if _, err := Verify(attack, fakeLookup{"a.example": pub}, body, now, "b.example", nil); err != ErrBadSignature {
		t.Fatalf("err = %v, want ErrBadSignature when replayed against another path", err)
	}
}

// An old signature must not be a standing credential. Outside the skew window,
// even a perfectly valid signature is refused.
func TestStaleTimestampIsRefused(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	signedAt := time.Unix(1_700_000_000, 0)
	body := []byte("x")

	req := request(t, http.MethodPost, "https://b.example/f", body)
	sign(t, req, "a.example", "b.example", "key-1", key, body, signedAt)

	late := signedAt.Add(MaxSkew + time.Second)
	if _, err := Verify(req, fakeLookup{"a.example": pub}, body, late, "b.example", nil); err != ErrStale {
		t.Fatalf("err = %v, want ErrStale past the skew window", err)
	}
	// A clock a little behind the sender must still accept it.
	early := signedAt.Add(-MaxSkew + time.Second)
	if _, err := Verify(req, fakeLookup{"a.example": pub}, body, early, "b.example", nil); err != nil {
		t.Fatalf("a request within skew was refused: %v", err)
	}
}

func TestMissingHeadersIsRefused(t *testing.T) {
	pub := hostKey(t, 1).Public().(ed25519.PublicKey)
	req := request(t, http.MethodPost, "https://b.example/f", []byte("x"))
	// No signing headers at all.
	if _, err := Verify(req, fakeLookup{"a.example": pub}, []byte("x"), time.Unix(1_700_000_000, 0), "b.example", nil); err != ErrMissingHeaders {
		t.Fatalf("err = %v, want ErrMissingHeaders", err)
	}
}

func TestMalformedTimestampIsRefused(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")
	req := request(t, http.MethodPost, "https://b.example/f", body)
	sign(t, req, "a.example", "b.example", "key-1", key, body, now)
	req.Header.Set(HeaderTimestamp, "not-a-number")

	if _, err := Verify(req, fakeLookup{"a.example": pub}, body, now, "b.example", nil); err != ErrBadTimestamp {
		t.Fatalf("err = %v, want ErrBadTimestamp", err)
	}
}

// An empty body still binds: a GET with no body must verify, and must still
// reject a body added afterward.
func TestEmptyBodyStillBinds(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)

	req := request(t, http.MethodGet, "https://b.example/federation/v1/liveness", nil)
	sign(t, req, "a.example", "b.example", "key-1", key, nil, now)

	if _, err := Verify(req, fakeLookup{"a.example": pub}, nil, now, "b.example", nil); err != nil {
		t.Fatalf("an empty-body request was refused: %v", err)
	}
	if _, err := Verify(req, fakeLookup{"a.example": pub}, []byte("added"), now, "b.example", nil); err != ErrBadSignature {
		t.Fatal("a body added to an empty-body request was accepted")
	}
}

// The timestamp header is covered by the signature, so an attacker cannot slide
// a captured request forward by rewriting it.
func TestRewritingTheTimestampBreaksTheSignature(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")

	req := request(t, http.MethodPost, "https://b.example/f", body)
	sign(t, req, "a.example", "b.example", "key-1", key, body, now)

	// Push the timestamp forward to dodge the skew check…
	future := now.Add(10 * time.Minute)
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(future.Unix(), 10))
	// …and verify at that future time, so skew passes. The signature must still fail.
	if _, err := Verify(req, fakeLookup{"a.example": pub}, body, future, "b.example", nil); err != ErrBadSignature {
		t.Fatalf("err = %v, want ErrBadSignature — the timestamp was rewritten", err)
	}
}
