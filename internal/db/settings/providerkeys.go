package settings

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// The owner's keys for the movie providers that need one, sealed like the others.
const (
	KeyMDBListAPIKey = "metadata.mdblist.api_key"
	KeyOMDbAPIKey    = "metadata.omdb.api_key"
	KeyFanartAPIKey  = "metadata.fanart.api_key"
	// KeyGoogleBooksAPIKey is optional: Google Books answers without one, on a small quota shared by everyone.
	KeyGoogleBooksAPIKey = "metadata.googlebooks.api_key"
	// KeyHardcoverAPIKey is the owner's Hardcover API token (hardcover.app/account/api).
	KeyHardcoverAPIKey = "metadata.hardcover.api_key"
	// The owner's keys for the stash-box servers, from the account page of each.
	KeyStashDBAPIKey   = "metadata.stashdb.api_key"
	KeyThePornDBAPIKey = "metadata.theporndb.api_key"
	KeyFansDBAPIKey    = "metadata.fansdb.api_key"
)

// providerKeySettings maps a provider to the setting its key is sealed under.
var providerKeySettings = map[string]string{
	MovieSourceMDBList: KeyMDBListAPIKey, MovieSourceOMDb: KeyOMDbAPIKey, MovieSourceFanart: KeyFanartAPIKey,
	BookSourceGoogleBooks: KeyGoogleBooksAPIKey, BookSourceHardcover: KeyHardcoverAPIKey,
	AdultSourceStashDB: KeyStashDBAPIKey, AdultSourceThePornDB: KeyThePornDBAPIKey, AdultSourceFansDB: KeyFansDBAPIKey,
	TVSourceTMDB: KeyTMDBAPIKey, TVSourceTVDB: KeyTVDBAPIKey, SourceTheAudioDB: KeyTheAudioDBAPIKey, SourceAcoustID: KeyAcoustIDAPIKey,
}

// ProviderKeySetting answers the setting a provider's key is sealed under, false for a provider without one.
func ProviderKeySetting(provider string) (string, bool) {
	setting, has := providerKeySettings[provider]
	return setting, has
}

// ProviderKeyStored reports whether the owner's key for a provider is stored, without opening it.
func (s *Snapshot) ProviderKeyStored(provider string) bool {
	setting, has := providerKeySettings[provider]
	return has && s.policy[setting] != ""
}

// ProviderKey opens the owner's key for a provider; empty when none is stored.
func (c *Cache) ProviderKey(provider string) (secrets.Secret, error) {
	setting, has := providerKeySettings[provider]
	if !has {
		return "", fmt.Errorf("settings: %s takes no key", provider)
	}
	return c.openSealedPolicy(setting)
}

// SetProviderKey stores the owner's key for a provider sealed, or clears it.
func (c *Cache) SetProviderKey(ctx context.Context, cl clock.Clock, provider string, key secrets.Secret) error {
	setting, has := providerKeySettings[provider]
	if !has {
		return fmt.Errorf("settings: %s takes no key", provider)
	}
	return c.setSealedPolicy(ctx, cl, setting, key)
}
