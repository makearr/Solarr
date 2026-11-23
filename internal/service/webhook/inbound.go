package webhook

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"io"
)

// Action is what an incoming webhook may ask for; each only enqueues the job its button does.
type Action string

// The actions of an incoming webhook.
const (
	ActionScan     Action = "scan"
	ActionImport   Action = "import"
	ActionRefresh  Action = "refresh"
	ActionSearch   Action = "search"
	ActionPlayback Action = "playback"
)

// Actions lists the vocabulary in the order a person reads it.
var Actions = []Action{ActionScan, ActionImport, ActionRefresh, ActionSearch, ActionPlayback}

// KnownAction says the name is an action.
func KnownAction(name string) bool {
	for _, action := range Actions {
		if string(action) == name {
			return true
		}
	}
	return false
}

// HashOfToken is the stored form of a token.
func HashOfToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewToken makes 32 random bytes as a base64url token and its stored hash.
func NewToken(random io.Reader) (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(random, raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashOfToken(token), nil
}

// NewRandomToken is NewToken over the system's random source.
func NewRandomToken() (token, hash string, err error) { return NewToken(rand.Reader) }

// TokenMatches compares a presented token with a stored hash in constant time.
func TokenMatches(hash, presented string) bool {
	return subtle.ConstantTimeCompare([]byte(hash), []byte(HashOfToken(presented))) == 1
}
