package nodelist

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

// End to end: a list is signed, written to a file, and a fresh Store loaded from
// that file recognises exactly the peers it names and no others -- the whole
// point of F1, exercised through disk the way a host actually loads it.
func TestSignWriteLoadRoundTrip(t *testing.T) {
	coord := coordKey(t, 3)
	pub := coord.Public().(ed25519.PublicKey)

	doc, err := Sign(sampleList(), coord)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "list.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(pub)
	if err := s.LoadFile(path); err != nil {
		t.Fatalf("a host could not load a freshly signed list: %v", err)
	}
	if !s.IsPeer("a.example") || !s.IsPeer("b.example") {
		t.Error("a named peer was not recognised after load")
	}
	if s.IsPeer("c.example") {
		t.Error("an unnamed host was treated as a peer")
	}

	// A different coordinator key must not load the same file.
	other := NewStore(coordKey(t, 4).Public().(ed25519.PublicKey))
	if err := other.LoadFile(path); err == nil {
		t.Error("a host trusting the wrong coordinator loaded the list")
	}
}
