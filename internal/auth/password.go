// Package auth turns a request into an identity: local passwords, API keys and
// trusted proxy headers (ADR-14). It decides *who* is asking; internal/authz
// decides what they may see.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters. OWASP's minimum recommendation at the time of writing:
// enough work to make offline cracking expensive, little enough that a login on
// a NAS is not noticeable. They are encoded into every hash, so raising them
// later re-hashes on next login rather than invalidating stored passwords.
const (
	hashMemoryKiB  = 19456 // 19 MiB
	hashIterations = 2
	hashThreads    = 1
	hashSaltBytes  = 16
	hashKeyBytes   = 32
)

var (
	// ErrPasswordMismatch reports a wrong password. It is deliberately the same
	// error whatever was wrong, so nothing distinguishes a bad password from an
	// unknown user.
	ErrPasswordMismatch = errors.New("auth: password does not match")

	// ErrMalformedHash reports a stored hash this binary cannot read.
	ErrMalformedHash = errors.New("auth: stored password hash is malformed")
)

// HashPassword returns an encoded argon2id hash, parameters included.
func HashPassword(password string) (string, error) {
	salt := make([]byte, hashSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, hashIterations, hashMemoryKiB, hashThreads, hashKeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, hashMemoryKiB, hashIterations, hashThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against an encoded hash.
//
// The comparison is constant-time. The parameters come from the hash rather
// than from the constants above, so a password stored under older settings
// still verifies.
func VerifyPassword(password, encoded string) error {
	parsed, err := parseHash(encoded)
	if err != nil {
		return err
	}

	candidate := argon2.IDKey([]byte(password), parsed.salt,
		parsed.iterations, parsed.memoryKiB, parsed.threads, uint32(len(parsed.key)))
	if subtle.ConstantTimeCompare(candidate, parsed.key) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// NeedsRehash reports whether a stored hash used weaker parameters than the
// current ones, so a successful login can quietly upgrade it.
func NeedsRehash(encoded string) bool {
	parsed, err := parseHash(encoded)
	if err != nil {
		return true
	}
	return parsed.memoryKiB < hashMemoryKiB ||
		parsed.iterations < hashIterations ||
		parsed.threads < hashThreads
}

type parsedHash struct {
	memoryKiB  uint32
	iterations uint32
	threads    uint8
	salt       []byte
	key        []byte
}

func parseHash(encoded string) (parsedHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return parsedHash{}, ErrMalformedHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return parsedHash{}, ErrMalformedHash
	}

	var parsed parsedHash
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&parsed.memoryKiB, &parsed.iterations, &parsed.threads); err != nil {
		return parsedHash{}, ErrMalformedHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return parsedHash{}, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return parsedHash{}, ErrMalformedHash
	}
	parsed.salt, parsed.key = salt, key
	return parsed, nil
}
