package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// indexerConns resolves every enabled indexer connection fresh, the same
// "ask, don't cache" discipline every settings-backed job dependency in
// this codebase already follows -- a connection added or disabled between
// runs takes effect on the very next search with no restart.
func (p *process) indexerConns(ctx context.Context) ([]music.IndexerConn, error) {
	configured, err := p.cache.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	factory := p.indexerClientFactory()

	conns := make([]music.IndexerConn, 0, len(configured))
	for _, indexer := range configured {
		if !indexer.Enabled {
			continue
		}
		apiKey, err := p.cache.DecryptedIndexerAPIKey(ctx, indexer.ID)
		if err != nil {
			return nil, err
		}
		client := factory(indexer.ID, indexer.BaseURL, apiKey.Reveal(), indexer.Egress())
		conn := music.IndexerConn{
			ID: indexer.ID, Name: indexer.Name, Impl: indexer.Impl, Client: client, Categories: indexer.MusicCategories(),
		}
		if p.indexerCaps != nil {
			conn.Params = map[string][]string{"music": p.indexerCaps.musicParams(ctx, indexer.ID, indexer.BaseURL, client)}
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

// tvIndexerConns is indexerConns for TV: the same connections, searched in TV's categories with tvsearch's parameters.
func (p *process) tvIndexerConns(ctx context.Context) ([]judge.Conn, error) {
	configured, err := p.cache.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	factory := p.indexerClientFactory()
	conns := make([]judge.Conn, 0, len(configured))
	for _, found := range configured {
		if !found.Enabled {
			continue
		}
		apiKey, err := p.cache.DecryptedIndexerAPIKey(ctx, found.ID)
		if err != nil {
			return nil, err
		}
		client := factory(found.ID, found.BaseURL, apiKey.Reveal(), found.Egress())
		conn := judge.Conn{ID: found.ID, Name: found.Name, Impl: found.Impl, Client: client, Categories: found.TVCategories()}
		if p.indexerCaps != nil {
			if params := p.indexerCaps.tvParams(ctx, found.ID, found.BaseURL, client); params != nil {
				conn.Params = map[string][]string{"tvsearch": params}
			}
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

// tvGrab builds the TV module's automatic search over the real stores.
func (p *process) tvGrab() tv.Grab {
	blocklist, sightings, filters := library.NewBlocklistStore(p.library), library.NewSightings(p.library), library.NewReleaseFilters(p.library)
	return tv.Grab{
		Search: tv.Search{
			Desired: library.NewDesired(p.library), Series: library.NewSeriesReader(p.library),
			Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(), Blocklist: &blocklist,
			Sightings: &sightings, ReleaseFilters: &filters,
		},
		Dispatch: p.dispatchFor(tv.MediaType),
	}
}

// musicSearch builds internal/media/music.Search over the real stores.
func (p *process) musicSearch() music.Search {
	blocklist, sightings := library.NewBlocklistStore(p.library), library.NewSightings(p.library)
	return music.Search{
		Works: library.NewWorks(p.library), Desired: library.NewDesired(p.library),
		Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(),
		Blocklist: &blocklist, Sightings: &sightings,
	}
}

// downloaderConnsFor resolves every enabled downloader connection fresh, the same "ask, don't cache" discipline
// indexerConns follows, with the client category a media type's downloads are sent under. A downloader serves the
// protocol its row names; one with an impl Solarr has no client for is skipped.
func (p *process) downloaderConnsFor(ctx context.Context, mediaType string) ([]music.DownloaderConn, error) {
	configured, err := p.cache.Downloaders(ctx)
	if err != nil {
		return nil, err
	}
	factory := p.downloaderClientFactory()

	conns := make([]music.DownloaderConn, 0, len(configured))
	for _, downloader := range configured {
		if !downloader.Enabled {
			continue
		}
		credentials, err := p.cache.DecryptedDownloaderCredentials(ctx, downloader.ID)
		if err != nil {
			return nil, err
		}
		client, known := adapters.NewDownloadClient(downloader.Impl, factory(downloader.ID, downloader.BaseURL), downloader.BaseURL, credentials.Reveal())
		if !known {
			continue
		}
		conns = append(conns, music.DownloaderConn{
			ID: downloader.ID, Protocol: downloader.Protocol, Category: downloader.Options().CategoryFor(mediaType),
			MaxQueued:      downloader.Options().MaxQueuedDownloads(),
			AlwaysSendUser: downloader.Options().SendsUserGrabs(),
			AlwaysSendRSS:  downloader.Options().SendsRSSGrabs(),
			Client:         client,
		})
	}
	return conns, nil
}

// musicGrab is Lidarr's automatic search: search, then send the best release.
func (p *process) musicGrab() music.Grab {
	return music.Grab{Search: p.musicSearch(), Dispatch: p.musicDispatch()}
}

// musicDispatch builds internal/media/music.Dispatch (ADR-57's own
// acquire.send) over the real stores.
func (p *process) musicDispatch() music.Dispatch { return p.dispatchFor(string(music.MediaType)) }

// dispatchFor is the dispatch of one media type: the same stores, its own client category.
func (p *process) dispatchFor(mediaType string) music.Dispatch {
	ready := library.NewReadyQueue(p.library)
	blocklist := library.NewBlocklistStore(p.library)
	return music.Dispatch{
		Candidates: statistics.NewCandidates(p.statistics), Acquisitions: library.NewAcquisitions(p.library),
		Desired:     library.NewDesired(p.library),
		Downloaders: func(ctx context.Context) ([]music.DownloaderConn, error) { return p.downloaderConnsFor(ctx, mediaType) },
		Fetch:       p.fetchRelease, History: p.recordDownloadHistory, Ready: &ready, Blocklist: &blocklist,
	}
}

// fetchRelease downloads a release's NZB or torrent through the indexer that listed it, disabled or not:
// the release was chosen while it was enabled.
func (p *process) fetchRelease(ctx context.Context, indexerID int64, link string) ([]byte, error) {
	if strings.HasPrefix(link, "magnet:") {
		return []byte(link), nil // nothing to fetch: the client takes the link itself
	}
	configured, err := p.cache.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	for _, found := range configured {
		if found.ID != indexerID {
			continue
		}
		apiKey, err := p.cache.DecryptedIndexerAPIKey(ctx, found.ID)
		if err != nil {
			return nil, err
		}
		return p.indexerClientFactory()(found.ID, found.BaseURL, apiKey.Reveal(), found.Egress()).Fetch(ctx, link)
	}
	return nil, fmt.Errorf("indexer %d is no longer configured", indexerID)
}

// registerSearchCategoryJob wires the per-category candidate search (todo
// 5.2) into the registry and the executor.
func (p *process) registerSearchCategoryJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.SearchCategoryDefinition); err != nil {
		return err
	}
	job := music.NewSearchCategoryJob(music.SearchCategoryConfig{
		Grab: p.musicGrab(), Indexers: p.indexerConns, Clock: clock.NewSystem(), Logger: p.logger,
	})
	executor.Handle(music.SearchCategoryJobType, job)
	return nil
}

// candidateSearcher builds adapters.CandidateSearch over the real stores.
func (p *process) candidateSearcher() adapters.CandidateSearch {
	return adapters.CandidateSearch{
		Works: library.NewWorks(p.library), Desired: library.NewDesired(p.library), Searching: adapters.MediaTypes{Registry: p.media}.SearchingOf,
		Search: p.musicSearch(), Dispatch: p.musicDispatch(), Queue: p.queue, Clock: clock.NewSystem(),
	}
}
