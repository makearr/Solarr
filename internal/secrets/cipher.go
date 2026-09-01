package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// ErrCorrupt reports ciphertext that failed authentication — a truncated value,
// a bit flip, or a value encrypted under a different key. The three are
// indistinguishable by design, so the message does not guess between them.
var ErrCorrupt = errors.New("secrets: value could not be decrypted")

// Cipher seals and opens credentials with AES-256-GCM.
//
// AES-GCM rather than an external AEAD because it is in the standard library
// and needs no CGO, which ADR-30 requires of the shipped binary. It is
// authenticated, so a tampered value fails rather than decrypting to rubbish.
type Cipher struct {
	aead cipher.AEAD
}

// New returns a Cipher for a 32-byte key, as produced by config.GenerateKey.
func New(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		// The key is not echoed: it is the key (Invariant 61).
		return nil, fmt.Errorf("secrets: encryption key is %d bytes, want 32", len(key))
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: initialise AES-GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Seal encrypts a secret for storage. A fresh random nonce is prepended to each
// value, so encrypting the same password twice produces different ciphertext —
// otherwise a settings.db would reveal which accounts share a password.
func (c *Cipher) Seal(secret Secret) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secrets: generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(secret), nil), nil
}

// Open decrypts a stored value.
func (c *Cipher) Open(sealed []byte) (Secret, error) {
	nonceSize := c.aead.NonceSize()
	if len(sealed) < nonceSize {
		return "", fmt.Errorf("%w: value is %d bytes, shorter than a nonce", ErrCorrupt, len(sealed))
	}

	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// The underlying error is not wrapped: it says nothing useful and
		// distinguishing "wrong key" from "tampered" is not something a
		// message should guess at.
		return "", ErrCorrupt
	}
	return Secret(plaintext), nil
}
