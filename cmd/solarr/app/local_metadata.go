package app

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// localClientLife is how long a connection's client, and the id index it holds, is kept before the server is read afresh.
const localClientLife = 10 * time.Minute

type localClient struct {
	catalog   mediaserver.IDCatalog
	updatedAt int64
	built     time.Time
}

// localAsker answers a library's metadata questions from the media servers it is mapped to as a source.
type localAsker struct {
	p     *process
	clock clock.Clock
	mu    sync.Mutex
	kept  map[int64]localClient
}

func newLocalAsker(p *process) *localAsker {
	return &localAsker{p: p, clock: clock.NewSystem(), kept: map[int64]localClient{}}
}

// Ask implements mediaserver.LocalAsker.
func (a *localAsker) Ask(ctx context.Context, categoryID int64, kind, mediaType string, ids map[string]string) (mediaserver.Item, bool, error) {
	servers, err := a.p.cache.MediaServers(ctx)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	mapped, err := a.p.cache.AllMediaServerLibraries(ctx)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	names := make([]string, 0, len(ids))
	for name, id := range ids {
		if id != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, server := range servers {
		if !server.Enabled || !server.AsMetadata || server.Kind != kind || !slices.ContainsFunc(mapped[server.ID], func(l settings.MediaServerLibrary) bool { return l.CategoryID == categoryID }) {
			continue
		}
		catalog, err := a.catalogOf(ctx, server)
		if err != nil {
			return mediaserver.Item{}, false, err
		}
		for _, name := range names {
			item, found, err := catalog.ItemByProviderID(ctx, mediaType, name, ids[name])
			if err != nil {
				a.forget(server.ID)
				return mediaserver.Item{}, false, err
			}
			if found {
				return item, true, nil
			}
		}
	}
	return mediaserver.Item{}, false, nil
}

// AskByPath implements mediaserver.LocalAsker.
func (a *localAsker) AskByPath(ctx context.Context, categoryID int64, solarrPath string) (mediaserver.Item, bool, error) {
	servers, err := a.p.cache.MediaServers(ctx)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	mapped, err := a.p.cache.AllMediaServerLibraries(ctx)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	for _, server := range servers {
		if !server.Enabled || !server.AsMetadata || !slices.ContainsFunc(mapped[server.ID], func(l settings.MediaServerLibrary) bool { return l.CategoryID == categoryID }) {
			continue
		}
		catalog, err := a.catalogOf(ctx, server)
		if err != nil {
			return mediaserver.Item{}, false, err
		}
		byPath, ok := catalog.(mediaserver.Catalog)
		if !ok {
			continue
		}
		pairs, err := a.p.cache.MediaServerPaths(ctx, server.ID)
		if err != nil {
			return mediaserver.Item{}, false, err
		}
		prefixes := make([]mediaserver.PathPair, len(pairs))
		for index, pair := range pairs {
			prefixes[index] = mediaserver.PathPair{Solarr: pair.SolarrPrefix, Server: pair.ServerPrefix}
		}
		serverPath, _ := mediaserver.MapPath(prefixes, solarrPath)
		item, found, err := byPath.ItemByPath(ctx, serverPath)
		if err != nil {
			a.forget(server.ID)
			return mediaserver.Item{}, false, err
		}
		if found {
			return item, true, nil
		}
	}
	return mediaserver.Item{}, false, nil
}

func (a *localAsker) catalogOf(ctx context.Context, server settings.MediaServer) (mediaserver.IDCatalog, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock.Now()
	if kept, ok := a.kept[server.ID]; ok && kept.updatedAt == server.UpdatedAt && now.Sub(kept.built) < localClientLife {
		return kept.catalog, nil
	}
	credential, err := a.p.cache.DecryptedMediaServerCredential(ctx, server.ID)
	if err != nil {
		return nil, err
	}
	client, err := a.p.mediaServerClients(server.Kind, server.BaseURL, server.Username, credential)
	if err != nil {
		return nil, err
	}
	catalog, ok := client.(mediaserver.IDCatalog)
	if !ok {
		return nil, mediaserver.ErrItemNotFound
	}
	a.kept[server.ID] = localClient{catalog: catalog, updatedAt: server.UpdatedAt, built: now}
	return catalog, nil
}

func (a *localAsker) forget(serverID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.kept, serverID)
}

// localMetadata is the process's one asker, so the id indexes of the servers are shared by every type.
func (p *process) localMetadata() *localAsker {
	p.localOnce.Do(func() { p.localShared = newLocalAsker(p) })
	return p.localShared
}
