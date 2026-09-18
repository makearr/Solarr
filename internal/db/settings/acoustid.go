package settings

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// KeyAcoustIDAPIKey is the AcoustID client key, sealed before it is stored
// (ADR-14: a credential is never written in the clear, even into settings.db).
// Advanced tier: identification works without it, just with one fewer rung.
const KeyAcoustIDAPIKey = "metadata.acoustid.api_key"

// AcoustIDConfigured reports whether a key has been stored, without opening it.
// The interface needs to say "disabled: no key" without handling the key
// itself, and a screen asking for one should not decrypt it to render.
func (s *Snapshot) AcoustIDConfigured() bool { return s.policy[KeyAcoustIDAPIKey] != "" }

// AcoustIDAPIKey opens the stored key. Empty when none is set, which is the
// disabled state rather than an error (D12). No context: the sealed value is
// already in the in-memory snapshot, so this reads nothing and blocks on
// nothing.
func (c *Cache) AcoustIDAPIKey() (secrets.Secret, error) {
	sealed := c.Snapshot().policy[KeyAcoustIDAPIKey]
	if sealed == "" {
		return "", nil
	}
	if c.cipher == nil {
		return "", ErrSecretsUnavailable
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("settings: decode acoustid api key: %w", err)
	}
	opened, err := c.cipher.Open(raw)
	if err != nil {
		return "", fmt.Errorf("settings: open acoustid api key: %w", err)
	}
	return opened, nil
}

// SetAcoustIDAPIKey stores the key sealed, or clears it when given an empty
// one. Base64 because instance_setting holds text; the bytes themselves are
// the cipher's, never the key.
func (c *Cache) SetAcoustIDAPIKey(ctx context.Context, cl clock.Clock, key secrets.Secret) error {
	if key == "" {
		return c.setPolicy(ctx, cl, KeyAcoustIDAPIKey, "")
	}
	if c.cipher == nil {
		return ErrSecretsUnavailable
	}
	stored, err := sealedForStorage(c.cipher, key)
	if err != nil {
		return err
	}
	return c.setPolicy(ctx, cl, KeyAcoustIDAPIKey, stored)
}

// sealedForStorage is what actually lands in instance_setting: the cipher's
// bytes, base64'd because the column holds text. Named rather than inlined so
// "the key is never stored in the clear" is a property something can assert.
func sealedForStorage(cipher *secrets.Cipher, key secrets.Secret) (string, error) {
	sealed, err := cipher.Seal(key)
	if err != nil {
		return "", fmt.Errorf("settings: seal acoustid api key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}
