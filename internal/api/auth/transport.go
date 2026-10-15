package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// SealedPrefix marks a value the browser encrypted for the server: "encv1:<ephemeral key>:<nonce>:<ciphertext>".
const SealedPrefix = "encv1:"

// ErrUnsealable is a sealed value that does not open: tampered, truncated, or sealed for another key.
var ErrUnsealable = errors.New("auth: a sealed value could not be opened")

// The HKDF labels; they version the scheme, so a change of either breaks every browser still holding the old page.
const (
	transportKeyInfo  = "solarr:transport-key:v1"
	transportSealInfo = "solarr:transport-seal:v1"
)

// Transport opens secrets the browser sealed for this server, so a password or API key never crosses plain HTTP in the
// clear: X25519 with a fresh browser key per value, HKDF-SHA256, AES-256-GCM. It stops a passive listener, not an
// active one; TLS at a reverse proxy is still the answer to that (documentation/web-interface.md).
type Transport struct {
	private *ecdh.PrivateKey
}

// NewTransport derives the server's X25519 key from the encryption key, so it is stable across restarts.
func NewTransport(encryptionKey []byte) (*Transport, error) {
	seed, err := hkdf.Key(sha256.New, encryptionKey, nil, transportKeyInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("auth: transport key: %w", err)
	}
	private, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		return nil, fmt.Errorf("auth: transport key: %w", err)
	}
	return &Transport{private: private}, nil
}

// PublicKey is the server's X25519 public key, base64, for the browser to seal against.
func (t *Transport) PublicKey() string {
	return base64.StdEncoding.EncodeToString(t.private.PublicKey().Bytes())
}

// IsSealed says whether a value carries the sealed prefix.
func IsSealed(value string) bool { return strings.HasPrefix(value, SealedPrefix) }

// Open returns a sealed value's plaintext.
func (t *Transport) Open(value string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(value, SealedPrefix), ":")
	if !IsSealed(value) || len(parts) != 3 {
		return "", ErrUnsealable
	}
	var raw [3][]byte
	for i, part := range parts {
		decoded, err := base64.StdEncoding.DecodeString(part)
		if err != nil {
			return "", ErrUnsealable
		}
		raw[i] = decoded
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(raw[0])
	if err != nil {
		return "", ErrUnsealable
	}
	shared, err := t.private.ECDH(ephemeral)
	if err != nil {
		return "", ErrUnsealable
	}
	plain, err := openGCM(shared, raw[1], raw[2])
	if err != nil {
		return "", ErrUnsealable
	}
	return string(plain), nil
}

// sealKey derives the AES-256 key of one sealed value from its shared secret.
func sealKey(shared []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, shared, nil, transportSealInfo, 32)
}

func openGCM(shared, nonce, ciphertext []byte) ([]byte, error) {
	key, err := sealKey(shared)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, ErrUnsealable
	}
	return aead.Open(nil, nonce, ciphertext, nil)
}
