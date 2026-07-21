package mlschain

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

func TestLinkIsDeterministic(t *testing.T) {
	a := Link([]byte("prev"), 3, "grp", []byte("commit"))
	b := Link([]byte("prev"), 3, "grp", []byte("commit"))
	if !bytes.Equal(a, b) {
		t.Fatal("same inputs must produce the same hash")
	}
}

// Every field changes the hash — position, group, and content are all bound.
func TestLinkBindsEveryField(t *testing.T) {
	base := Link([]byte("prev"), 3, "grp", []byte("commit"))
	cases := map[string][]byte{
		"different prevHash": Link([]byte("PREV"), 3, "grp", []byte("commit")),
		"different seq":      Link([]byte("prev"), 4, "grp", []byte("commit")),
		"different groupID":  Link([]byte("prev"), 3, "GRP", []byte("commit")),
		"different commit":   Link([]byte("prev"), 3, "grp", []byte("COMMIT")),
	}
	for name, got := range cases {
		if bytes.Equal(base, got) {
			t.Errorf("%s must change the hash but did not", name)
		}
	}
}

// The length-prefixing must stop a boundary from being slid between adjacent
// fields — the classic concatenation ambiguity.
func TestLinkResistsFieldBoundaryAmbiguity(t *testing.T) {
	x := Link([]byte("ab"), 1, "c", nil)
	y := Link([]byte("a"), 1, "bc", nil)
	if bytes.Equal(x, y) {
		t.Fatal("('ab','c') and ('a','bc') must not collide")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	hash := Link(nil, 1, "grp", []byte("commit"))
	sig := Sign(priv, hash)
	if !Verify(pub, hash, sig) {
		t.Fatal("a hub signature must verify under its own key")
	}
}

func TestVerifyRejectsWrongKeyTamperAndEmpty(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	other, _, _ := ed25519.GenerateKey(nil)
	hash := Link(nil, 1, "grp", []byte("commit"))
	sig := Sign(priv, hash)

	if Verify(other, hash, sig) {
		t.Error("a signature must not verify under a different host's key")
	}
	if Verify(pub, Link(nil, 2, "grp", []byte("commit")), sig) {
		t.Error("a signature must not verify against a different hash")
	}
	if Verify(pub, hash, nil) {
		t.Error("a missing signature must never read as valid")
	}
	if Verify(nil, hash, sig) {
		t.Error("a missing key must never verify")
	}
}
