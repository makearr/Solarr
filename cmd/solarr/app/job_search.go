package app

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/adult"
	"github.com/makearr/Solarr/internal/media/book"
	"github.com/makearr/Solarr/internal/media/comic"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// indexerConns resolves every enabled indexer connection fresh (ask, don't cache), so an added or disabled indexer takes effect on the next search.
func (p *process) indexerConns(ctx context.Context) ([]music.IndexerConn, error) {
	if p.solarrMode().SearchesStopped() {
		return nil, nil
	}
	configured, err := p.cache.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	conns := make([]music.IndexerConn, 0, len(configured))
	for _, found := range configured {
		if !found.Enabled {
			continue
		}
		apiKey, err := p.cache.DecryptedIndexerAPIKey(ctx, found.ID)
		if err != nil {
			// One unreadable key (an encryption key changed) must not stop the searches of every other indexer.
			p.logger.Warn("indexer skipped: its API key cannot be read", slog.Int64("indexer_id", found.ID), slog.Any("error", err))
			continue
		}
		client, known := p.indexerDriver(ctx, found, apiKey.Reveal())
		if !known {
			continue
		}
		categories := found.StoredMusicCategories()
		var unreadable bool
		if len(categories) == 0 {
			var ask bool
			if categories, ask, unreadable = p.categoriesFor(ctx, found, client, indexer.RootMusic); !ask && !unreadable {
				continue
			}
		}
		conn := music.IndexerConn{
			ID: found.ID, Name: found.Name, Impl: found.Impl, Priority: found.Priority, Client: client,
			Categories: categories, Unreadable: unreadableReason(unreadable),
		}
		if p.indexerCaps != nil {
			conn.Params = map[string][]string{"music": p.indexerCaps.musicParams(ctx, found.ID, found.BaseURL, client)}
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

// unreadableReason is what a search reports for an indexer whose capabilities could not be read; empty when they could.
func unreadableReason(unreadable bool) string {
	if !unreadable {
		return ""
	}
	return "not searched: its capabilities could not be read, so no category is known"
}

// tvIndexerConns is indexerConns for TV: the same connections, searched in TV's categories with tvsearch's parameters.
func (p *process) tvIndexerConns(ctx context.Context) ([]judge.Conn, error) {
	return p.searchConns(ctx, indexer.RootTV, nil, "tvsearch", "tv-search")
}

// movieIndexerConns is indexerConns for movies: the same connections, searched in the movie categories with the
// movie function's parameters.
func (p *process) movieIndexerConns(ctx context.Context) ([]judge.Conn, error) {
	return p.searchConns(ctx, indexer.RootMovies, nil, "movie", "movie-search")
}

// bookIndexerConns is indexerConns for a book type: the same connections, searched in its Newznab categories with the
// book function's parameters.
func (p *process) bookIndexerConns(format book.Format) func(ctx context.Context) ([]judge.Conn, error) {
	return func(ctx context.Context) ([]judge.Conn, error) {
		return p.searchConns(ctx, indexer.RootBooks, slices.Clone(format.FeedCategories), "book", "book-search")
	}
}

// comicIndexerConns is indexerConns for comics: the same connections, searched in Newznab's comic and book categories by text.
func (p *process) comicIndexerConns(ctx context.Context) ([]judge.Conn, error) {
	return p.searchConns(ctx, indexer.RootBooks, slices.Clone(comic.FeedCategories), "search", "search")
}

// adultIndexerConns is indexerConns for the adult type: the same connections, searched in Newznab's XXX categories by text.
func (p *process) adultIndexerConns(ctx context.Context) ([]judge.Conn, error) {
	return p.searchConns(ctx, indexer.RootAdult, slices.Clone(adult.FeedCategories), "search", "search")
}

// searchConns resolves every enabled indexer fresh, as indexerConns does, that lists a category of the media type rooted
// at root, searched in wanted (its own categories for the type when nil) with the parameters the indexer's caps list for
// capsSearch under function.
func (p *process) searchConns(ctx context.Context, root int, wanted []int, function, capsSearch string) ([]judge.Conn, error) {
	if p.solarrMode().SearchesStopped() {
		return nil, nil
	}
	configured, err := p.cache.Indexers(ctx)
	if err != nil {
		return nil, err
	}
	conns := make([]judge.Conn, 0, len(configured))
	for _, found := range configured {
		if !found.Enabled {
			continue
		}
		apiKey, err := p.cache.DecryptedIndexerAPIKey(ctx, found.ID)
		if err != nil {
			// One unreadable key (an encryption key changed) must not stop the searches of every other indexer.
			p.logger.Warn("indexer skipped: its API key cannot be read", slog.Int64("indexer_id", found.ID), slog.Any("error", err))
			continue
		}
		client, known := p.indexerDriver(ctx, found, apiKey.Reveal())
		if !known {
			continue
		}
		categories, ask, unreadable := p.categoriesFor(ctx, found, client, root)
		if !ask && !unreadable {
			continue
		}
		if wanted != nil {
			categories = slices.Clone(wanted)
		}
		conn := judge.Conn{ID: found.ID, Name: found.Name, Impl: found.Impl, Priority: found.Priority, Client: client, Categories: categories, Unreadable: unreadableReason(unreadable),
			Seal: func(link string) string { return indexer.SealLink(link, apiKey.Reveal()) }}
		if p.indexerCaps != nil {
			if params := p.indexerCaps.searchParams(ctx, found.ID, found.BaseURL, client, capsSearch); params != nil {
				conn.Params = map[string][]string{function: params}
			}
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

// tvGrab builds the TV module's automatic search over the real stores.
func (p *process) tvGrab() tv.Grab {
	return tv.Grab{Search: p.tvSearch(), Dispatch: p.dispatchOf(tv.MediaType)}
}

// tvSearch is the TV module's search over the real stores; what is already downloading is not asked for again.
func (p *process) tvSearch() tv.Search {
	blocklist, sightings, filters := library.NewBlocklistStore(p.library), library.NewSightings(p.library), library.NewReleaseFilters(p.library)
	return tv.Search{
		Series: library.NewSeriesReader(p.library),
		InFlight: func(ctx context.Context, itemID int64) (map[string]bool, error) {
			parts, _, err := library.NewAcquisitions(p.library).InFlightParts(ctx, itemID)
			return parts, err
		},
		Deps: works.Deps{Desired: library.NewDesired(p.library), Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(), Blocklist: &blocklist, Sightings: &sightings, ReleaseFilters: &filters},
	}
}

// dispatchOf is dispatchFor with what a media type adds: a TV season names the episodes a release holds, so several
// downloads of it may run together.
func (p *process) dispatchOf(mediaType string) music.Dispatch {
	dispatch := p.dispatchFor(mediaType)
	if mediaType == tv.MediaType {
		dispatch.PartsOf = p.tvSearch().ReleaseParts
	}
	return dispatch
}

// musicSearch builds internal/media/music.Search over the real stores.
func (p *process) musicSearch() music.Search {
	blocklist, sightings := library.NewBlocklistStore(p.library), library.NewSightings(p.library)
	return music.Search{
		Works: library.NewWorks(p.library), Desired: library.NewDesired(p.library),
		Candidates: statistics.NewCandidates(p.statistics), Settings: p.cache, Clock: clock.NewSystem(),
		Blocklist: &blocklist, Sightings: &sightings, VideoReader: library.NewSeriesReader(p.library),
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
		client, known := adapters.NewDownloadClient(downloader.Impl,
			adapters.DownloadClientDeps{HTTP: factory(downloader.ID, downloader.BaseURL), FS: p.fsys, Clock: clock.NewSystem(), Blackholes: &p.blackholes},
			adapters.DownloadConnection(downloader.BaseURL, credentials.Reveal(), downloader.Options()))
		if !known {
			continue
		}
		conns = append(conns, music.DownloaderConn{
			ID: downloader.ID, Protocol: downloader.Protocol, Category: downloader.Options().CategoryFor(mediaType),
			MaxQueued: downloader.Options().MaxQueuedDownloads(), Priority: downloader.Options().PriorityRank(),
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
		MediaType:  mediaType,
		Candidates: statistics.NewCandidates(p.statistics), Acquisitions: library.NewAcquisitions(p.library),
		Desired:     library.NewDesired(p.library),
		Downloaders: func(ctx context.Context) ([]music.DownloaderConn, error) { return p.downloaderConnsFor(ctx, mediaType) },
		BoundClient: p.boundClient,
		Fetch:       p.fetchRelease, History: p.recordDownloadHistory, Ready: &ready, Blocklist: &blocklist,
		ReadyLimit: func() int64 { return p.cache.Snapshot().ReadyQueueLimit() }, Admits: p.ratingAdmits,
		Paused: func() bool { return p.solarrMode().DownloadsPaused() }, Locks: &p.sendLocks,
	}
}

// boundClient is the download client an indexer's releases are bound to, 0 for none.
func (p *process) boundClient(ctx context.Context, indexerID int64) int64 {
	found, err := p.cache.GetIndexer(ctx, indexerID)
	if err != nil || found.DownloadClientID() == nil {
		return 0
	}
	return *found.DownloadClientID()
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
		client, known := p.indexerDriver(ctx, found, apiKey.Reveal())
		if !known {
			return nil, fmt.Errorf("indexer %d is a kind Solarr has no implementation of", indexerID)
		}
		return client.Fetch(ctx, indexer.UnsealLink(link, apiKey.Reveal()))
	}
	return nil, fmt.Errorf("indexer %d is no longer configured", indexerID)
}

// searchCategoryJob is the per-category candidate search (todo 5.2).
func (p *process) searchCategoryJob() media.JobRegistration {
	return media.JobRegistration{Definition: music.SearchCategoryDefinition, Handler: music.NewSearchCategoryJob(music.SearchCategoryConfig{
		Grab: p.musicGrab(), Indexers: p.indexerConns, Clock: clock.NewSystem(), Logger: p.logger,
	})}
}

// candidateSearcher builds adapters.CandidateSearch over the real stores.
func (p *process) candidateSearcher() adapters.CandidateSearch {
	return adapters.CandidateSearch{
		Works: library.NewWorks(p.library), Desired: library.NewDesired(p.library), Searching: adapters.MediaTypes{Registry: p.media}.SearchingOf,
		Recognising: adapters.MediaTypes{Registry: p.media}.RecognisingOf,
		Search:      p.musicSearch(), Dispatch: p.musicDispatch(), DispatchFor: p.dispatchOf, Queue: p.queue, Clock: clock.NewSystem(),
		SearchesStopped: func() bool { return p.solarrMode().SearchesStopped() },
	}
}
