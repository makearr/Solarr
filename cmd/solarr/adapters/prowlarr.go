package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Prowlarr satisfies apiserver.ProwlarrLister and apiserver.ArrConnections: a Prowlarr's indexers, and a
// connection's download clients, each marked when already added here.
type Prowlarr struct {
	Cache  *settings.Cache
	List   func(ctx context.Context, baseURL, apiKey string) ([]indexer.ProwlarrIndexer, error)
	Lidarr LidarrClientFactory
	// Proxies reads a Prowlarr's indexer proxies; nil syncs none.
	Proxies func(ctx context.Context, baseURL, apiKey string) ([]indexer.ProwlarrProxy, error)
	Clock   clock.Clock
	Logger  *slog.Logger
}

// Run syncs every enabled Prowlarr connection whose sync is automatic; one failing does not stop the others.
func (a Prowlarr) Run(ctx context.Context, _ jobs.Job) error {
	instances, err := a.Cache.ArrInstances(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, instance := range instances {
		if instance.App != settings.ArrAppProwlarr || !instance.Enabled || !instance.Sync().Automatic {
			continue
		}
		_, err := a.syncIndexers(ctx, instance.ID)
		message := ""
		if err != nil {
			message = err.Error()
			failures = append(failures, fmt.Errorf("%s: %w", instance.Name, err))
		}
		if recorded := a.Cache.SetArrInstanceSyncResult(ctx, instance.ID, a.Clock.Now().Unix(), message); recorded != nil && a.Logger != nil {
			a.Logger.Warn("record prowlarr sync", slog.Int64("instance_id", instance.ID), slog.Any("error", recorded))
		}
	}
	return errors.Join(failures...)
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

// SyncResult counts what one Prowlarr sync did.
type SyncResult struct {
	Added, Updated, Paused, Resumed, Skipped int64
	// ClientsAdded were created from Prowlarr's download clients; ClientsWaiting need a secret Prowlarr will not give.
	ClientsAdded, ClientsWaiting int64
}

// SyncIndexers keeps the indexers the options choose in step with the Prowlarr connection: new ones are added, the
// address, name and key follow Prowlarr, and a pause changes here only when it changed there since the last sync.
func (a Prowlarr) SyncIndexers(ctx context.Context, instanceID int64) (apiserver.ProwlarrSyncResult, error) {
	result, err := a.syncIndexers(ctx, instanceID)
	return apiserver.ProwlarrSyncResult(result), err
}

func (a Prowlarr) syncIndexers(ctx context.Context, instanceID int64) (SyncResult, error) {
	instance, key, err := a.connection(ctx, instanceID, settings.ArrAppProwlarr)
	if err != nil {
		return SyncResult{}, err
	}
	options := instance.Sync()
	listed, err := a.List(ctx, instance.BaseURL, key)
	if err != nil {
		return SyncResult{}, err
	}
	configured, err := a.Cache.Indexers(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	byBase, bySource := map[string]settings.Indexer{}, map[int64]settings.Indexer{}
	names := map[string]bool{}
	for _, existing := range configured {
		names[existing.Name] = true
		byBase[existing.BaseURL] = existing
		if source := existing.Source(); source != nil && source.ArrID == instanceID {
			bySource[source.ExternalID] = existing
		}
	}
	secret, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return SyncResult{}, err
	}
	now := a.Clock.Now().Unix()
	var result SyncResult
	var failures []error
	proxyOf, err := a.syncProxies(ctx, instance, key, options, now)
	if err != nil {
		return result, err
	}
	if options.DownloadClients && a.Lidarr != nil {
		result.ClientsAdded, result.ClientsWaiting, err = a.syncDownloadClients(ctx, instance, key, now)
		if err != nil {
			return result, err
		}
	}
	for _, entry := range listed {
		if (entry.Enabled && !options.ActiveIndexers) || (!entry.Enabled && !options.PausedIndexers) ||
			(options.SupportedOnly && !entry.Supported) {
			result.Skipped++
			continue
		}
		source := &settings.IndexerSource{ArrID: instanceID, ExternalID: entry.ProwlarrID, UpstreamEnabled: entry.Enabled}
		existing, found := bySource[entry.ProwlarrID]
		if !found {
			existing, found = byBase[entry.BaseURL]
		}
		if !found {
			// Two Prowlarrs often serve the same site; the second is told apart by its connection's name.
			name := entry.Name
			if names[name] {
				name = entry.Name + " (" + instance.Name + ")"
			}
			names[name] = true
			if _, err := a.Cache.CreateIndexer(ctx, settings.IndexerRequest{
				Name: name, Impl: entry.Impl, BaseURL: entry.BaseURL, Enabled: entry.Enabled, Priority: entry.Priority,
				Source: source, ProxyID: proxyOf(entry), QueriesPerHour: entry.QueriesPerHour,
			}, secret, now); err != nil {
				failures = append(failures, fmt.Errorf("add %s: %w", entry.Name, err))
				continue
			}
			result.Added++
			continue
		}
		enabled := existing.Enabled
		if previous := existing.Source(); previous == nil || previous.UpstreamEnabled != entry.Enabled {
			enabled = entry.Enabled
			if enabled != existing.Enabled && enabled {
				result.Resumed++
			} else if enabled != existing.Enabled {
				result.Paused++
			}
		}
		chosen := existing.ProxyID()
		if chosen == nil || a.followsProxy(ctx, *chosen, instanceID) {
			chosen = proxyOf(entry)
		}
		// Prowlarr's stated limit applies; without one the limit set here stays.
		queries := existing.QueriesPerHour()
		if entry.QueriesPerHour > 0 {
			queries = entry.QueriesPerHour
		}
		request := settings.IndexerRequest{
			Name: existing.Name, Impl: entry.Impl, BaseURL: entry.BaseURL, Enabled: enabled, Priority: existing.Priority,
			EgressProxyURL: existing.Egress().ProxyURL, MusicCategories: existing.StoredMusicCategories(),
			Definition: existing.Definition(), Source: source, ProxyID: chosen, DownloadClientID: existing.DownloadClientID(), QueriesPerHour: queries,
		}
		if _, err := a.Cache.UpdateIndexer(ctx, existing.ID, request, now); err != nil {
			failures = append(failures, fmt.Errorf("update %s: %w", entry.Name, err))
			continue
		}
		if held, err := a.Cache.DecryptedIndexerAPIKey(ctx, existing.ID); err != nil || held.Reveal() != secret.Reveal() {
			if err := a.Cache.SetIndexerAPIKey(ctx, existing.ID, secret, now); err != nil {
				failures = append(failures, fmt.Errorf("key of %s: %w", entry.Name, err))
				continue
			}
		}
		result.Updated++
	}
	return result, errors.Join(failures...)
}

// followsProxy reports whether the proxy is one this Prowlarr connection's sync made, so the sync may change it.
func (a Prowlarr) followsProxy(ctx context.Context, proxyID, instanceID int64) bool {
	proxy, err := a.Cache.GetProxy(ctx, proxyID)
	return err != nil || proxy.SourceArrID == instanceID
}

// syncProxies makes the Prowlarr's proxies entries of the proxy list and answers which one an indexer gets: the first
// that shares a tag with it, a FlareSolverr before the others. With the option off or no reader it answers none.
func (a Prowlarr) syncProxies(ctx context.Context, instance settings.ArrInstance, key string, options settings.ProwlarrSync, now int64) (func(indexer.ProwlarrIndexer) *int64, error) {
	none := func(indexer.ProwlarrIndexer) *int64 { return nil }
	if !options.Proxies || a.Proxies == nil {
		return none, nil
	}
	listed, err := a.Proxies(ctx, instance.BaseURL, key)
	if err != nil {
		return none, err
	}
	held, err := a.Cache.Proxies(ctx)
	if err != nil {
		return none, err
	}
	byExternal := map[int64]settings.Proxy{}
	for _, one := range held {
		if one.SourceArrID == instance.ID {
			byExternal[one.SourceExternalID] = one
		}
	}
	type tagged struct {
		id   int64
		kind string
		tags []int64
	}
	var usable []tagged
	for _, one := range listed {
		request := settings.ProxyRequest{
			Name: instance.Name + ": " + one.Name, Kind: settings.ProxyKind(one.Kind), URL: reachableFrom(instance.BaseURL, one.URL),
			SourceArrID: instance.ID, SourceExternalID: one.ProwlarrID,
		}
		existing, found := byExternal[one.ProwlarrID]
		if found {
			// An address changed here stays: only Prowlarr's name and kind follow.
			request.URL = existing.URL
			if _, err = a.Cache.UpdateProxy(ctx, existing.ID, request, now); err != nil {
				return none, fmt.Errorf("proxy %s: %w", one.Name, err)
			}
		} else if existing, err = a.Cache.CreateProxy(ctx, request, now); err != nil {
			return none, fmt.Errorf("proxy %s: %w", one.Name, err)
		}
		usable = append(usable, tagged{id: existing.ID, kind: one.Kind, tags: one.Tags})
	}
	return func(entry indexer.ProwlarrIndexer) *int64 {
		var chosen *tagged
		for index, candidate := range usable {
			shares := false
			for _, tag := range candidate.tags {
				shares = shares || slices.Contains(entry.Tags, tag)
			}
			if shares && (chosen == nil || (candidate.kind == "flaresolverr" && chosen.kind != "flaresolverr")) {
				chosen = &usable[index]
			}
		}
		if chosen == nil {
			return nil
		}
		return &chosen.id
	}, nil
}

// syncDownloadClients adds Prowlarr's active download clients that need no secret: a client with an address, signed in
// without a user name, and not one that asks for an API key or password. The rest are counted as waiting; the connection's
// "Use its download clients" asks for their secret once.
func (a Prowlarr) syncDownloadClients(ctx context.Context, instance settings.ArrInstance, key string, now int64) (added, waiting int64, err error) {
	clients, err := a.downloadClients(ctx, instance, key)
	if err != nil {
		return 0, 0, err
	}
	held, err := a.Cache.Downloaders(ctx)
	if err != nil {
		return 0, 0, err
	}
	here := map[string]bool{}
	for _, existing := range held {
		here[strings.TrimRight(existing.BaseURL, "/")] = true
	}
	for _, client := range clients {
		kind, known := DownloadClientDefinition(strings.ToLower(client.Implementation))
		if !client.Enabled || !known || !kind.HasAddress() || here[client.BaseURL()] {
			continue
		}
		if client.Username != "" || (kind.SecretLabel != "" && !kind.UserName) {
			waiting++
			continue
		}
		options := settings.DownloaderOptions{Category: client.Category}
		if client.Priority > 0 {
			priority := min(client.Priority, 50)
			options.Priority = &priority
		}
		encoded, err := json.Marshal(options)
		if err != nil {
			return added, waiting, err
		}
		if _, err := a.Cache.CreateDownloader(ctx, settings.DownloaderRequest{
			Name: client.Name, Impl: strings.ToLower(client.Implementation), BaseURL: client.BaseURL(), Enabled: true,
			Protocol: string(kind.Protocol), SettingsJSON: string(encoded),
		}, "", now); err != nil {
			return added, waiting, fmt.Errorf("add download client %s: %w", client.Name, err)
		}
		here[client.BaseURL()] = true
		added++
	}
	return added, waiting, nil
}

// reachableFrom turns an address on Prowlarr's own host ("localhost") into that host as Solarr reaches it, the host of
// the Prowlarr connection; any other address is kept.
func reachableFrom(prowlarrURL, address string) string {
	target, err := url.Parse(address)
	if err != nil {
		return address
	}
	switch target.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return address
	}
	base, err := url.Parse(prowlarrURL)
	if err != nil || base.Hostname() == "" {
		return address
	}
	if port := target.Port(); port != "" {
		target.Host = net.JoinHostPort(base.Hostname(), port)
	} else {
		target.Host = base.Hostname()
	}
	return target.String()
}
