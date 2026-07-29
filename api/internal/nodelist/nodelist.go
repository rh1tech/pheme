// Package nodelist is the signed map of who is in the federated network.
//
// Every host mirrors the same list: a set of `domain -> public key` entries,
// signed as one document by a coordinator key that every host is configured to
// trust. It is how a host tells a real peer's word from a stranger's, and it is
// also the revocation mechanism — a compromised host is removed by issuing a new
// list without it, which is why a list carries an expiry and a serial.
//
// This is faithful to FidoNet's nodelist: compiled and signed centrally,
// mirrored everywhere. The tradeoff is that admission is centralised even though
// hosting is not; the alternative, open federation, has no answer to spam and
// abuse on day one. See docs/development/federation.md, Decision 3.
package nodelist

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Node is one host's entry.
type Node struct {
	Domain    string            `json:"domain"`
	PublicKey ed25519.PublicKey `json:"publicKey"`
	// Alias is an optional short, network-wide name for this host, so a user can
	// be addressed as `name@pheme1` instead of `name@a-long-host.example.tld`. It
	// carries no trust — the domain and its key are what authenticate a host — it
	// is purely a friendlier spelling the whole network agrees on because it is
	// signed into the same list. Empty means the host is addressed by domain only.
	Alias string `json:"alias,omitempty"`
}

// List is the full set, as one signable document.
type List struct {
	// Serial increases with every issue, so a host can refuse to replace a
	// newer list with an older one — a rollback that would re-admit a removed
	// host. Verify surfaces it; enforcing monotonicity is the caller's job,
	// since a stateless verifier has no "newer" to compare against.
	Serial uint64 `json:"serial"`

	Issued  time.Time `json:"issued"`
	Expires time.Time `json:"expires"`

	Nodes []Node `json:"nodes"`
}

// signedDoc is the wire form: the list plus a detached signature over its
// canonical bytes. The signature is separate from the payload it covers, so the
// exact bytes that were signed can be reconstructed and re-verified.
type signedDoc struct {
	List      json.RawMessage `json:"list"`
	Signature string          `json:"sig"`
}

var (
	ErrSignature = errors.New("nodelist: signature does not verify")
	ErrExpired   = errors.New("nodelist: list has expired")
	ErrNotYet    = errors.New("nodelist: list is not yet valid")
	ErrMalformed = errors.New("nodelist: malformed document")
)

