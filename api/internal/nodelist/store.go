package nodelist

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// Store holds the nodelist a host currently trusts, verified against the
// coordinator key, and lets it be replaced by a newer one.
//
// It is the read side every federated request goes through: "is this domain a
// peer, and what key vouches for it". Safe for concurrent use — the list is
// read on every S2S request and replaced rarely.
type Store struct {
	coord ed25519.PublicKey

	mu     sync.RWMutex
	list   List
	loaded bool
}

// ErrNoList is returned by lookups before any list has been loaded, so a caller
// can tell "not a peer" (a known list that omits the domain) from "we do not
// yet know who the peers are" (no list at all) — a host with no list must refuse
// federation, not silently treat everyone as absent.
var ErrNoList = errors.New("nodelist: no list loaded")

// NewStore creates a store that trusts lists signed by coord.
func NewStore(coord ed25519.PublicKey) *Store { return &Store{coord: coord} }

// Replace verifies a signed document and adopts it, refusing to move backwards.
//
// The serial check is the rollback defence: an attacker who can feed a host an
// old-but-validly-signed list — one that still contains a since-removed peer —
// gains nothing, because a lower serial than the one in hand is refused. Equal
// serial is also refused, so a replay of the current list is a no-op rather than
// a reset of any per-list state a future caller might keep.
func (s *Store) Replace(doc string) error {
	l, err := Verify(doc, s.coord)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded && l.Serial <= s.list.Serial {
		return errServialNotNewer(s.list.Serial, l.Serial)
	}
	s.list = l
	s.loaded = true
	return nil
}

// LoadFile reads and adopts a signed list from disk. Used at startup and by a
// refresh that pulls the mirror to a local file.
func (s *Store) LoadFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return s.Replace(string(b))
}

// KeyFor returns the signing key a peer domain is vouched for by, or false if
// the domain is not a peer. Returns ErrNoList before any list is loaded.
func (s *Store) KeyFor(domain string) (ed25519.PublicKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded {
		return nil, ErrNoList
	}
	// A list past its expiry stops vouching for anyone: an expired list is a
	// host that has fallen out of contact with the coordinator, and it must not
	// keep honouring a roster that may have had a compromised peer removed
	// since. This is checked on read, not only on load, so a long-running host
	// notices its list going stale.
	if time.Now().After(s.list.Expires) {
		return nil, ErrExpired
	}
	if key, ok := s.list.Lookup(domain); ok {
		return key, nil
	}
	return nil, nil // loaded, valid, and this domain is simply not a peer
}

// IsPeer reports whether a domain is a currently-trusted peer.
func (s *Store) IsPeer(domain string) bool {
	key, err := s.KeyFor(domain)
	return err == nil && key != nil
}

// DomainForAlias resolves a host alias to its domain from the loaded list. It
// returns false when there is no list, the list has expired, or no host carries
// the alias — an alias only means something while the list vouching for it is
// valid, exactly as a key does.
func (s *Store) DomainForAlias(alias string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded || time.Now().After(s.list.Expires) {
		return "", false
	}
	return s.list.DomainForAlias(alias)
}

// AliasForDomain returns a host's alias, or false if it has none (or no valid
// list is loaded). For rendering a remote member as `name@alias`.
func (s *Store) AliasForDomain(domain string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded || time.Now().After(s.list.Expires) {
		return "", false
	}
	return s.list.AliasForDomain(domain)
}

// HostInfo is one host as a client needs to see it: its domain and its optional
// alias. It deliberately omits the key — a browser does not verify S2S signatures.
type HostInfo struct {
	Domain string `json:"domain"`
	Alias  string `json:"alias,omitempty"`
}

// Hosts lists every host in the loaded list, so a client can offer aliases when a
// user types a cross-host handle. Empty when no valid list is loaded.
func (s *Store) Hosts() []HostInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.loaded || time.Now().After(s.list.Expires) {
		return nil
	}
	out := make([]HostInfo, 0, len(s.list.Nodes))
	for _, n := range s.list.Nodes {
		out = append(out, HostInfo{Domain: n.Domain, Alias: n.Alias})
	}
	return out
}

// Serial is the serial of the loaded list, or 0 if none.
func (s *Store) Serial() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.list.Serial
}

func errServialNotNewer(have, got uint64) error {
	return fmt.Errorf("nodelist: refusing a list with serial %s when serial %s is already loaded",
		strconv.FormatUint(got, 10), strconv.FormatUint(have, 10))
}
