package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/makearr/MediaNexus/internal/secrets"
)

// apiKeyPrefix makes a leaked key recognisable in a log or a paste, so it can
// be revoked rather than puzzled over.
const apiKeyPrefix = "mnx_"

// apiKeyBytes is 256 bits of entropy.
const apiKeyBytes = 32

// GenerateAPIKey returns the key to show the user once, and the hash to store.
//
// The plaintext is never stored and cannot be recovered: the API shows it at
// creation and never again.
func GenerateAPIKey() (secrets.Secret, string, error) {
	raw := make([]byte, apiKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("auth: generate api key: %w", err)
	}
	key := apiKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return secrets.Secret(key), HashAPIKey(key), nil
}

// HashAPIKey is the stored form.
//
// SHA-256 rather than argon2id, deliberately. Password hashing is slow because
// passwords have little entropy and must survive an offline dictionary attack;
// an API key is 256 random bits, where a dictionary attack has nothing to work
// with. Making key lookup slow would only add an argon2 computation to every
// authenticated request.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// LooksLikeAPIKey reports whether a credential is shaped like one of ours,
// so an obviously wrong value is rejected without a database lookup.
func LooksLikeAPIKey(candidate string) bool {
	return strings.HasPrefix(candidate, apiKeyPrefix) && len(candidate) > len(apiKeyPrefix)+16
}
