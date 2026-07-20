package ident

import "testing"

func TestParseRoundTrip(t *testing.T) {
	cases := []string{
		"mimi://a.example/u/alice",
		"mimi://a.example/d/dev-1",
		"mimi://a.example/r/room-1",
		"mimi://a.example/g/group-1",
		"mimi://sub.domain.example.co.uk/u/bob",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			id, err := Parse(raw)
			if err != nil {
				t.Fatalf("Parse(%q) = %v", raw, err)
			}
			if got := id.String(); got != raw {
				t.Errorf("round trip = %q, want %q", got, raw)
			}
		})
	}
}

func TestParseExtractsTheParts(t *testing.T) {
	id, err := Parse("mimi://a.example/u/alice")
	if err != nil {
		t.Fatal(err)
	}
	if id.Domain != "a.example" {
		t.Errorf("Domain = %q", id.Domain)
	}
	if id.Kind != KindUser {
		t.Errorf("Kind = %q", id.Kind)
	}
	if id.Local != "alice" {
		t.Errorf("Local = %q", id.Local)
	}
}

// The domain is what says which host is authoritative for an identifier, so it
// is the one part that must never be ambiguous. Case is normalised because DNS
// is case-insensitive and two spellings of one host must not read as two hosts.
func TestParseLowercasesTheDomainButNotTheLocalPart(t *testing.T) {
	id, err := Parse("mimi://A.Example/u/Alice")
	if err != nil {
		t.Fatal(err)
	}
	if id.Domain != "a.example" {
		t.Errorf("Domain = %q, want lowercased", id.Domain)
	}
	// Local parts are opaque. Lowercasing them would silently merge two
	// distinct identifiers on hosts that treat them as distinct.
	if id.Local != "Alice" {
		t.Errorf("Local = %q, want untouched", id.Local)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":                "",
		"wrong scheme":         "https://a.example/u/alice",
		"no scheme":            "a.example/u/alice",
		"missing domain":       "mimi:///u/alice",
		"missing kind":         "mimi://a.example/alice",
		"unknown kind":         "mimi://a.example/x/alice",
		"missing local":        "mimi://a.example/u/",
		"trailing segment":     "mimi://a.example/u/alice/extra",
		"domain with slash":    "mimi://a.example/x//u/alice",
		"domain with port":     "mimi://a.example:443/u/alice",
		"domain with userinfo": "mimi://user@a.example/u/alice",
		"space in local":       "mimi://a.example/u/al ice",
		"domain not a host":    "mimi://not_a_domain/u/alice",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if id, err := Parse(raw); err == nil {
				t.Errorf("Parse(%q) = %v, want an error", raw, id)
			}
		})
	}
}

// A port would let two identifiers name the same host and compare unequal, and
// the domain is a trust anchor -- it is what the nodelist keys on and what a
// certificate authenticates. It has to be exactly the host and nothing else.
func TestParseRejectsAnythingButABareHost(t *testing.T) {
	for _, raw := range []string{
		"mimi://a.example:443/u/alice",
		"mimi://alice@a.example/u/alice",
		"mimi://a.example/u/alice?q=1",
		"mimi://a.example/u/alice#frag",
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", raw)
		}
	}
}

// Percent-encoding must survive, or a local part containing a reserved
// character would parse to something other than what was built.
func TestLocalPartsWithReservedCharactersSurvive(t *testing.T) {
	for _, local := range []string{"a/b", "a:b", "a?b", "a#b", "a%b", "a b"} {
		id := User("a.example", local)
		back, err := Parse(id.String())
		if err != nil {
			t.Fatalf("Parse(%q) = %v", id.String(), err)
		}
		if back.Local != local {
			t.Errorf("local %q round-tripped to %q via %q", local, back.Local, id.String())
		}
	}
}

