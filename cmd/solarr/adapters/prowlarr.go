package adapters

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Prowlarr satisfies apiserver.ProwlarrLister and apiserver.ArrConnections: a Prowlarr's indexers, and a
// connection's download clients, each marked when already added here.
type Prowlarr struct {
	Cache  *settings.Cache
	List   func(ctx context.Context, baseURL, apiKey string) ([]indexer.ProwlarrIndexer, error)
	Lidarr LidarrClientFactory
	Clock  clock.Clock
}

func (a Prowlarr) ListProwlarr(ctx context.Context, baseURL, apiKey string) ([]apiserver.ProwlarrIndexerRow, error) {
	listed, err := a.List(ctx, baseURL, apiKey)
	if err != nil {
		return nil, err
	}
	configured, err := a.Cache.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	added := make(map[string]bool, len(configured))
	for _, existing := range configured {
		added[existing.BaseURL] = true
	}
	rows := make([]apiserver.ProwlarrIndexerRow, len(listed))
	for i, entry := range listed {
		rows[i] = apiserver.ProwlarrIndexerRow{
			ProwlarrID: entry.ProwlarrID, Name: entry.Name, Impl: entry.Impl, BaseURL: entry.BaseURL,
			Enabled: entry.Enabled, Music: entry.Music, AlreadyAdded: added[entry.BaseURL],
		}
	}
	return rows, nil
}

// connection is an instance and its revealed key, refused for an app it cannot serve.
func (a Prowlarr) connection(ctx context.Context, instanceID int64, apps ...settings.ArrApp) (settings.ArrInstance, string, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return settings.ArrInstance{}, "", err
	}
	allowed := false
	for _, app := range apps {
		allowed = allowed || instance.App == app
	}
	if !allowed {
		return settings.ArrInstance{}, "", fmt.Errorf("%w: %s", apiserver.ErrArrAppNotYetSupported, instance.App)
	}
	key, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return settings.ArrInstance{}, "", err
	}
	return instance, key.Reveal(), nil
}

// InstanceIndexers is ListProwlarr over a stored Prowlarr connection.
func (a Prowlarr) InstanceIndexers(ctx context.Context, instanceID int64) ([]apiserver.ProwlarrIndexerRow, error) {
	instance, key, err := a.connection(ctx, instanceID, settings.ArrAppProwlarr)
	if err != nil {
		return nil, err
	}
	return a.ListProwlarr(ctx, instance.BaseURL, key)
}

// UseInstanceIndexers adds the named Prowlarr indexers with the connection's key; one already here is skipped.
func (a Prowlarr) UseInstanceIndexers(ctx context.Context, instanceID int64, prowlarrIDs []int64) (int64, error) {
	instance, key, err := a.connection(ctx, instanceID, settings.ArrAppProwlarr)
	if err != nil {
		return 0, err
	}
	rows, err := a.ListProwlarr(ctx, instance.BaseURL, key)
	if err != nil {
		return 0, err
	}
	secret, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return 0, err
	}
	wanted := make(map[int64]bool, len(prowlarrIDs))
	for _, id := range prowlarrIDs {
		wanted[id] = true
	}
	var added int64
	for _, row := range rows {
		if !wanted[row.ProwlarrID] || row.AlreadyAdded {
			continue
		}
		if _, err := a.Cache.CreateIndexer(ctx, settings.IndexerRequest{
			Name: row.Name, Impl: row.Impl, BaseURL: row.BaseURL, Enabled: true,
		}, secret, a.Clock.Now().Unix()); err != nil {
			return added, fmt.Errorf("add %s: %w", row.Name, err)
		}
		added++
	}
	return added, nil
}

// InstanceDownloadClients reads one *Arr's download clients.
func (a Prowlarr) InstanceDownloadClients(ctx context.Context, instanceID int64) ([]apiserver.ArrDownloadClientRow, error) {
	instance, key, err := a.connection(ctx, instanceID,
		settings.ArrAppProwlarr, settings.ArrAppLidarr, settings.ArrAppSonarr, settings.ArrAppRadarr, settings.ArrAppWhisparr)
	if err != nil {
		return nil, err
	}
	clients, err := a.downloadClients(ctx, instance, key)
	if err != nil {
		return nil, err
	}
	configured, err := a.Cache.Downloaders(ctx)
	if err != nil {
		return nil, err
	}
	here := make(map[string]bool, len(configured))
	for _, existing := range configured {
		here[strings.TrimRight(existing.BaseURL, "/")] = true
	}
	rows := make([]apiserver.ArrDownloadClientRow, len(clients))
	for i, client := range clients {
		impl := strings.ToLower(client.Implementation)
		rows[i] = apiserver.ArrDownloadClientRow{
			Name: client.Name, Impl: impl, Protocol: client.Protocol, BaseURL: client.BaseURL(), Category: client.Category,
			Username: client.Username,
			Enabled:  client.Enabled, Supported: supported(impl), AlreadyAdded: here[client.BaseURL()],
		}
	}
	return rows, nil
}

func supported(impl string) bool {
	_, known := DownloadClientDefinition(impl)
	return known
}

// downloadClients reads an instance's download clients through its own app's API version.
func (a Prowlarr) downloadClients(ctx context.Context, instance settings.ArrInstance, key string) ([]arrimport.DownloadClient, error) {
	client := a.Lidarr(instance.BaseURL, key)
	switch instance.App {
	case settings.ArrAppSonarr:
		return client.Sonarr().DownloadClients(ctx)
	case settings.ArrAppRadarr:
		return client.Radarr().DownloadClients(ctx)
	case settings.ArrAppWhisparr:
		return client.Whisparr().DownloadClients(ctx)
	default:
		return client.DownloadClients(ctx)
	}
}

// SharedCategories answers, for the client at baseURL, each category a connected *Arr also sends under, with the
// *Arr's name: that *Arr imports whatever lands there. An *Arr that cannot be read is skipped.
func (a Prowlarr) SharedCategories(ctx context.Context, baseURL string) map[string]string {
	instances, err := a.Cache.ArrInstances(ctx)
	if err != nil {
		return nil
	}
	shared := map[string]string{}
	for _, instance := range instances {
		if !instance.Enabled || instance.App == settings.ArrAppProwlarr {
			continue
		}
		key, err := a.Cache.DecryptedAPIKey(ctx, instance.ID)
		if err != nil {
			continue
		}
		clients, err := a.downloadClients(ctx, instance, key.Reveal())
		if err != nil {
			continue
		}
		for _, client := range clients {
			if client.Category != "" && SameClientAddress(client.BaseURL(), baseURL) {
				shared[strings.ToLower(client.Category)] = instance.Name
			}
		}
	}
	return shared
}

// SameClientAddress compares two client URLs by host and port, ignoring scheme, case and URL base.
func SameClientAddress(a, b string) bool {
	hostPort := func(raw string) string {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return ""
		}
		return strings.ToLower(parsed.Host)
	}
	left := hostPort(a)
	return left != "" && left == hostPort(b)
}
