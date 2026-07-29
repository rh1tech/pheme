// Package federation is the host-to-host (S2S) layer: how one Pheme instance
// authenticates a request to a peer, and verifies one arriving from a peer,
// against the trust anchored in the signed nodelist.
//
// A request is signed with the sending host's Ed25519 key; the receiver looks
// the sender up in its nodelist and verifies against the key that vouches for
// that domain. mTLS would authenticate the transport, but half the point of
// this project is that hosts sit behind CDNs and reverse proxies that terminate
// TLS — so the signature is over the request itself, and does not depend on the
// terminating party being the application.
package federation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Header names. Lowercase-canonical is applied by net/http; these are the
// wire spellings.
const (
	HeaderOrigin    = "Pheme-Origin"    // the sending host's domain
	HeaderKeyID     = "Pheme-Key-Id"    // which of the sender's keys signed
	HeaderTimestamp = "Pheme-Timestamp" // unix seconds, replay bound
	HeaderNonce     = "Pheme-Nonce"     // per-request random, single-use within the skew window
	HeaderSignature = "Pheme-Signature" // base64url Ed25519 over the canonical string
)

// scheme labels what these bytes are, so this host's key signing an S2S request
// can never be confused with the same key signing something else (an ordering
// chain link, a token). Bump it if the canonical string's shape changes.
const scheme = "pheme-s2s-v2"

// nonceLen is the size of the per-request nonce. 16 bytes makes an accidental
// collision — which would reject an honest request — negligible over any
// realistic volume within one skew window.
const nonceLen = 16

// MaxSkew bounds how far a request's timestamp may be from the receiver's clock.
// A signed request is otherwise replayable forever; five minutes is loose enough
// for real clock drift and tight enough that a captured request is not a
// standing credential.
const MaxSkew = 5 * time.Minute

var (
	ErrMissingHeaders = errors.New("federation: request is missing signing headers")
	ErrBadTimestamp   = errors.New("federation: timestamp is malformed")
	ErrStale          = errors.New("federation: timestamp is outside the allowed skew")
	ErrBadSignature   = errors.New("federation: signature does not verify")
	ErrReplayed       = errors.New("federation: request nonce has already been used")
)

// canonicalString is the exact bytes that get signed and verified. Both sides
// build it the same way from the same fields, so a mismatch anywhere — method,
// path, body, sender, time — is a different string and fails verification.
//
// The body is bound by its SHA-256 rather than included, so an arbitrarily large
// body signs and verifies in constant space, and a receiver can hash the body it
// actually read rather than trusting a length header. Method and path are bound
// so a captured signature cannot be replayed against a different endpoint.
// DESTINATION is bound but travels in no header, and that is the point: the
// sender writes the domain it believes it is calling, the receiver writes its
// own, and a disagreement simply fails to verify. There is nothing on the wire
// for an intermediary to rewrite. Without this, every host serving the same
// route paths meant a request signed FOR one peer was a byte-identical valid
// request AT any other, which any peer could forward to be attributed to the
// original signer.
//
// NONCE makes the signature single-use. The timestamp bounds replay to the skew
// window, but a captured request stayed valid for the whole of it — long enough
// to drain a user's key packages or duplicate a message into an ordered log.
func canonicalString(method, path, origin, destination, keyID, timestamp, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		scheme,
		strings.ToUpper(method),
		path,
		origin,
		strings.ToLower(strings.TrimSpace(destination)),
		keyID,
		timestamp,
		nonce,
		base64.RawURLEncoding.EncodeToString(sum[:]),
	}, "\n")
}

// Sign attaches the signing headers to an outbound request.
//
// It reads the body once and restores it, since http.Request bodies are single-
// use; a caller must set the body before signing. keyID names which of the
// sender's keys was used, so a receiver whose nodelist has a rotated key can
// still tell "signed by a key I do not list" from a bad signature.
// destination is the peer domain this request is addressed to, as the nodelist
// spells it — NOT the URL's host, which may be a loopback address or a proxy.
func Sign(req *http.Request, origin, destination, keyID string, key ed25519.PrivateKey, body []byte, now time.Time) error {
	nonce, err := newNonce()
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	canon := canonicalString(req.Method, req.URL.Path, origin, destination, keyID, ts, nonce, body)
	sig := ed25519.Sign(key, []byte(canon))

	req.Header.Set(HeaderOrigin, origin)
	req.Header.Set(HeaderKeyID, keyID)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, encodeSig(sig))
	return nil
}

