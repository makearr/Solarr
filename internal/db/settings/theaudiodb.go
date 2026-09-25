package settings

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// KeyTheAudioDBAPIKey is a supporter's own TheAudioDB key, sealed like the AcoustID key. Without
// one the public key is used (answered 2026-09-25).
const KeyTheAudioDBAPIKey = "metadata.theaudiodb.api_key"

// TheAudioDBConfigured reports whether an own key has been stored, without opening it.
func (s *Snapshot) TheAudioDBConfigured() bool { return s.policy[KeyTheAudioDBAPIKey] != "" }

// TheAudioDBAPIKey opens the own key; empty means the public one is used.
func (c *Cache) TheAudioDBAPIKey() (secrets.Secret, error) {
	return c.openSealedPolicy(KeyTheAudioDBAPIKey)
}

// SetTheAudioDBAPIKey stores the own key sealed, or clears it, going back to the public key.
func (c *Cache) SetTheAudioDBAPIKey(ctx context.Context, cl clock.Clock, key secrets.Secret) error {
	return c.setSealedPolicy(ctx, cl, KeyTheAudioDBAPIKey, key)
}
