// Package otp holds short-lived email-verification artifacts: pending signups
// (registration awaiting a code), password-reset codes, and per-email send
// cooldowns. It follows the project's interface + Memory/real-adapter + driver
// convention; the Memory implementation is the zero-dependency default and the
// Redis implementation is used in production so state survives multiple API
// instances.
//
// Verification codes are never stored in plaintext — only their SHA-256 hash is
// persisted, so a leaked datastore dump does not reveal live codes.
package otp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// MaxAttempts is the number of wrong code entries allowed before a pending
// verification is invalidated and the user must request a new code.
const MaxAttempts = 3

// ErrNotFound is returned when no pending artifact exists for the given email.
var ErrNotFound = errors.New("not found")

// Signup is a pending registration awaiting email verification. No user row is
// created until the code is confirmed.
type Signup struct {
	Email        string
	PasswordHash string
	CodeHash     string
	Attempts     int
}

// Reset is a pending password reset awaiting code confirmation.
type Reset struct {
	Email    string
	UserID   string
	CodeHash string
	Attempts int
}

// Store persists pending signups, password-reset codes, and send cooldowns.
type Store interface {
	PutSignup(ctx context.Context, s Signup, ttl time.Duration) error
	GetSignup(ctx context.Context, email string) (Signup, error)
	// IncrSignupAttempts increments and returns the new attempt count, or
	// ErrNotFound if no pending signup exists.
	IncrSignupAttempts(ctx context.Context, email string) (int, error)
	DelSignup(ctx context.Context, email string) error

	PutReset(ctx context.Context, r Reset, ttl time.Duration) error
	GetReset(ctx context.Context, email string) (Reset, error)
	IncrResetAttempts(ctx context.Context, email string) (int, error)
	DelReset(ctx context.Context, email string) error

	// CooldownOK atomically marks key as used for ttl and reports whether the
	// caller may proceed: true means allowed (and the marker was set); false
	// means the key is still within its cooldown window.
	CooldownOK(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// GenerateCode returns a cryptographically random 6-digit numeric code,
// zero-padded (e.g. "004271").
func GenerateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// HashCode returns the hex SHA-256 of a code, suitable for storage.
func HashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// EqualCode reports whether code matches a stored hash in constant time.
func EqualCode(code, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashCode(code)), []byte(hash)) == 1
}
