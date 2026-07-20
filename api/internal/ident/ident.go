// Package ident builds and parses the qualified identifiers used across a
// federated network of Pheme instances.
//
// An identifier says which host is authoritative for the thing it names:
//
//	mimi://a.example/u/alice        a user
//	mimi://a.example/d/<deviceId>   a device
//	mimi://a.example/r/<roomId>     a room
//	mimi://a.example/g/<groupId>    an MLS group
//
// The form is the IETF MIMI working group's (draft-ietf-mimi-protocol). Note it
// is NOT `im:user@domain` — earlier drafts used that and the current one
// replaced it with this hierarchical scheme, where the domain sits in the
// authority component.
//
// These are WIRE identifiers, derived rather than stored. Documents keep their
// local opaque ids and gain a domain; rewriting every primary key would be a far
// larger migration for no benefit, because qualification matters at boundaries,
// not in indexes.
package ident

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Scheme is the URI scheme all identifiers carry.
const Scheme = "mimi"

// Kind is the single-letter path segment naming what an identifier refers to.
type Kind string

const (
	KindUser   Kind = "u"
	KindDevice Kind = "d"
	KindRoom   Kind = "r"
	KindGroup  Kind = "g"
)

func (k Kind) valid() bool {
	switch k {
	case KindUser, KindDevice, KindRoom, KindGroup:
		return true
	}
	return false
}

// ID is a parsed identifier.
type ID struct {
	// Domain is the authoritative host, always lowercase. This is the trust
	// anchor: it is what the nodelist keys on and what a certificate
	// authenticates, so it must be exactly a host — no port, no userinfo.
	Domain string
	Kind   Kind
	// Local is the host-local identifier, opaque and case-sensitive.
	Local string
}

var (
	ErrEmpty      = errors.New("ident: empty identifier")
	ErrScheme     = errors.New("ident: not a mimi:// identifier")
	ErrDomain     = errors.New("ident: missing or malformed domain")
	ErrKind       = errors.New("ident: missing or unknown kind")
	ErrLocal      = errors.New("ident: missing local part")
	ErrExtraneous = errors.New("ident: identifier carries a query, fragment, port or userinfo")
	// ErrNotCanonical is returned for input that is not already in the exact
	// form String() produces. Parse(s).String() == s holds for everything Parse
	// accepts, so an identifier has one spelling and string equality and parsed
	// equality can never disagree.
	ErrNotCanonical = errors.New("ident: identifier is not in canonical form")
)

// User builds a user identifier.
func User(domain, local string) ID { return ID{Domain: lower(domain), Kind: KindUser, Local: local} }

// Device builds a device identifier.
func Device(domain, local string) ID {
	return ID{Domain: lower(domain), Kind: KindDevice, Local: local}
}

// Room builds a room identifier.
func Room(domain, local string) ID { return ID{Domain: lower(domain), Kind: KindRoom, Local: local} }

// Group builds an MLS group identifier.
func Group(domain, local string) ID {
	return ID{Domain: lower(domain), Kind: KindGroup, Local: local}
}

// String renders the identifier. The local part is percent-encoded, so a local
// id containing a reserved character survives a round trip instead of parsing
// back as something else.
func (id ID) String() string {
	return fmt.Sprintf("%s://%s/%s/%s", Scheme, id.Domain, id.Kind, encodeLocal(id.Local))
}

// IsLocal reports whether this identifier belongs to the given host. DNS is
// case-insensitive, so a host configured with different case is the same host —
// getting that wrong would have a server treat itself as a peer.
func (id ID) IsLocal(domain string) bool { return id.Domain == lower(domain) }

// Parse reads an identifier, rejecting anything that is not exactly one.
func Parse(raw string) (ID, error) {
	if strings.TrimSpace(raw) == "" {
		return ID{}, ErrEmpty
	}
	// One identifier, one spelling. url.Parse tolerates a raw space and silently
	// encodes it, so "…/u/a b" and "…/u/a%20b" would both parse to the same ID
	// while comparing unequal as strings — the same class of ambiguity that
	// ports and userinfo are rejected for. Anything outside printable ASCII
	// must arrive percent-encoded, which also means IDN domains arrive as
	// punycode.
	for i := 0; i < len(raw); i++ {
		if raw[i] <= 0x20 || raw[i] >= 0x7f {
			return ID{}, ErrNotCanonical
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ID{}, fmt.Errorf("ident: %w", err)
	}
	if u.Scheme != Scheme {
		return ID{}, ErrScheme
	}
	// A port would let two identifiers name one host and compare unequal;
	// userinfo, a query or a fragment have no meaning here and would be a way
	// to smuggle differences past an equality check.
	if u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ID{}, ErrExtraneous
	}
	domain := lower(u.Hostname())
	if !validDomain(domain) {
		return ID{}, ErrDomain
	}

	// Exactly two segments: the kind and the local part. Splitting the ESCAPED
	// path keeps a percent-encoded slash inside the local part from reading as
	// a segment boundary.
	rest := strings.TrimPrefix(u.EscapedPath(), "/")
	kindStr, localRaw, found := strings.Cut(rest, "/")
	if !found {
		return ID{}, ErrKind
	}
	kind := Kind(kindStr)
	if !kind.valid() {
		return ID{}, ErrKind
	}
	if strings.Contains(localRaw, "/") {
		return ID{}, ErrLocal
	}
	local, err := url.PathUnescape(localRaw)
	if err != nil {
		return ID{}, fmt.Errorf("ident: %w", err)
	}
	if local == "" {
		return ID{}, ErrLocal
	}
	return ID{Domain: domain, Kind: kind, Local: local}, nil
}

// PairKey is the deduplication key for a conversation between two users: one
// value however the pair is ordered.
//
// It replaces a "a:b" string join, which cannot survive identifiers that
// contain the separator — and worse, silently collides: ("x", "y:z") and
// ("x:y", "z") both join to "x:y:z", so two different pairs would share a
// conversation. Hashing length-prefixed parts makes that impossible regardless
// of what characters an identifier contains.
func PairKey(a, b ID) string {
	x, y := a.String(), b.String()
	if x > y {
		x, y = y, x
	}
	h := sha256.New()
	for _, s := range []string{x, y} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// encodeLocal percent-encodes a local part for use as one path segment.
// url.PathEscape leaves '/' alone, which would create a second segment.
func encodeLocal(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), "/", "%2F")
}

// validDomain accepts a hostname or an IP literal, and nothing else. Deliberately
// strict: this value decides which host is trusted for an identifier.
func validDomain(d string) bool {
	if d == "" || len(d) > 253 {
		return false
	}
	if net.ParseIP(d) != nil {
		return true
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
			if !ok {
				return false
			}
		}
	}
	// A bare label ("localhost") is a host, but not one that can be a trust
	// anchor across a network, so require at least one dot.
	return strings.Contains(d, ".")
}
