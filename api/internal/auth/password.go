package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. These balance security and latency for interactive
// logins; tune for the deployment's hardware.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash is returned when a stored password hash is malformed.
var ErrInvalidHash = errors.New("invalid password hash format")

// maxArgonMemory is the largest memory parameter this will honour from a stored hash, in KiB.
//
// VerifyPassword takes its cost parameters from the hash it is checking — that is what PHC format
// is for, and it is how a deployment raises its parameters without invalidating every existing
// password. But it means the allocation size is read from stored data, so it needs a ceiling.
// Without one, a single malformed or hostile row turns one login attempt into an out-of-memory
// kill of the whole process.
const maxArgonMemory = 256 * 1024 // 256 MiB

// hashSlots bounds how many Argon2 derivations run at once.
//
// Each one allocates argonMemory — 64 MiB — for the duration, and nothing else in the login path
// allocates anything close. Unbounded, a thundering herd sizes itself: a thousand clients
// reconnecting after a deploy would ask for 64 GB between them and the kernel would kill the
// server, which is precisely when it is least able to afford being killed.
//
// Measured, not guessed: a load run provisioning a thousand accounts held 832 MB live in
// argon2.initBlocks, 99% of the process heap, from only sixteen concurrent hashes.
//
// Waiters queue rather than fail. A slow login is a worse experience than a fast one and a much
// better one than an outage, and a goroutine waiting for a slot costs nothing until it gets one.
var hashSlots = make(chan struct{}, maxConcurrentHashes())

func maxConcurrentHashes() int {
	// Argon2 is CPU-bound as well as memory-hungry, and already uses argonThreads internally, so
	// there is nothing to gain from more concurrent derivations than the machine has cores. The
	// floor of 2 keeps a single-core container from serialising logins completely.
	if n := runtime.NumCPU(); n > 2 {
		return n
	}
	return 2
}

// withHashSlot runs fn holding one of the bounded derivation slots.
func withHashSlot[T any](fn func() T) T {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return fn()
}

// HashPassword returns an Argon2id PHC-formatted hash of the password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := withHashSlot(func() []byte {
		return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	})
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the PHC-formatted hash.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	var mem, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return false, ErrInvalidHash
	}
	// The allocation size comes from stored data, so it is bounded before it is used. A hash
	// claiming m=17179869184 must be refused as malformed, not honoured with a 16 TiB allocation.
	if mem > maxArgonMemory || mem == 0 || threads == 0 || time == 0 {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}
	got := withHashSlot(func() []byte {
		return argon2.IDKey([]byte(password), salt, time, mem, threads, uint32(len(want)))
	})
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
