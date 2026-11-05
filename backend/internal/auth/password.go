package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (Architecture 9.1: OWASP minimum, tuned later). They are
// embedded in the PHC string, so raising them later does not break old hashes.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16

	// maxPasswordBytes bounds the work an unauthenticated caller can ask for.
	maxPasswordBytes = 128
	// maxConcurrentHashes caps memory: each hash holds argonMemoryKiB at once.
	maxConcurrentHashes = 4
)

// Hasher turns a password into a storable one-way string (NFR-SEC-003).
type Hasher interface {
	Hash(ctx context.Context, password string) (string, error)
}

// Argon2idHasher produces PHC-format Argon2id hashes. A semaphore limits
// concurrent hashes so a burst of registrations cannot exhaust memory (Architecture 8.1).
type Argon2idHasher struct {
	sem chan struct{}
}

func NewArgon2idHasher() *Argon2idHasher {
	return &Argon2idHasher{sem: make(chan struct{}, maxConcurrentHashes)}
}

func (h *Argon2idHasher) Hash(ctx context.Context, password string) (string, error) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// verifyPassword checks password against a PHC string produced by Hash. Login
// (a later ticket) will use it; it lives here so the format has one owner.
func verifyPassword(password, phc string) (bool, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version")
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, fmt.Errorf("bad argon2 parameters: %w", err)
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("bad salt: %w", err)
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("bad hash: %w", err)
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
