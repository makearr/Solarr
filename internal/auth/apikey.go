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
func GenerateAPIKey() (secrets.Secret, string, error) {
	raw := make([]byte, apiKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("auth: generate api key: %w", err)
	}
	key := apiKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return secrets.Secret(key), HashAPIKey(key), nil
}

// HashAPIKey is the stored form.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// LooksLikeAPIKey reports whether a credential is shaped like one of ours,
// so an obviously wrong value is rejected without a database lookup.
func LooksLikeAPIKey(candidate string) bool {
	return strings.HasPrefix(candidate, apiKeyPrefix) && len(candidate) > len(apiKeyPrefix)+16
}
