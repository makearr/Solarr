package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/discover"
)

// discoverService is the refresh of Discover's pool: settings from the cache, feeds from the media types.
func (p *process) discoverService() discover.Service {
	return discover.Service{
		Config: discoverConfiguration{cache: p.cache}, Pool: library.NewRecommendations(p.library), Sources: discoverSources{registry: p.media},
		Judge: discoverJudge{process: p, pool: library.NewRecommendations(p.library), exclusions: library.NewExclusions(p.library)},
		Clock: clock.NewSystem(), Logger: p.logger,
	}
}

type discoverConfiguration struct{ cache *settings.Cache }

func (c discoverConfiguration) Global() settings.DiscoverGlobal {
	return c.cache.Snapshot().DiscoverGlobal()
}
func (c discoverConfiguration) Libraries(ctx context.Context) ([]settings.LibraryDiscover, error) {
	return c.cache.LibraryDiscovers(ctx)
}
func (c discoverConfiguration) Category(id int64) (settings.Category, bool) {
	return c.cache.Snapshot().Category(id)
}

type discoverSources struct{ registry *media.Registry }

func (s discoverSources) Feeds(mediaType string) (media.Recommending, bool) {
	return media.Capability[media.Recommending](s.registry, media.Key(mediaType))
}

type discoverJudge struct {
	process    *process
	pool       library.Recommendations
	exclusions library.Exclusions
}

func (j discoverJudge) RatingLimit(category settings.Category) discover.RatingLimit {
	limit := discover.RatingLimit{MaxAge: -1, AllowUnrated: category.AllowUnrated}
	if category.MaxAge != nil {
		limit.MaxAge = *category.MaxAge
	}
	if reader, ok := media.Capability[media.Rating](j.process.media, media.Key(category.MediaType)); ok {
		limit.AgeOf = reader.RatingAge
	}
	return limit
}

func (j discoverJudge) DefaultLanguages(category settings.Category) []string {
	return settings.LanguageProfileFor(j.process.cache.Snapshot().DefaultLanguages(), category.Kind == "anime").AudioPreferred
}

func (j discoverJudge) FactsOf(ctx context.Context, category settings.Category, item media.Recommendation) (discover.Facts, int64, error) {
	workID, monitored, err := j.pool.Held(ctx, item.Provider, item.ExternalID)
	if err != nil {
		return discover.Facts{}, 0, err
	}
	excluded, err := j.exclusions.Excluded(ctx, []library.ExternalIDWrite{{Provider: item.Provider, ExternalID: item.ExternalID}})
	if err != nil {
		return discover.Facts{}, 0, err
	}
	suggestion, found, err := j.process.suggester().ForCandidate(ctx, category.MediaType, item.Traits)
	if err != nil {
		return discover.Facts{}, 0, err
	}
	return discover.Facts{Monitored: monitored, Excluded: excluded, OtherLibrary: found && suggestion.CategoryID != category.ID}, workID, nil
}

// registerDiscoverJob wires the refresh of the recommendation pool.
func (p *process) registerDiscoverJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(discover.Definition); err != nil {
		return err
	}
	executor.Handle(discover.JobType, p.discoverService())
	return nil
}

// enqueueDiscoverRefresh submits the refresh of every enabled library; registered as a task, so it runs daily.
func (p *process) enqueueDiscoverRefresh(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := discover.Enqueue(ctx, p.queue, 0)
	if err != nil {
		p.logger.Warn("enqueue discover refresh", slog.Any("error", err))
	}
	return result, err
}
