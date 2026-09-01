package auth

import (
	"encoding/base64"

	"golang.org/x/crypto/argon2"
)

// WeakFixtureKeyForTest builds a hash under deliberately weak parameters, so
// the test for verifying against older settings uses a real argon2id output
// rather than a literal that would silently rot.
func WeakFixtureKeyForTest(password, salt string) string {
	key := argon2.IDKey([]byte(password), []byte(salt), 1, 8, 1, hashKeyBytes)
	return base64.RawStdEncoding.EncodeToString(key)
}
