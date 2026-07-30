package invite

import (
	"strings"
	"testing"
)

func TestGenerateCodeIsUniqueAndURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if seen[code] {
			t.Fatalf("duplicate code %q after %d draws", code, i)
		}
		seen[code] = true
		// Base64url, unpadded: anything else would need escaping to survive a link.
		if strings.ContainsAny(code, "+/= ") {
			t.Fatalf("code %q contains characters that do not survive a URL", code)
		}
		if len(code) < 20 {
			t.Fatalf("code %q is shorter than 128 bits of entropy would produce", code)
		}
	}
}

func TestHashCodeIgnoresSurroundingWhitespaceOnly(t *testing.T) {
	code := "AbCdEf-123_xyz"
	if HashCode(code) != HashCode("  "+code+"\n") {
		t.Fatal("a code pasted with stray whitespace did not match itself")
	}
	// Case IS significant — the alphabet is case-sensitive, and folding it would collapse
	// distinct codes onto one hash.
	if HashCode(code) == HashCode(strings.ToLower(code)) {
		t.Fatal("hashing is case-insensitive; distinct codes collide")
	}
}

func TestEqualCode(t *testing.T) {
	code, err := GenerateCode()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	hash := HashCode(code)
	if !EqualCode(code, hash) {
		t.Fatal("a code did not match its own hash")
	}
	if EqualCode(code+"x", hash) {
		t.Fatal("a different code matched")
	}
	if EqualCode("", hash) {
		t.Fatal("an empty code matched")
	}
}

func TestPrefixIsShortAndNonSecret(t *testing.T) {
	code, err := GenerateCode()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	p := Prefix(code)
	if len(p) != PrefixLen || !strings.HasPrefix(code, p) {
		t.Fatalf("prefix %q is not the first %d characters of %q", p, PrefixLen, code)
	}
	// Shorter than the prefix length: return what there is rather than panicking on a slice.
	if got := Prefix("ab"); got != "ab" {
		t.Fatalf("Prefix(%q) = %q, want %q", "ab", got, "ab")
	}
}