// Sign validates a list and returns it as a signed document.
//
// Validation happens here, at the one point a list is created, rather than at
// every point it is read: a signed list is trusted precisely because the
// coordinator vouched for it, so the coordinator's tool is where a malformed
// entry must be caught.
func Sign(l List, coord ed25519.PrivateKey) (string, error) {
	if err := l.validate(); err != nil {
		return "", err
	}
	payload, err := canonical(l)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(coord, payload)
	doc := signedDoc{List: payload, Signature: encodeKey(sig)}
	out, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Verify checks a signed document against the trusted coordinator key and the
// current time, returning the list.
func Verify(doc string, coord ed25519.PublicKey) (List, error) {
	return VerifyAt(doc, coord, time.Now())
}

// VerifyAt is Verify at an explicit time, for tests and for a host whose clock
// is the thing under test.
func VerifyAt(doc string, coord ed25519.PublicKey, now time.Time) (List, error) {
	var sd signedDoc
	if err := json.Unmarshal([]byte(doc), &sd); err != nil {
		return List{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	sig, err := decodeKey(sd.Signature)
	if err != nil {
		return List{}, fmt.Errorf("%w: signature: %v", ErrMalformed, err)
	}
	// Verify over the raw bytes as they arrived, not over a re-marshalling of
	// the parsed list — a round trip through a struct could reorder or
	// reformat and break a signature that is actually valid.
	if !ed25519.Verify(coord, sd.List, sig) {
		return List{}, ErrSignature
	}
	var l List
	if err := json.Unmarshal(sd.List, &l); err != nil {
		return List{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	// Re-validate, even though Sign already did. Trusting the coordinator is not
	// the same as trusting whatever tool produced the bytes it signed, and the
	// failure is not graceful: a node carrying a wrong-sized key flows through
	// KeyFor into ed25519.Verify, which PANICS rather than returning false. A
	// trust anchor that can be made to crash the verifier by being merely
	// malformed is worse than one that refuses to load.
	if err := l.validate(); err != nil {
		return List{}, err
	}
	// Time is checked only after the signature, so an attacker cannot learn
	// anything by probing with malformed documents.
	if now.After(l.Expires) {
		return List{}, ErrExpired
	}
	if now.Before(l.Issued) {
		return List{}, ErrNotYet
	}
	return l, nil
}

// Lookup returns the public key for a domain, matched case-insensitively.
func (l List) Lookup(domain string) (ed25519.PublicKey, bool) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	for _, n := range l.Nodes {
		if strings.ToLower(n.Domain) == domain {
			return n.PublicKey, true
		}
	}
	return nil, false
}

// DomainForAlias returns the domain a host alias names, case-insensitively, or
// false if no host carries that alias.
func (l List) DomainForAlias(alias string) (string, bool) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return "", false
	}
	for _, n := range l.Nodes {
		if strings.ToLower(n.Alias) == alias {
			return n.Domain, true
		}
	}
	return "", false
}

// AliasForDomain returns a host's alias, case-insensitively, or false if it has
// none. The inverse of DomainForAlias, for rendering a member as `name@alias`.
func (l List) AliasForDomain(domain string) (string, bool) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	for _, n := range l.Nodes {
		if strings.ToLower(n.Domain) == domain && n.Alias != "" {
			return n.Alias, true
		}
	}
	return "", false
}

func (l List) validate() error {
	if l.Expires.Before(l.Issued) || l.Expires.Equal(l.Issued) {
		return fmt.Errorf("%w: expiry is not after issue", ErrMalformed)
	}
	seen := make(map[string]struct{}, len(l.Nodes))
	domains := make(map[string]struct{}, len(l.Nodes))
	aliases := make(map[string]struct{}, len(l.Nodes))
	for _, n := range l.Nodes {
		d := strings.ToLower(strings.TrimSpace(n.Domain))
		if !validDomain(d) {
			return fmt.Errorf("%w: bad domain %q", ErrMalformed, n.Domain)
		}
		if len(n.PublicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: %q has a %d-byte key", ErrMalformed, n.Domain, len(n.PublicKey))
		}
		// A domain twice, with two keys, is ambiguous about which to trust, and
		// the safe reading of ambiguity in a trust anchor is to trust neither.
		if _, dup := seen[d]; dup {
			return fmt.Errorf("%w: %q appears twice", ErrMalformed, n.Domain)
		}
		seen[d] = struct{}{}
		domains[d] = struct{}{}
		if n.Alias != "" {
			a := strings.ToLower(strings.TrimSpace(n.Alias))
			if !validAlias(a) {
				return fmt.Errorf("%w: bad alias %q", ErrMalformed, n.Alias)
			}
			// An alias must resolve to exactly one host, and must not be readable
			// as a domain — either would make `name@x` ambiguous, the same reason
			// a duplicate domain is rejected.
			if _, dup := aliases[a]; dup {
				return fmt.Errorf("%w: alias %q appears twice", ErrMalformed, n.Alias)
			}
			aliases[a] = struct{}{}
		}
	}
	// Cross-check aliases against domains only after every domain is known, so
	// order in the list does not matter.
	for a := range aliases {
		if _, clash := domains[a]; clash {
			return fmt.Errorf("%w: alias %q collides with a host domain", ErrMalformed, a)
		}
	}
	return nil
}

// canonical renders a list the one way, so the bytes that get signed are the
// bytes any host reconstructs. json.Marshal is deterministic for these types
// (struct field order fixed, no maps), which is what makes a detached signature
// over the payload sound.
func canonical(l List) ([]byte, error) { return json.Marshal(l) }

func encodeKey(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decodeKey(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// MarshalJSON / UnmarshalJSON put the key on the wire as base64url rather than a
// JSON array of bytes, which is both smaller and what a human editing the list
// would expect to see.
func (n Node) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Domain    string `json:"domain"`
		PublicKey string `json:"publicKey"`
		Alias     string `json:"alias,omitempty"`
	}{n.Domain, encodeKey(n.PublicKey), n.Alias})
}

func (n *Node) UnmarshalJSON(b []byte) error {
	var raw struct {
		Domain    string `json:"domain"`
		PublicKey string `json:"publicKey"`
		Alias     string `json:"alias"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	key, err := decodeKey(raw.PublicKey)
	if err != nil {
		return fmt.Errorf("nodelist: node %q key: %w", raw.Domain, err)
	}
	n.Domain = raw.Domain
	n.PublicKey = key
	n.Alias = raw.Alias
	return nil
}

// validAlias accepts a short, dot-free host handle: 2–32 chars of lowercase
// letters, digits and hyphens, starting alphanumeric. Dot-free is what keeps an
// alias distinguishable from a domain (a domain always contains a dot).
func validAlias(a string) bool {
	if len(a) < 2 || len(a) > 32 {
		return false
	}
	for i := 0; i < len(a); i++ {
		c := a[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
		if !ok {
			return false
		}
	}
	first, last := a[0], a[len(a)-1]
	alnum := func(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') }
	return alnum(first) && alnum(last)
}

func validDomain(d string) bool {
	if d == "" || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	if net.ParseIP(d) != nil {
		return false // a trust anchor is a name, not an address
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
	}
	return true
}
