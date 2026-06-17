// Package auth provides credential helpers: API key generation/hashing and a
// password hashing interface. The password implementation here is a development
// placeholder — production must use Argon2id (golang.org/x/crypto/argon2).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// GenerateAPIKey returns a new high-entropy secret of the form "phm_<random>"
// and its storage hash. The plaintext is shown to the user once; only the hash
// is persisted.
func GenerateAPIKey() (plaintext, hash, prefix string) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	secret := base64.RawURLEncoding.EncodeToString(b)
	plaintext = "phm_" + secret
	hash = HashAPIKey(plaintext)
	prefix = plaintext[:min(12, len(plaintext))]
	return plaintext, hash, prefix
}

// HashAPIKey returns the hex-encoded SHA-256 of an API key. API keys are high
// entropy, so a fast hash with constant-time comparison is appropriate.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// EqualAPIKey reports whether a presented key matches a stored hash.
func EqualAPIKey(presented, storedHash string) bool {
	got := HashAPIKey(strings.TrimSpace(presented))
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}