// encodeSig renders a signature the way the header carries it.
func encodeSig(sig []byte) string { return base64.RawURLEncoding.EncodeToString(sig) }

func newNonce() (string, error) {
	b := make([]byte, nonceLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("federation: nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Verified is what a receiver learns from a good signature: which host sent the
// request, proven, not merely claimed.
type Verified struct {
	Origin string
	KeyID  string
}

// ReplayGuard remembers which request nonces have been used. *idempotency.Memory
// and *idempotency.Redis both satisfy it — the question "have I already handled
// this exact request?" is the same one, and in production it must be answered
// from a store every instance shares, or a replay simply retries until it lands
// on a different instance.
type ReplayGuard interface {
	Seen(ctx context.Context, key string, window time.Duration) (bool, error)
}

// keyLookup answers "what key does the nodelist vouch for this domain with".
// *nodelist.Store satisfies it; the interface keeps this package from importing
// the store directly and makes verification trivially testable with a fake.
type keyLookup interface {
	KeyFor(domain string) (ed25519.PublicKey, error)
}

// Verify authenticates an inbound request against the nodelist.
//
// It reports WHO sent it, not merely that the signature was well-formed: the
// signing key is the one the nodelist vouches for the claimed origin, so a
// verified origin is a proven one. A domain that is not a peer, a key the
// nodelist does not list, a stale timestamp, or a body that does not match its
// bound hash all fail here.
// destination is this host's own domain. It is not read from the request — a
// receiver knows who it is, and taking it from the caller would defeat the
// binding entirely.
//
// replay, when non-nil, records each request's nonce for twice the skew window
// and refuses one it has already seen. Passing nil disables the check, which is
// correct only for tests and for a host with no shared store: a signature is
// then single-use in intent but not in enforcement.
func Verify(req *http.Request, lookup keyLookup, body []byte, now time.Time, destination string, replay ReplayGuard) (Verified, error) {
	origin := req.Header.Get(HeaderOrigin)
	keyID := req.Header.Get(HeaderKeyID)
	tsStr := req.Header.Get(HeaderTimestamp)
	nonce := req.Header.Get(HeaderNonce)
	sigStr := req.Header.Get(HeaderSignature)
	if origin == "" || keyID == "" || tsStr == "" || nonce == "" || sigStr == "" {
		return Verified{}, ErrMissingHeaders
	}

	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return Verified{}, ErrBadTimestamp
	}
	// Skew is checked before the signature so a replayed-but-valid signature
	// still expires; the check is cheap and leaks nothing.
	if d := now.Sub(time.Unix(ts, 0)); d > MaxSkew || d < -MaxSkew {
		return Verified{}, ErrStale
	}

	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sigStr, "="))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Verified{}, ErrBadSignature
	}

	key, err := lookup.KeyFor(origin)
	if err != nil {
		return Verified{}, fmt.Errorf("federation: %s: %w", origin, err)
	}
	// Not a peer, or a peer the nodelist vouches for with an unusable key.
	// Deliberately the same class of failure as a bad signature — a stranger and
	// a forger should learn the same thing. The length check is not decoration:
	// ed25519.Verify PANICS on a wrong-sized key, so a nodelist carrying a
	// malformed entry would otherwise take down the request rather than reject it.
	if len(key) != ed25519.PublicKeySize {
		return Verified{}, ErrBadSignature
	}

	canon := canonicalString(req.Method, req.URL.Path, origin, destination, keyID, tsStr, nonce, body)
	if !ed25519.Verify(key, []byte(canon), sig) {
		return Verified{}, ErrBadSignature
	}

	// Only after the signature verifies: an unauthenticated caller must not be
	// able to fill the replay store with nonces of its choosing.
	if replay != nil {
		// Scoped by origin so one peer cannot burn another's nonce, and by scheme
		// so this key space never collides with the ingest idempotency keys that
		// share the store.
		seen, err := replay.Seen(req.Context(), scheme+":"+origin+":"+nonce, 2*MaxSkew)
		if err != nil {
			// The guard is unavailable. Refuse rather than wave the request
			// through: "we cannot tell whether this is a replay" is not a reason
			// to accept one.
			return Verified{}, fmt.Errorf("federation: replay guard: %w", err)
		}
		if seen {
			return Verified{}, ErrReplayed
		}
	}
	return Verified{Origin: origin, KeyID: keyID}, nil
}
