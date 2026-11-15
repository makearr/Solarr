package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// ArrSetup adds the indexers and download clients an *Arr holds, as a run of the import asks for them.
type ArrSetup struct {
	Cache *settings.Cache
	Clock clock.Clock
	// FS checks a root folder exists here before it is added.
	FS fsx.FS
}

// importSetup brings the *Arr's indexers and download clients the options name; each is added once, found by its address.
func (a ArrSync) importSetup(ctx context.Context, instance settings.ArrInstance, client arrimport.LidarrClient, options apiserver.ArrSyncOptions) error {
	if (!options.Indexers && !options.DownloadClients) || a.Setup.Cache == nil {
		return nil
	}
	var failures []error
	if options.Indexers {
		failures = append(failures, a.Setup.indexers(ctx, instance.App, client))
	}
	if options.DownloadClients {
		failures = append(failures, a.Setup.downloadClients(ctx, instance.App, client))
	}
	return errors.Join(failures...)
}

// indexers adds the Newznab and Torznab indexers of the *Arr, enabled as it has them; Prowlarr's own are used through
// its connection, and an indexer of another kind has no address Solarr can search.
func (s ArrSetup) indexers(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) error {
	var found []arrimport.Indexer
	var err error
	switch app {
	case settings.ArrAppSonarr:
		found, err = client.Sonarr().Indexers(ctx)
	case settings.ArrAppRadarr:
		found, err = client.Radarr().Indexers(ctx)
	case settings.ArrAppWhisparr:
		found, err = client.Whisparr().Indexers(ctx)
	default:
		found, err = client.Indexers(ctx)
	}
	if err != nil {
		return fmt.Errorf("indexers: %w", err)
	}
	have, err := s.Cache.Indexers(ctx)
	if err != nil {
		return fmt.Errorf("indexers: %w", err)
	}
	known := map[string]bool{}
	for _, existing := range have {
		known[strings.TrimRight(existing.BaseURL, "/")] = true
	}
	prowlarr := s.prowlarrKeys(ctx)
	var failures, keyless []error
	for _, entry := range found {
		impl := strings.ToLower(entry.Implementation)
		if (impl != "newznab" && impl != "torznab") || entry.BaseURL == "" || known[strings.TrimRight(entry.BaseURL, "/")] {
			continue
		}
		request := settings.IndexerRequest{Name: entry.Name, Impl: impl, BaseURL: entry.BaseURL, Enabled: entry.Enabled}
		key := entry.APIKey
		// The *Arr hides its keys: with none to copy the indexer is added switched off, except one that reaches Prowlarr,
		// which a connection here already holds the key of.
		if arrimport.Masked(key) || key == "" {
			if prowlarrKey, viaProwlarr := prowlarr[hostOf(entry.BaseURL)]; viaProwlarr {
				key = prowlarrKey
			} else {
				key, request.Enabled = "", false
				keyless = append(keyless, errors.New(entry.Name))
			}
		}
		if app == settings.ArrAppLidarr && len(entry.Categories) > 0 {
			request.MusicCategories = entry.Categories[:min(len(entry.Categories), maxIndexerCategories)]
		}
		if _, err := s.Cache.CreateIndexer(ctx, request, secrets.Secret(key), s.Clock.Now().Unix()); err != nil {
			failures = append(failures, fmt.Errorf("add indexer %s: %w", entry.Name, err))
		}
	}
	if len(keyless) > 0 {
		names := make([]string, len(keyless))
		for i, err := range keyless {
			names[i] = err.Error()
		}
		failures = append(failures, fmt.Errorf("indexers added switched off, enter their API keys: %s", strings.Join(names, ", ")))
	}
	return errors.Join(failures...)
}

// maxIndexerCategories bounds the categories carried over from one indexer.
const maxIndexerCategories = 32

// hostOf is the host and port of an address, lower-cased, to tell whether two addresses reach one server.
func hostOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

// prowlarrKeys are the API keys of the Prowlarr connections, by host: an indexer an *Arr reaches through Prowlarr talks
// to it with Prowlarr's key.
func (s ArrSetup) prowlarrKeys(ctx context.Context) map[string]string {
	keys := map[string]string{}
	instances, err := s.Cache.ArrInstances(ctx)
	if err != nil {
		return keys
	}
	for _, instance := range instances {
		if instance.App != settings.ArrAppProwlarr {
			continue
		}
		if key, err := s.Cache.DecryptedAPIKey(ctx, instance.ID); err == nil {
			keys[hostOf(instance.BaseURL)] = key.Reveal()
		}
	}
	return keys
}

// downloadClients adds the *Arr's download clients Solarr has a client for, found by address. An *Arr hides a client's
// password, so each arrives switched off, to be switched on once its credentials are entered.
func (s ArrSetup) downloadClients(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) error {
	var found []arrimport.DownloadClient
	var err error
	switch app {
	case settings.ArrAppSonarr:
		found, err = client.Sonarr().DownloadClients(ctx)
	case settings.ArrAppRadarr:
		found, err = client.Radarr().DownloadClients(ctx)
	case settings.ArrAppWhisparr:
		found, err = client.Whisparr().DownloadClients(ctx)
	default:
		found, err = client.DownloadClients(ctx)
	}
	if err != nil {
		return fmt.Errorf("download clients: %w", err)
	}
	have, err := s.Cache.Downloaders(ctx)
	if err != nil {
		return fmt.Errorf("download clients: %w", err)
	}
	known := map[string]bool{}
	for _, existing := range have {
		known[strings.TrimRight(existing.BaseURL, "/")] = true
	}
	var failures []error
	for _, entry := range found {
		impl := strings.ToLower(entry.Implementation)
		definition, supported := DownloadClientDefinition(impl)
		if !supported || known[entry.BaseURL()] {
			continue
		}
		request := settings.DownloaderRequest{Name: entry.Name, Impl: impl, BaseURL: entry.BaseURL(), Enabled: false, Protocol: string(definition.Protocol)}
		// Its priority carries over; its category does not, since two apps sharing one category fight over the files.
		if entry.Priority >= 1 && entry.Priority <= 50 {
			priority := entry.Priority
			if encoded, err := json.Marshal(settings.DownloaderOptions{Priority: &priority}); err == nil {
				request.SettingsJSON = string(encoded)
			}
		}
		if entry.Category != "" {
			failures = append(failures, fmt.Errorf("%s: %s uses category %q; give Solarr its own category before enabling it", entry.Name, app, entry.Category))
		}
		if _, err := s.Cache.CreateDownloader(ctx, request, "", s.Clock.Now().Unix()); err != nil {
			failures = append(failures, fmt.Errorf("add download client %s: %w", entry.Name, err))
		}
	}
	return errors.Join(failures...)
}
