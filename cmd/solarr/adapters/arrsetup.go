package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// ArrSetup adds the indexers and download clients an *Arr holds, as a run of the import asks for them.
type ArrSetup struct {
	Cache *settings.Cache
	Clock clock.Clock
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
	var failures []error
	for _, entry := range found {
		impl := strings.ToLower(entry.Implementation)
		if (impl != "newznab" && impl != "torznab") || entry.BaseURL == "" || known[strings.TrimRight(entry.BaseURL, "/")] {
			continue
		}
		request := settings.IndexerRequest{Name: entry.Name, Impl: impl, BaseURL: entry.BaseURL, Enabled: entry.Enabled}
		if _, err := s.Cache.CreateIndexer(ctx, request, secrets.Secret(entry.APIKey), s.Clock.Now().Unix()); err != nil {
			failures = append(failures, fmt.Errorf("add indexer %s: %w", entry.Name, err))
		}
	}
	return errors.Join(failures...)
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
		if _, err := s.Cache.CreateDownloader(ctx, request, "", s.Clock.Now().Unix()); err != nil {
			failures = append(failures, fmt.Errorf("add download client %s: %w", entry.Name, err))
		}
	}
	return errors.Join(failures...)
}
