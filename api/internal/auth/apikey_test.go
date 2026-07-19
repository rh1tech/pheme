package auth

import (
	"strings"
	"testing"
)

// API keys: the credential on the public ingest endpoint.
//
// A key is a bearer secret, so the only things protecting it are its entropy and the fact that only
// its hash is ever stored. Both were untested. The comparison is constant-time deliberately — the
// hashes are of high-entropy secrets so a timing side channel is a long way from practical, but the
// cost of doing it properly is nil and the cost of finding out you were wrong is a leaked key.

func TestAGeneratedKeyMatchesItsOwnHashAndNothingElse(t *testing.T) {
	plaintext, hash, prefix := GenerateAPIKey()

	if !EqualAPIKey(plaintext, hash) {
		t.Fatal("a freshly generated key does not match its own hash; no key would ever work")
	}
	if EqualAPIKey(plaintext+"x", hash) {
		t.Error("a key with an extra character matched")
	}
	if EqualAPIKey(plaintext[:len(plaintext)-1], hash) {
		t.Error("a truncated key matched")
	}

	// The prefix identifies a key to a human — "which of these is the one in the cron job" — and
	// must be a prefix of the real thing, short enough not to be the secret.
	if !strings.HasPrefix(plaintext, prefix) {
		t.Errorf("prefix %q is not a prefix of the key", prefix)
	}
	if len(prefix) >= len(plaintext) {
		t.Errorf("the prefix is the whole key (%d of %d characters); a listing that shows it "+
			"shows the secret", len(prefix), len(plaintext))
	}
}

// The stored form must not be the key. Anyone with read access to the database would otherwise hold
// every customer's credentials.
func TestTheStoredHashIsNotTheKey(t *testing.T) {
	plaintext, hash, _ := GenerateAPIKey()

	if hash == plaintext {
		t.Fatal("the stored hash IS the key")
	}
	if strings.Contains(hash, plaintext) || strings.Contains(plaintext, hash) {
		t.Error("the stored hash contains the key, or the reverse")
	}
	// Hex-encoded SHA-256.
	if len(hash) != 64 {
		t.Errorf("hash is %d characters, want 64 hex characters of SHA-256", len(hash))
	}
	if strings.Trim(hash, "0123456789abcdef") != "" {
		t.Errorf("hash %q is not hex", hash)
	}
}

// Keys must be unique and unguessable. A repeat would let one customer's key open another's channel.
func TestGeneratedKeysAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		plaintext, hash, _ := GenerateAPIKey()
		if seen[plaintext] {
			t.Fatalf("the same key was generated twice: %q", plaintext)
		}
		if seen[hash] {
			t.Fatalf("the same hash was generated twice")
		}
		seen[plaintext] = true
		seen[hash] = true

		// Enough entropy to be worth calling a secret. 24 random bytes, base64url, plus the prefix.
		if len(plaintext) < 30 {
			t.Fatalf("key %q is only %d characters", plaintext, len(plaintext))
		}
		if !strings.HasPrefix(plaintext, "phm_") {
			t.Errorf("key %q does not carry the phm_ prefix that identifies what it is", plaintext)
		}
	}
}

// Hashing is deterministic — the same key must produce the same hash, or a stored key would stop
// working the moment it was checked again.
func TestHashingIsDeterministic(t *testing.T) {
	const key = "phm_a-fixed-key-for-this-test"
	if HashAPIKey(key) != HashAPIKey(key) {
		t.Fatal("hashing the same key twice gave different answers")
	}
	if HashAPIKey(key) == HashAPIKey(key+"x") {
		t.Error("two different keys hash to the same value")
	}
}

// Surrounding whitespace is forgiven on the way in — a key pasted from a config file or a shell
// variable often carries a newline, and refusing it would be a support ticket rather than security.
func TestAPresentedKeyIsTrimmed(t *testing.T) {
	plaintext, hash, _ := GenerateAPIKey()

	for _, presented := range []string{
		plaintext, " " + plaintext, plaintext + "\n", "\t" + plaintext + "  \r\n",
	} {
		if !EqualAPIKey(presented, hash) {
			t.Errorf("a key with surrounding whitespace (%q) was refused", presented)
		}
	}
	// But whitespace INSIDE is a different key.
	if EqualAPIKey(plaintext[:5]+" "+plaintext[5:], hash) {
		t.Error("a key with whitespace inserted into the middle matched")
	}
}

// Nothing matches an empty or malformed stored hash. A row that lost its hash must not become a key
// that accepts anything.
func TestNothingMatchesAnEmptyOrBrokenStoredHash(t *testing.T) {
	plaintext, _, _ := GenerateAPIKey()

	for _, stored := range []string{"", "not-a-hash", strings.Repeat("0", 64)} {
		if EqualAPIKey(plaintext, stored) {
			t.Errorf("a key matched the stored value %q", stored)
		}
	}
	// And an empty presented key matches nothing either.
	_, hash, _ := GenerateAPIKey()
	for _, presented := range []string{"", "   ", "phm_"} {
		if EqualAPIKey(presented, hash) {
			t.Errorf("the empty-ish key %q was accepted", presented)
		}
	}
}
