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
	// Verify reports whether password matches a hash made by Hash. A false result
	// with a nil error is a plain mismatch; an error means the stored hash is unusable.
	Verify(ctx context.Context, password, phc string) (bool, error)
}

// Argon2idHasher produces PHC-format Argon2id hashes. A semaphore limits
// concurrent hashes so a burst of registrations cannot exhaust memory (Architecture 8.1).
type Argon2idHasher struct {
	sem chan struct{}
}

func NewArgon2idHasher() *Argon2idHasher {
	return &Argon2idHasher{sem: make(chan struct{}, maxConcurrentHashes)}
}

// acquire takes a hashing slot. Verify shares it with Hash: a login burst costs
// the same memory as a registration burst, so both must be bounded together.
func (h *Argon2idHasher) acquire(ctx context.Context) (release func(), err error) {
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *Argon2idHasher) Verify(ctx context.Context, password, phc string) (bool, error) {
	// An oversized password cannot match (registration caps it), so skip the work
	// an unauthenticated caller could otherwise ask for.
	if len(password) > maxPasswordBytes {
		return false, nil
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return verifyPassword(password, phc)
}

func (h *Argon2idHasher) Hash(ctx context.Context, password string) (string, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := argon2.IDKey([]byte(normalizePassword(password)), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// phcHash is a parsed Argon2id PHC string.
type phcHash struct {
	m, t      uint32
	p         uint8
	salt, key []byte
}

// parsePHC reads a PHC string produced by Hash. It lives next to Hash so the format
// has one owner. Errors never echo the input: it may be a secret.
func parsePHC(phc string) (phcHash, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return phcHash{}, fmt.Errorf("unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return phcHash{}, fmt.Errorf("unsupported argon2 version")
	}
	var h phcHash
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &h.m, &h.t, &h.p); err != nil {
		return phcHash{}, fmt.Errorf("bad argon2 parameters")
	}
	enc := base64.RawStdEncoding
	var err error
	if h.salt, err = enc.DecodeString(parts[4]); err != nil {
		return phcHash{}, fmt.Errorf("bad salt")
	}
	if h.key, err = enc.DecodeString(parts[5]); err != nil {
		return phcHash{}, fmt.Errorf("bad hash")
	}
	return h, nil
}

// Bounds for hashes we accept from outside (the Admin seed file). They stop a typo or a
// hostile file from making every login of that account allocate gigabytes.
const (
	minSaltLen   = 8
	minKeyLen    = 16
	maxArgonMem  = 256 * 1024 // KiB
	maxArgonTime = 10
	maxArgonPar  = 8
)

// checkPHC reports whether phc is a well-formed Argon2id hash with sane parameters.
func checkPHC(phc string) error {
	h, err := parsePHC(phc)
	if err != nil {
		return err
	}
	if len(h.salt) < minSaltLen || len(h.key) < minKeyLen {
		return fmt.Errorf("salt or hash too short")
	}
	if h.m == 0 || h.m > maxArgonMem || h.t == 0 || h.t > maxArgonTime || h.p == 0 || h.p > maxArgonPar {
		return fmt.Errorf("argon2 parameters out of range")
	}
	return nil
}

// verifyPassword checks password against a PHC string produced by Hash.
func verifyPassword(password, phc string) (bool, error) {
	h, err := parsePHC(phc)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(normalizePassword(password)), h.salt, h.t, h.m, h.p, uint32(len(h.key)))
	return subtle.ConstantTimeCompare(got, h.key) == 1, nil
}
