package nodelist

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

func coordKey(t *testing.T, seed byte) ed25519.PrivateKey {
	t.Helper()
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed
	}
	return ed25519.NewKeyFromSeed(s)
}

func sampleList() List {
	return List{
		Serial:  7,
		Issued:  time.Unix(1_700_000_000, 0).UTC(),
		Expires: time.Unix(1_800_000_000, 0).UTC(),
		Nodes: []Node{
			{Domain: "a.example", PublicKey: pk(1)},
			{Domain: "b.example", PublicKey: pk(2)},
		},
	}
}

// pk returns a deterministic public key for a seed.
func pk(seed byte) ed25519.PublicKey {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed
	}
	return ed25519.NewKeyFromSeed(s).Public().(ed25519.PublicKey)
}

func TestSignedListVerifies(t *testing.T) {
	coord := coordKey(t, 9)
	signed, err := Sign(sampleList(), coord)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(signed, coord.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("a list this key signed did not verify: %v", err)
	}
	if got.Serial != 7 || len(got.Nodes) != 2 {
		t.Errorf("round trip lost data: %+v", got)
	}
}

// The whole point of the signature: a list from the wrong coordinator is
// refused. Without this, any host could publish a nodelist adding itself.
func TestListFromAnotherCoordinatorIsRefused(t *testing.T) {
	real := coordKey(t, 9)
	imposter := coordKey(t, 10)

	signed, err := Sign(sampleList(), imposter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(signed, real.Public().(ed25519.PublicKey)); err == nil {
		t.Fatal("a list signed by the wrong key was accepted")
	}
}

// A single flipped byte anywhere in the payload must fail verification, or the
// signature guarantees nothing about what it covers.
func TestTamperedListIsRefused(t *testing.T) {
	coord := coordKey(t, 9)
	signed, err := Sign(sampleList(), coord)
	if err != nil {
		t.Fatal(err)
	}
	pub := coord.Public().(ed25519.PublicKey)

	// Flip a byte in the middle of the document.
	b := []byte(signed)
	b[len(b)/2] ^= 0x01
	if _, err := Verify(string(b), pub); err == nil {
		t.Fatal("a tampered list verified")
	}
}

// Adding a node to a signed list without re-signing must be caught. This is the
// exact attack the signature exists to stop: a host cannot add itself.
func TestAppendingANodeInvalidatesTheSignature(t *testing.T) {
	coord := coordKey(t, 9)
	signed, err := Sign(sampleList(), coord)
	if err != nil {
		t.Fatal(err)
	}
	pub := coord.Public().(ed25519.PublicKey)

	// Splice an extra node into the JSON payload, leaving the signature line.
	tampered := strings.Replace(signed,
		`"domain":"b.example"`,
		`"domain":"evil.example","publicKey":"`+encodeKey(pk(66))+`"},{"domain":"b.example"`,
		1)
	if tampered == signed {
		t.Fatal("test did not modify the document")
	}
	if _, err := Verify(tampered, pub); err == nil {
		t.Fatal("a node added after signing was accepted")
	}
}

func TestLookupFindsANode(t *testing.T) {
	l := sampleList()
	key, ok := l.Lookup("a.example")
	if !ok {
		t.Fatal("known node not found")
	}
	if !key.Equal(pk(1)) {
		t.Error("wrong key returned")
	}
	if _, ok := l.Lookup("unknown.example"); ok {
		t.Error("unknown node reported as present")
	}
}

// Domains are matched case-insensitively: DNS is, and the nodelist keys trust on
// the domain, so "A.Example" and "a.example" must resolve to one entry.
func TestLookupIsCaseInsensitive(t *testing.T) {
	l := sampleList()
	if _, ok := l.Lookup("A.Example"); !ok {
		t.Error("case difference defeated the lookup")
	}
}

// A list past its expiry is refused. A nodelist is a revocation mechanism as
// much as a trust one -- a compromised host is removed by issuing a new list
// without it -- so an old list must not be honoured forever, or removal never
// takes effect.
func TestExpiredListIsRefused(t *testing.T) {
	coord := coordKey(t, 9)
	l := sampleList()
	l.Expires = time.Unix(1_700_000_100, 0).UTC() // just after Issued
	signed, err := Sign(l, coord)
	if err != nil {
		t.Fatal(err)
	}
	pub := coord.Public().(ed25519.PublicKey)

	if _, err := VerifyAt(signed, pub, l.Expires.Add(time.Second)); err == nil {
		t.Fatal("an expired list was accepted")
	}
	if _, err := VerifyAt(signed, pub, l.Issued.Add(time.Second)); err != nil {
		t.Fatalf("a list within its window was refused: %v", err)
	}
}

// Serial is monotonic so a host can refuse to replace a newer list with an
// older one -- a rollback attack, feeding a host a stale list that still lists a
// since-removed node. Verify does not enforce this itself (it has no memory);
// it surfaces the serial so the caller can.
func TestSerialSurvives(t *testing.T) {
	coord := coordKey(t, 9)
	l := sampleList()
	l.Serial = 42
	signed, err := Sign(l, coord)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(signed, coord.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if got.Serial != 42 {
		t.Errorf("serial = %d, want 42", got.Serial)
	}
}

// A duplicate domain is rejected at sign time: two entries for one host, with
// different keys, is ambiguous about which key to trust, and the safe reading
// is to trust neither.
func TestSigningRejectsDuplicateDomains(t *testing.T) {
	l := sampleList()
	l.Nodes = append(l.Nodes, Node{Domain: "a.example", PublicKey: pk(3)})
	if _, err := Sign(l, coordKey(t, 9)); err == nil {
		t.Fatal("a list with a duplicate domain was signed")
	}
}

func TestSigningRejectsAMalformedNode(t *testing.T) {
	for name, mut := range map[string]func(*List){
		"empty domain":          func(l *List) { l.Nodes[0].Domain = "" },
		"bad domain":            func(l *List) { l.Nodes[0].Domain = "not a domain" },
		"short key":             func(l *List) { l.Nodes[0].PublicKey = []byte{1, 2, 3} },
		"expires before issued": func(l *List) { l.Expires = l.Issued.Add(-time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			l := sampleList()
			mut(&l)
			if _, err := Sign(l, coordKey(t, 9)); err == nil {
				t.Fatalf("%s was signed", name)
			}
		})
	}
}
