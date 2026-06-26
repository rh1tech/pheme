package auth

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Password policy constants. The server is the source of truth; web and mobile
// clients mirror these rules for a strength meter, but acceptance is decided
// here.
const (
	// MinPasswordLength is the minimum acceptable password length.
	MinPasswordLength = 8
	// MaxPasswordLength bounds input so Argon2id is never handed a huge string.
	MaxPasswordLength = 200
	// minCharacterClasses is how many of {lower, upper, digit, symbol} a
	// password must include.
	minCharacterClasses = 2
)

// ErrWeakPassword indicates a password failed the strength policy. The wrapped
// message is safe to surface to the user.
var ErrWeakPassword = errors.New("weak password")

// commonPasswords is a small embedded blocklist of the most frequently breached
// passwords. Kept intentionally short — a "light" check, not a full dictionary.
var commonPasswords = map[string]bool{
	"password": true, "password1": true, "password123": true, "passw0rd": true,
	"12345678": true, "123456789": true, "1234567890": true, "12341234": true,
	"qwerty123": true, "qwertyuiop": true, "1q2w3e4r": true, "1qaz2wsx": true,
	"iloveyou": true, "admin123": true, "welcome1": true, "welcome123": true,
	"letmein1": true, "abc12345": true, "football": true, "baseball": true,
	"sunshine": true, "princess": true, "dragon123": true, "monkey123": true,
	"trustno1": true, "starwars": true, "whatever": true, "changeme": true,
	"superman": true, "michael1": true, "11111111": true, "00000000": true,
}

// ValidatePassword enforces the password policy: a minimum length, at least two
// character classes, and rejection of well-known weak passwords. It returns an
// error wrapping ErrWeakPassword with a user-facing reason, or nil when the
// password is acceptable.
func ValidatePassword(pw string) error {
	if len(pw) < MinPasswordLength {
		return fmt.Errorf("%w: use at least %d characters", ErrWeakPassword, MinPasswordLength)
	}
	if len(pw) > MaxPasswordLength {
		return fmt.Errorf("%w: must be at most %d characters", ErrWeakPassword, MaxPasswordLength)
	}
	if commonPasswords[strings.ToLower(pw)] {
		return fmt.Errorf("%w: this password is too common", ErrWeakPassword)
	}
	if characterClasses(pw) < minCharacterClasses {
		return fmt.Errorf("%w: mix letters with numbers or symbols", ErrWeakPassword)
	}
	return nil
}

// characterClasses counts how many of lower, upper, digit, and symbol appear.
func characterClasses(pw string) int {
	var lower, upper, digit, symbol bool
	for _, r := range pw {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		default:
			symbol = true
		}
	}
	n := 0
	for _, present := range []bool{lower, upper, digit, symbol} {
		if present {
			n++
		}
	}
	return n
}
