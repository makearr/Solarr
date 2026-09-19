package app

import (
	"context"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
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
		conns = append(conns, music.IndexerConn{
			ID: indexer.ID, Impl: indexer.Impl,
			Client: factory(indexer.ID, indexer.BaseURL, apiKey.Reveal(), indexer.Egress()),
		})
	}
	return conns, nil
}

// musicSearch builds internal/media/music.Search over the real stores.
func (p *process) musicSearch() music.Search {
	return music.Search{
		Works: library.NewWorks(p.library), Desired: library.NewDesired(p.library),
		Candidates: library.NewCandidates(p.library), Settings: p.cache, Clock: clock.NewSystem(),
	}
}

// downloaderConns resolves every enabled downloader connection fresh, the
// same "ask, don't cache" discipline indexerConns already follows. Only
// SABnzbd (usenet) exists today (ADR-46) -- an enabled torrent/ddl
// downloader row would simply never match any candidate's own protocol
// until a second implementation exists.
func (p *process) downloaderConns(ctx context.Context) ([]music.DownloaderConn, error) {
	configured, err := p.cache.Downloaders(ctx)
	if err != nil {
		return nil, err
	}
	factory := p.downloaderClientFactory()

	conns := make([]music.DownloaderConn, 0, len(configured))
	for _, downloader := range configured {
		if !downloader.Enabled || downloader.Impl != "sabnzbd" {
			continue
		}
		credentials, err := p.cache.DecryptedDownloaderCredentials(ctx, downloader.ID)
		if err != nil {
			return nil, err
		}
		conns = append(conns, music.DownloaderConn{
			ID: downloader.ID, Protocol: downloader.Protocol,
			Client: factory(downloader.ID, downloader.BaseURL, credentials.Reveal()),
		})
	}
	return conns, nil
}

// musicDispatch builds internal/media/music.Dispatch (ADR-57's own
// acquire.send) over the real stores.
func (p *process) musicDispatch() music.Dispatch {
	return music.Dispatch{
		Candidates: library.NewCandidates(p.library), Acquisitions: library.NewAcquisitions(p.library),
		Desired: library.NewDesired(p.library), Settings: p.cache, Downloaders: p.downloaderConns,
	}
}

// registerSearchCategoryJob wires the per-category candidate search (todo
// 5.2) into the registry and the executor.
func (p *process) registerSearchCategoryJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(music.SearchCategoryDefinition); err != nil {
		return err
	}
	job := music.NewSearchCategoryJob(music.SearchCategoryConfig{
		Search: p.musicSearch(), Indexers: p.indexerConns, Clock: clock.NewSystem(), Logger: p.logger,
	})
	executor.Handle(music.SearchCategoryJobType, job)
	return nil
}

// candidateSearcher builds adapters.CandidateSearch over the real stores.
func (p *process) candidateSearcher() adapters.CandidateSearch {
	return adapters.CandidateSearch{
		Works: library.NewWorks(p.library), Monitor: p.worksMonitor(),
		Search: p.musicSearch(), Dispatch: p.musicDispatch(),
		Queue: p.queue, Indexers: p.indexerConns, Clock: clock.NewSystem(),
	}
}
