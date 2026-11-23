package app

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/titlekey"
	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/remoteimage"
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
func (a *localAsker) Ask(ctx context.Context, categoryID int64, kind, mediaType string, question mediaserver.Question) (mediaserver.Item, bool, error) {
	servers, err := a.p.cache.MediaServers(ctx)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	mapped, err := a.p.cache.AllMediaServerLibraries(ctx)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	ids := question.IDs
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
		if item, found := a.byTitle(ctx, catalog, mediaType, question); found {
			return item, true, nil
		}
	}
	return mediaserver.Item{}, false, nil
}

// byTitle finds an item whose title is the question's exactly (case, accents and punctuation aside), and whose author agrees when both name one.
func (a *localAsker) byTitle(ctx context.Context, catalog mediaserver.IDCatalog, mediaType string, question mediaserver.Question) (mediaserver.Item, bool) {
	searcher, ok := catalog.(mediaserver.Catalog)
	if !ok || question.Title == "" {
		return mediaserver.Item{}, false
	}
	found, err := searcher.Search(ctx, mediaType, question.Title, 10)
	if err != nil {
		return mediaserver.Item{}, false
	}
	for _, item := range found {
		if titlekey.Fold(item.Title, titlekey.Compact) != titlekey.Fold(question.Title, titlekey.Compact) {
			continue
		}
		authors := authorsOf(item)
		if question.Author == "" || len(authors) == 0 || slices.ContainsFunc(authors, func(name string) bool {
			return titlekey.Fold(name, titlekey.Compact) == titlekey.Fold(question.Author, titlekey.Compact)
		}) {
			return item, true
		}
	}
	return mediaserver.Item{}, false
}

func authorsOf(item mediaserver.Item) []string {
	var names []string
	for _, person := range item.People {
		if person.Kind == "author" {
			names = append(names, person.Name)
		}
	}
	return names
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

// localImages serves the pictures of the media servers used as a metadata source, with each connection's own credentials.
type localImages struct{ p *process }

var _ remoteimage.Local = localImages{}

// fetcherFor finds the enabled connection whose picture address this is.
func (l localImages) fetcherFor(ctx context.Context, address string) (mediaserver.ImageFetcher, bool) {
	servers, err := l.p.cache.MediaServers(ctx)
	if err != nil {
		return nil, false
	}
	for _, server := range servers {
		if !server.Enabled || !server.AsMetadata || !strings.HasPrefix(address, strings.TrimRight(server.BaseURL, "/")+"/") {
			continue
		}
		catalog, err := l.p.localMetadata().catalogOf(ctx, server)
		if err != nil {
			continue
		}
		if fetcher, ok := catalog.(mediaserver.ImageFetcher); ok && fetcher.OwnsImage(address) {
			return fetcher, true
		}
	}
	return nil, false
}

// Owns implements remoteimage.Local.
func (l localImages) Owns(ctx context.Context, address string) bool {
	_, found := l.fetcherFor(ctx, address)
	return found
}

// Fetch implements remoteimage.Local.
func (l localImages) Fetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	fetcher, found := l.fetcherFor(ctx, address)
	if !found {
		return nil, mediaserver.ErrItemNotFound
	}
	return fetcher.FetchImage(ctx, address, limit)
}
