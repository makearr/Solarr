package app

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/discover"
)

// discoverService is the refresh of Discover's pool: settings from the cache, feeds from the media types.
func (p *process) discoverService() discover.Service {
	pool := library.NewRecommendations(p.library)
	return discover.Service{
		AutoAdd: &discover.AutoAdd{
			Pool: pool, SearchAllowed: func() bool { return !p.solarrMode().SearchesStopped() },
			Adders: func(mediaType string) (media.Adding, bool) {
				return media.Capability[media.Adding](p.media, media.Key(mediaType))
			},
			AfterAdd: func(ctx context.Context) { _, _ = p.enqueueCategoryCounts(ctx) },
		},
		Config: discoverConfiguration{cache: p.cache}, Pool: library.NewRecommendations(p.library), Sources: discoverSources{registry: p.media},
		Judge:       discoverJudge{process: p, pool: library.NewRecommendations(p.library), exclusions: library.NewExclusions(p.library)},
		Collections: discoverCollections{pool: pool},
		Clock:       clock.NewSystem(), Logger: p.logger,
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

// discoverRefresher queues the refresh a settings change asks for.
type discoverRefresher struct{ process *process }

func (r discoverRefresher) RefreshDiscover(ctx context.Context, categoryID int64) error {
	_, err := discover.Enqueue(ctx, r.process.queue, categoryID)
	return err
}

func (r discoverRefresher) ForgetDiscover(ctx context.Context, categoryID int64) error {
	return library.NewRecommendations(r.process.library).ForgetCategory(ctx, categoryID)
}

type discoverCollections struct{ pool library.Recommendations }

func (c discoverCollections) Candidates(ctx context.Context, category settings.Category, limit int64) ([]media.Recommendation, error) {
	rows, err := c.pool.CollectionCandidates(ctx, category.ID, category.MediaType, limit)
	if err != nil {
		return nil, err
	}
	out := make([]media.Recommendation, 0, len(rows))
	for _, row := range rows {
		out = append(out, media.Recommendation{Provider: row.Provider, ExternalID: row.ExternalID, Title: row.Title, Subtitle: row.Collection, Overview: row.Overview, Year: row.Year, Traits: media.Traits{OriginalLanguage: row.OriginalLanguage}})
	}
	return out, nil
}

// seriesFeeds reads TMDB's charts for a series library and AniList's for an anime one.
type seriesFeeds struct {
	series, anime media.Recommending
	isAnime       func(categoryID int64) bool
}

// RecommendationFeeds answers TMDB's feeds, or AniList's alone while TMDB has no key (which AniList does not need).
func (f seriesFeeds) RecommendationFeeds(ctx context.Context) ([]media.RecommendationFeed, map[media.RecommendationFeed]string) {
	usable, unavailable := f.series.RecommendationFeeds(ctx)
	if len(usable) == 0 {
		usable, _ = f.anime.RecommendationFeeds(ctx)
	}
	return usable, unavailable
}

func (f seriesFeeds) Recommend(ctx context.Context, query media.RecommendQuery) ([]media.Recommendation, bool, error) {
	if f.isAnime(query.CategoryID) {
		return f.anime.Recommend(ctx, query)
	}
	return f.series.Recommend(ctx, query)
}

// resolveSeriesEntry names the series a list entry is through the series type's list resolver.
func (p *process) resolveSeriesEntry(ctx context.Context, entry media.ListEntry) (media.ListMatch, error) {
	resolver, ok := media.Capability[media.ListResolving](p.media, media.Key("tv"))
	if !ok {
		return media.ListMatch{}, media.ErrNotWired
	}
	return resolver.ResolveListEntry(ctx, entry)
}

// discoverPreviewer answers the auto-add preview from the same judgement a refresh makes.
type discoverPreviewer struct{ process *process }

func (r discoverPreviewer) PreviewDiscoverAutoAdd(ctx context.Context, one settings.LibraryDiscover) ([]apiserver.DiscoverPreviewItem, error) {
	verdicts, err := r.process.discoverService().AutoAddPreview(ctx, one)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.DiscoverPreviewItem, 0, len(verdicts))
	for _, verdict := range verdicts {
		item := apiserver.DiscoverPreviewItem{
			Title: verdict.Item.Title, Year: verdict.Item.Year, Type: verdict.Type, Rating: verdict.Item.Rating, Votes: verdict.Item.Votes, WouldAdd: verdict.Decision.Admitted,
		}
		for _, rejection := range verdict.Decision.Rejections {
			item.Rejections = append(item.Rejections, apiserver.DiscoverPreviewRejection{Reason: string(rejection.Reason), Value: rejection.Value})
		}
		out = append(out, item)
	}
	return out, nil
}
