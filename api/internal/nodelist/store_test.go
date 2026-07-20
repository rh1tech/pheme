package nodelist

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func signedAt(t *testing.T, coord ed25519.PrivateKey, serial uint64, issued, expires time.Time, nodes ...Node) string {
	t.Helper()
	doc, err := Sign(List{Serial: serial, Issued: issued, Expires: expires, Nodes: nodes}, coord)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestStoreRefusesLookupsBeforeAnyListIsLoaded(t *testing.T) {
	s := NewStore(coordKey(t, 1).Public().(ed25519.PublicKey))
	// The distinction that matters: not "absent", but "we do not yet know the
	// peers". A host with no list must refuse federation, not treat the world
	// as empty.
	if _, err := s.KeyFor("a.example"); !errors.Is(err, ErrNoList) {
		t.Errorf("err = %v, want ErrNoList", err)
	}
	if s.IsPeer("a.example") {
		t.Error("a domain was a peer before any list was loaded")
	}
}

func TestStoreAdoptsAndLooksUp(t *testing.T) {
	coord := coordKey(t, 1)
	s := NewStore(coord.Public().(ed25519.PublicKey))
	now := time.Now()
	doc := signedAt(t, coord, 1, now.Add(-time.Hour), now.Add(time.Hour),
		Node{Domain: "a.example", PublicKey: pk(5)})
	if err := s.Replace(doc); err != nil {
		t.Fatal(err)
	}
	key, err := s.KeyFor("a.example")
	if err != nil {
		t.Fatal(err)
	}
	if !key.Equal(pk(5)) {
		t.Error("wrong key")
	}
	if !s.IsPeer("a.example") {
		t.Error("known host not a peer")
	}
	if s.IsPeer("stranger.example") {
		t.Error("unknown host reported as peer")
	}
}

// The rollback defence. A validly-signed but OLDER list -- one that still lists
// a peer since removed -- must be refused, or removal could be undone by
// replaying yesterday's list.
func TestStoreRefusesAnOlderSerial(t *testing.T) {
	coord := coordKey(t, 1)
	s := NewStore(coord.Public().(ed25519.PublicKey))
	now := time.Now()

	newer := signedAt(t, coord, 5, now.Add(-time.Hour), now.Add(time.Hour))
	older := signedAt(t, coord, 4, now.Add(-time.Hour), now.Add(time.Hour),
		Node{Domain: "removed.example", PublicKey: pk(9)})

	if err := s.Replace(newer); err != nil {
		t.Fatal(err)
	}
	if err := s.Replace(older); err == nil {
		t.Fatal("an older list replaced a newer one")
	}
	// And the removed host did not sneak back in.
	if s.IsPeer("removed.example") {
		t.Error("a since-removed host became a peer via an old list")
	}
}

// Replaying the current list is refused too, so it cannot reset any per-list
// state a caller keeps.
func TestStoreRefusesAnEqualSerial(t *testing.T) {
	coord := coordKey(t, 1)
	s := NewStore(coord.Public().(ed25519.PublicKey))
	now := time.Now()
	doc := signedAt(t, coord, 3, now.Add(-time.Hour), now.Add(time.Hour))
	if err := s.Replace(doc); err != nil {
		t.Fatal(err)
	}
	if err := s.Replace(doc); err == nil {
		t.Fatal("re-applying the same serial was allowed")
	}
}

// A list from the wrong coordinator never gets adopted, so the store's contents
// are only ever things the trusted key signed.
func TestStoreRefusesAForeignList(t *testing.T) {
	real := coordKey(t, 1)
	s := NewStore(real.Public().(ed25519.PublicKey))
	imposter := coordKey(t, 2)
	now := time.Now()
	doc := signedAt(t, imposter, 1, now.Add(-time.Hour), now.Add(time.Hour),
		Node{Domain: "evil.example", PublicKey: pk(9)})
	if err := s.Replace(doc); err == nil {
		t.Fatal("a foreign-signed list was adopted")
	}
	if s.IsPeer("evil.example") {
		t.Error("a host from a foreign list became a peer")
	}
}

// An expired list stops vouching on READ, not only at load time, so a
// long-running host whose list ages out notices rather than trusting a stale
// roster forever.
func TestStoreStopsTrustingAnExpiredList(t *testing.T) {
	coord := coordKey(t, 1)
	s := NewStore(coord.Public().(ed25519.PublicKey))

	// Adopt a list that is valid now but expires very soon. Replace verifies
	// against time.Now(), so it must still be within the window at adoption.
	now := time.Now()
	doc := signedAt(t, coord, 1, now.Add(-time.Hour), now.Add(50*time.Millisecond),
		Node{Domain: "a.example", PublicKey: pk(5)})
	if err := s.Replace(doc); err != nil {
		t.Fatal(err)
	}
	if !s.IsPeer("a.example") {
		t.Fatal("peer not trusted while the list was valid")
	}

	time.Sleep(80 * time.Millisecond)

	if _, err := s.KeyFor("a.example"); !errors.Is(err, ErrExpired) {
		t.Errorf("err = %v, want ErrExpired once the list aged out", err)
	}
	if s.IsPeer("a.example") {
		t.Error("an expired list still vouched for a peer")
	}
}
