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
// What needs the person's attention afterwards comes back as notes, apart from what failed.
func (a ArrSync) importSetup(
	ctx context.Context, instance settings.ArrInstance, client arrimport.LidarrClient, options apiserver.ArrSyncOptions,
) ([]string, error) {
	if (!options.Indexers && !options.DownloadClients) || a.Setup.Cache == nil || instance.App.ComicManager() {
		return nil, nil
	}
	var notes []string
	var failures []error
	if options.Indexers {
		found, err := a.Setup.indexers(ctx, instance.App, client)
		notes, failures = append(notes, found...), append(failures, err)
	}
	if options.DownloadClients {
		found, err := a.Setup.downloadClients(ctx, instance.App, client)
		notes, failures = append(notes, found...), append(failures, err)
	}
	return notes, errors.Join(failures...)
}

// indexers adds the Newznab and Torznab indexers of the *Arr, enabled as it has them; Prowlarr's own are used through
// its connection, and an indexer of another kind has no address Solarr can search.
func (s ArrSetup) indexers(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) ([]string, error) {
	found, err := arrIndexers(ctx, app, client)
	if err != nil {
		return nil, fmt.Errorf("indexers: %w", err)
	}
	have, err := s.Cache.Indexers(ctx)
	if err != nil {
		return nil, fmt.Errorf("indexers: %w", err)
	}
	known := map[string]bool{}
	for _, existing := range have {
		known[strings.TrimRight(existing.BaseURL, "/")] = true
	}
	prowlarr := s.prowlarrKeys(ctx)
	var failures []error
	var keyless []string
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
				keyless = append(keyless, entry.Name)
			}
		}
		if app == settings.ArrAppLidarr && len(entry.Categories) > 0 {
			request.MusicCategories = entry.Categories[:min(len(entry.Categories), maxIndexerCategories)]
		}
		if _, err := s.Cache.CreateIndexer(ctx, request, secrets.Secret(key), s.Clock.Now().Unix()); err != nil {
			failures = append(failures, fmt.Errorf("add indexer %s: %w", entry.Name, err))
		}
	}
	var notes []string
	if len(keyless) > 0 {
		notes = append(notes, "Indexers added switched off, enter their API keys: "+strings.Join(keyless, ", "))
	}
	return notes, errors.Join(failures...)
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
func (s ArrSetup) downloadClients(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) ([]string, error) {
	found, err := arrDownloadClients(ctx, app, client)
	if err != nil {
		return nil, fmt.Errorf("download clients: %w", err)
	}
	have, err := s.Cache.Downloaders(ctx)
	if err != nil {
		return nil, fmt.Errorf("download clients: %w", err)
	}
	mappings := remotePathMappingsOf(ctx, app, client)
	known := map[string]bool{}
	for _, existing := range have {
		known[strings.TrimRight(existing.BaseURL, "/")] = true
	}
	var failures []error
	var notes []string
	for _, entry := range found {
		impl := strings.ToLower(entry.Implementation)
		definition, supported := DownloadClientDefinition(impl)
		if !supported || known[entry.BaseURL()] {
			continue
		}
		request := settings.DownloaderRequest{Name: entry.Name, Impl: impl, BaseURL: entry.BaseURL(), Enabled: false, Protocol: string(definition.Protocol)}
		// Its priority and the remote path mapping of its host carry over; its category does not, since two apps sharing one category fight over the files.
		options := settings.DownloaderOptions{}
		if entry.Priority >= 1 && entry.Priority <= 50 {
			priority := entry.Priority
			options.Priority = &priority
		}
		for _, mapping := range mappings {
			if strings.EqualFold(mapping.Host, entry.Host) && mapping.RemotePath != "" && mapping.LocalPath != "" {
				options.RemotePath, options.LocalPath = mapping.RemotePath, mapping.LocalPath
				break
			}
		}
		if options != (settings.DownloaderOptions{}) {
			if encoded, err := json.Marshal(options); err == nil {
				request.SettingsJSON = string(encoded)
			}
		}
		if entry.Category != "" {
			notes = append(notes, fmt.Sprintf("%s: %s uses category %q; give Solarr its own category before enabling it", entry.Name, app, entry.Category))
		}
		if _, err := s.Cache.CreateDownloader(ctx, request, "", s.Clock.Now().Unix()); err != nil {
			failures = append(failures, fmt.Errorf("add download client %s: %w", entry.Name, err))
		}
	}
	return notes, errors.Join(failures...)
}

// remotePathMappingsOf reads the *Arr's Remote Path Mappings; unreadable ones leave the clients without a mapping, which the person sets by hand.
func remotePathMappingsOf(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) []arrimport.RemotePathMapping {
	var mappings []arrimport.RemotePathMapping
	var err error
	switch app {
	case settings.ArrAppSonarr:
		mappings, err = client.Sonarr().RemotePathMappings(ctx)
	case settings.ArrAppRadarr:
		mappings, err = client.Radarr().RemotePathMappings(ctx)
	case settings.ArrAppWhisparr:
		mappings, err = client.Whisparr().RemotePathMappings(ctx)
	default:
		mappings, err = client.RemotePathMappings(ctx)
	}
	if err != nil {
		return nil
	}
	return mappings
}

// arrIndexers reads an *Arr's indexers from the API version its app speaks.
func arrIndexers(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) ([]arrimport.Indexer, error) {
	switch app {
	case settings.ArrAppSonarr:
		return client.Sonarr().Indexers(ctx)
	case settings.ArrAppRadarr:
		return client.Radarr().Indexers(ctx)
	case settings.ArrAppWhisparr:
		return client.Whisparr().Indexers(ctx)
	}
	return client.Indexers(ctx)
}

// arrDownloadClients reads an *Arr's download clients from the API version its app speaks.
func arrDownloadClients(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) ([]arrimport.DownloadClient, error) {
	switch app {
	case settings.ArrAppSonarr:
		return client.Sonarr().DownloadClients(ctx)
	case settings.ArrAppRadarr:
		return client.Radarr().DownloadClients(ctx)
	case settings.ArrAppWhisparr:
		return client.Whisparr().DownloadClients(ctx)
	}
	return client.DownloadClients(ctx)
}

// arrImportLists reads an *Arr's import lists from the API version its app speaks.
func arrImportLists(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) ([]arrimport.ImportList, error) {
	switch app {
	case settings.ArrAppSonarr:
		return client.Sonarr().ImportLists(ctx)
	case settings.ArrAppRadarr:
		return client.Radarr().ImportLists(ctx)
	case settings.ArrAppWhisparr:
		return client.Whisparr().ImportLists(ctx)
	}
	return client.ImportLists(ctx)
}
