package app

import (
	"context"

	"github.com/makearr/MediaNexus/cmd/medianexus/adapters"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/platform/clock"
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
		Works: library.NewWorks(p.library), Search: p.musicSearch(), Queue: p.queue,
		Indexers: p.indexerConns, Clock: clock.NewSystem(),
	}
}
