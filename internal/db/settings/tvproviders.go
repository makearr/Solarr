package settings

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// KeyTMDBAPIKey is the user's own TMDB credential, a v4 read access token or a v3 API key, sealed like the
// AcoustID key; TMDB has no public key, so TV works without it on less data.
const KeyTMDBAPIKey = "metadata.tmdb.api_key"

// KeyTVDBAPIKey is an own TheTVDB v4 project key, sealed; without one the built-in project key is used.
const KeyTVDBAPIKey = "metadata.tvdb.api_key"

// TMDBConfigured reports whether a TMDB credential has been stored, without opening it.
func (s *Snapshot) TMDBConfigured() bool { return s.policy[KeyTMDBAPIKey] != "" }

// TVDBConfigured reports whether an own TheTVDB key has been stored, without opening it.
func (s *Snapshot) TVDBConfigured() bool { return s.policy[KeyTVDBAPIKey] != "" }

// TMDBAPIKey opens the credential; empty means none is stored.
func (c *Cache) TMDBAPIKey() (secrets.Secret, error) { return c.openSealedPolicy(KeyTMDBAPIKey) }

// TVDBAPIKey opens the own key; empty means the built-in project key is used.
func (c *Cache) TVDBAPIKey() (secrets.Secret, error) { return c.openSealedPolicy(KeyTVDBAPIKey) }

// SetTMDBAPIKey stores the credential sealed, or clears it.
func (c *Cache) SetTMDBAPIKey(ctx context.Context, cl clock.Clock, key secrets.Secret) error {
	return c.setSealedPolicy(ctx, cl, KeyTMDBAPIKey, key)
}

// SetTVDBAPIKey stores the own key sealed, or clears it, going back to the built-in project key.
func (c *Cache) SetTVDBAPIKey(ctx context.Context, cl clock.Clock, key secrets.Secret) error {
	return c.setSealedPolicy(ctx, cl, KeyTVDBAPIKey, key)
}
