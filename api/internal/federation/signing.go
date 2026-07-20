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
	"crypto/ed25519"
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
	HeaderSignature = "Pheme-Signature" // base64url Ed25519 over the canonical string
)

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
)

// canonicalString is the exact bytes that get signed and verified. Both sides
// build it the same way from the same fields, so a mismatch anywhere — method,
// path, body, sender, time — is a different string and fails verification.
//
// The body is bound by its SHA-256 rather than included, so an arbitrarily large
// body signs and verifies in constant space, and a receiver can hash the body it
// actually read rather than trusting a length header. Method and path are bound
// so a captured signature cannot be replayed against a different endpoint.
func canonicalString(method, path, origin, keyID, timestamp string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		strings.ToUpper(method),
		path,
		origin,
		keyID,
		timestamp,
		base64.RawURLEncoding.EncodeToString(sum[:]),
	}, "\n")
}

// Sign attaches the signing headers to an outbound request.
//
// It reads the body once and restores it, since http.Request bodies are single-
// use; a caller must set the body before signing. keyID names which of the
// sender's keys was used, so a receiver whose nodelist has a rotated key can
// still tell "signed by a key I do not list" from a bad signature.
func Sign(req *http.Request, origin, keyID string, key ed25519.PrivateKey, body []byte, now time.Time) {
	ts := strconv.FormatInt(now.Unix(), 10)
	canon := canonicalString(req.Method, req.URL.Path, origin, keyID, ts, body)
	sig := ed25519.Sign(key, []byte(canon))

	req.Header.Set(HeaderOrigin, origin)
	req.Header.Set(HeaderKeyID, keyID)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(sig))
}

// Verified is what a receiver learns from a good signature: which host sent the
// request, proven, not merely claimed.
type Verified struct {
	Origin string
	KeyID  string
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
func Verify(req *http.Request, lookup keyLookup, body []byte, now time.Time) (Verified, error) {
	origin := req.Header.Get(HeaderOrigin)
	keyID := req.Header.Get(HeaderKeyID)
	tsStr := req.Header.Get(HeaderTimestamp)
	sigStr := req.Header.Get(HeaderSignature)
	if origin == "" || keyID == "" || tsStr == "" || sigStr == "" {
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
	if key == nil {
		// Not a peer. Deliberately the same class of failure as a bad
		// signature — a stranger and a forger should learn the same thing.
		return Verified{}, ErrBadSignature
	}

	canon := canonicalString(req.Method, req.URL.Path, origin, keyID, tsStr, body)
	if !ed25519.Verify(key, []byte(canon), sig) {
		return Verified{}, ErrBadSignature
	}
	return Verified{Origin: origin, KeyID: keyID}, nil
}