func TestConstructors(t *testing.T) {
	if got := User("a.example", "alice").String(); got != "mimi://a.example/u/alice" {
		t.Errorf("User = %q", got)
	}
	if got := Device("a.example", "d1").String(); got != "mimi://a.example/d/d1" {
		t.Errorf("Device = %q", got)
	}
	if got := Room("a.example", "r1").String(); got != "mimi://a.example/r/r1" {
		t.Errorf("Room = %q", got)
	}
	if got := Group("a.example", "g1").String(); got != "mimi://a.example/g/g1" {
		t.Errorf("Group = %q", got)
	}
}

func TestIsLocal(t *testing.T) {
	id := User("a.example", "alice")
	if !id.IsLocal("a.example") {
		t.Error("same domain reported as remote")
	}
	// DNS is case-insensitive; a host configured with different case is the
	// same host, and treating it as remote would federate a server with itself.
	if !id.IsLocal("A.Example") {
		t.Error("case difference reported as remote")
	}
	if id.IsLocal("b.example") {
		t.Error("different domain reported as local")
	}
}

// The pair key replaces DirectKey's "a:b" string join, which cannot survive
// identifiers that contain the separator. Order must not matter: {a,b} and
// {b,a} are one conversation.
func TestPairKeyIsOrderIndependent(t *testing.T) {
	a := User("a.example", "alice")
	b := User("b.example", "bob")
	if PairKey(a, b) != PairKey(b, a) {
		t.Error("PairKey depends on argument order")
	}
}

func TestPairKeyDistinguishesDifferentPairs(t *testing.T) {
	a := User("a.example", "alice")
	b := User("b.example", "bob")
	c := User("a.example", "carol")
	if PairKey(a, b) == PairKey(a, c) {
		t.Error("different pairs collided")
	}
}

// The failure a string join invites: two DIFFERENT pairs whose concatenations
// are identical. With "a:b" joins, ("x", "y:z") and ("x:y", "z") both produce
// "x:y:z". Hashing length-prefixed parts makes that impossible.
func TestPairKeyResistsSeparatorConfusion(t *testing.T) {
	one := PairKey(User("h", "x"), User("h", "y:z"))
	two := PairKey(User("h", "x:y"), User("h", "z"))
	if one == two {
		t.Error("two different pairs produced the same key")
	}
}

func TestPairKeyDistinguishesSameNameOnDifferentHosts(t *testing.T) {
	// The entire point of qualifying identifiers: alice on one host is not
	// alice on another, and a conversation with each is two conversations.
	x := PairKey(User("a.example", "alice"), User("c.example", "carol"))
	y := PairKey(User("b.example", "alice"), User("c.example", "carol"))
	if x == y {
		t.Error("same local name on different hosts collided")
	}
}

// Parse(s).String() == s for everything Parse accepts. Without this an
// identifier could have two spellings, and string equality and parsed equality
// would disagree — which for a value that decides WHICH HOST IS TRUSTED is the
// kind of ambiguity that becomes a security bug rather than a formatting one.
func TestEverythingParseAcceptsIsAlreadyCanonical(t *testing.T) {
	locals := []string{
		"alice", "Alice", "a/b", "a:b", "a?b", "a#b", "a%b", "a b",
		"24charhexlookingid0000000", "with-dash", "with.dot", "with_underscore",
		"%2F", "%20", "..", ".",
	}
	kinds := []Kind{KindUser, KindDevice, KindRoom, KindGroup}
	for _, local := range locals {
		for _, kind := range kinds {
			built := ID{Domain: "a.example", Kind: kind, Local: local}.String()
			parsed, err := Parse(built)
			if err != nil {
				t.Fatalf("Parse(%q) = %v — String produced something Parse rejects", built, err)
			}
			if got := parsed.String(); got != built {
				t.Errorf("not canonical: %q parsed and re-rendered to %q", built, got)
			}
			if parsed.Local != local {
				t.Errorf("local %q survived as %q", local, parsed.Local)
			}
		}
	}
}
