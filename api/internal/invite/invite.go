// Package invite mints and verifies the one-time registration codes an invite-only server
// hands out. It is deliberately tiny: the storage lives in the store package and the policy
// in the auth and admin handlers — this is only the code itself.
//
// Codes are never stored in plaintext, only their SHA-256 hash, so a leaked database dump
// does not hand the reader a working set of invitations.
package invite

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// codeBytes is the entropy behind one invite. 16 bytes is 128 bits, which is not guessable
// and stays short enough to survive being pasted into a chat message by hand.
const codeBytes = 16

// PrefixLen is how much of a code is kept in the clear, so an admin list can distinguish
// rows. Short enough to be useless on its own.
const PrefixLen = 6

// GenerateCode returns a fresh URL-safe invite code.
func GenerateCode() (string, error) {
	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate invite code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Normalize trims the incidental damage a code takes on its way through a link, an email
// client and a paste buffer. It does NOT change case: the alphabet is case-sensitive.
func Normalize(code string) string {
	return strings.TrimSpace(code)
}

// HashCode returns the hex SHA-256 of a code, suitable for storage and for lookup. It is
// deterministic on purpose — an invite is found BY its hash, so a per-row salt (as used for
// passwords) would force a scan of every invite on every registration.
func HashCode(code string) string {
	sum := sha256.Sum256([]byte(Normalize(code)))
	return hex.EncodeToString(sum[:])
}

// EqualCode reports whether code matches a stored hash, in constant time.
func EqualCode(code, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashCode(code)), []byte(hash)) == 1
}

// Prefix is the leading, non-secret fragment of a code kept alongside its hash.
func Prefix(code string) string {
	c := Normalize(code)
	if len(c) <= PrefixLen {
		return c
	}
	return c[:PrefixLen]
}
