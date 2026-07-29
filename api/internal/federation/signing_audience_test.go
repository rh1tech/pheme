package federation

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"sync"
	"testing"
	"time"
)

// memGuard is an in-process ReplayGuard, standing in for the shared store a real
// host uses.
type memGuard struct {
	mu   sync.Mutex
	seen map[string]bool
	fail error
}

func newGuard() *memGuard { return &memGuard{seen: map[string]bool{}} }

func (g *memGuard) Seen(_ context.Context, key string, _ time.Duration) (bool, error) {
	if g.fail != nil {
		return false, g.fail
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	was := g.seen[key]
	g.seen[key] = true
	return was, nil
}

// The finding: every host serves the same route paths, so a request A signed for
// B was a byte-identical valid request at C, and C attributed it to A. The
// destination is now bound, so C's verification builds a different canonical
// string and fails.
func TestRequestForOnePeerIsRefusedByAnother(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"userId":"victim"}`)
	lookup := fakeLookup{"a.example": pub}

	req := request(t, http.MethodPost, "https://b.example/federation/v1/claim-key-packages", body)
	sign(t, req, "a.example", "b.example", "key-1", key, body, now)

	// It verifies at the host it was addressed to.
	if _, err := Verify(req, lookup, body, now, "b.example", nil); err != nil {
		t.Fatalf("the addressed peer refused a good request: %v", err)
	}

	// Forwarded verbatim to a third host, it must not.
	if _, err := Verify(req, lookup, body, now, "c.example", nil); err != ErrBadSignature {
		t.Fatalf("err = %v, want ErrBadSignature — host C accepted a request addressed to B", err)
	}
}

// A receiver's own domain is what it verifies against, so a peer cannot dodge
// the binding by spelling the destination differently.
func TestDestinationIsCaseAndSpaceInsensitive(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")

	req := request(t, http.MethodPost, "https://b.example/f", body)
	sign(t, req, "a.example", "B.Example ", "key-1", key, body, now)

	if _, err := Verify(req, fakeLookup{"a.example": pub}, body, now, "b.example", nil); err != nil {
		t.Fatalf("a destination differing only in case was refused: %v", err)
	}
}

// The finding: a captured request stayed valid for the whole skew window. The
// nonce makes each signature single-use.
func TestACapturedRequestIsAcceptedOnlyOnce(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	signedAt := time.Unix(1_700_000_000, 0)
	body := []byte(`{"userId":"victim"}`)
	lookup := fakeLookup{"a.example": pub}
	guard := newGuard()

	req := request(t, http.MethodPost, "https://b.example/federation/v1/claim-key-packages", body)
	sign(t, req, "a.example", "b.example", "key-1", key, body, signedAt)

	if _, err := Verify(req, lookup, body, signedAt, "b.example", guard); err != nil {
		t.Fatalf("the first delivery was refused: %v", err)
	}
	// Replayed anywhere inside the window — must be refused every time.
	for i := 1; i < 5; i++ {
		at := signedAt.Add(time.Duration(i) * time.Minute)
		if _, err := Verify(req, lookup, body, at, "b.example", guard); err != ErrReplayed {
			t.Fatalf("replay at +%dm: err = %v, want ErrReplayed", i, err)
		}
	}
}

// Two peers must not share a nonce namespace: one peer using a nonce cannot make
// another peer's identical nonce look like a replay.
func TestNoncesAreScopedPerPeer(t *testing.T) {
	aKey, bKey := hostKey(t, 1), hostKey(t, 2)
	lookup := fakeLookup{
		"a.example": aKey.Public().(ed25519.PublicKey),
		"b.example": bKey.Public().(ed25519.PublicKey),
	}
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")
	guard := newGuard()

	first := request(t, http.MethodPost, "https://c.example/f", body)
	sign(t, first, "a.example", "c.example", "k", aKey, body, now)
	if _, err := Verify(first, lookup, body, now, "c.example", guard); err != nil {
		t.Fatalf("first peer refused: %v", err)
	}

	// The other peer sends a request carrying the SAME nonce value.
	second := request(t, http.MethodPost, "https://c.example/f", body)
	sign(t, second, "b.example", "c.example", "k", bKey, body, now)
	second.Header.Set(HeaderNonce, first.Header.Get(HeaderNonce))
	// Re-sign so the borrowed nonce is the one actually covered by the signature.
	resign(t, second, "b.example", "c.example", "k", bKey, body, now, first.Header.Get(HeaderNonce))

	if _, err := Verify(second, lookup, body, now, "c.example", guard); err != nil {
		t.Fatalf("a second peer's identical nonce was treated as a replay: %v", err)
	}
}

// An unavailable replay guard must fail closed: "we cannot tell whether this is a
// replay" is not a reason to accept one.
func TestAnUnavailableReplayGuardRefuses(t *testing.T) {
	key := hostKey(t, 1)
	pub := key.Public().(ed25519.PublicKey)
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")

	req := request(t, http.MethodPost, "https://b.example/f", body)
	sign(t, req, "a.example", "b.example", "key-1", key, body, now)

	guard := newGuard()
	guard.fail = context.DeadlineExceeded
	if _, err := Verify(req, fakeLookup{"a.example": pub}, body, now, "b.example", guard); err == nil {
		t.Fatal("a request was accepted while the replay guard was unavailable")
	}
}

// A nodelist entry with an unusable key must be a rejection, not a panic:
// ed25519.Verify panics on a wrong-sized key, so an unguarded lookup would take
// the request down instead of refusing it.
func TestAMalformedNodelistKeyIsRefusedNotFatal(t *testing.T) {
	key := hostKey(t, 1)
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")

	req := request(t, http.MethodPost, "https://b.example/f", body)
	sign(t, req, "a.example", "b.example", "key-1", key, body, now)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a malformed nodelist key panicked the verifier: %v", r)
		}
	}()
	short := fakeLookup{"a.example": ed25519.PublicKey(make([]byte, 5))}
	if _, err := Verify(req, short, body, now, "b.example", nil); err != ErrBadSignature {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// resign re-signs a request with an explicit nonce, for tests that need to
// control it.
func resign(t *testing.T, req *http.Request, origin, destination, keyID string,
	key ed25519.PrivateKey, body []byte, now time.Time, nonce string) {
	t.Helper()
	canon := canonicalString(req.Method, req.URL.Path, origin, destination, keyID,
		req.Header.Get(HeaderTimestamp), nonce, body)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, encodeSig(ed25519.Sign(key, []byte(canon))))
}
