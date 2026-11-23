package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// JobType is the refresh of the recommendation pool of one library, or of every enabled library.
const JobType = jobs.Type("discover.refresh")

const actionKey = "discover.refresh"

// maxPages bounds how many provider pages one feed reads in one refresh.
const maxPages = 5

// Definition declares the refresh: background metadata work that reads providers and writes only Discover's own pool.
var Definition = jobs.Definition{Name: "Refreshing recommendations", Type: JobType, Pool: jobs.PoolMetadata, MaxAttempts: 2}

// Payload scopes a run to one library; zero is every enabled library.
type Payload struct {
	CategoryID int64 `json:"category_id,omitempty"`
}

// The codes a feed's last read leaves for the interface to word; empty is a read that worked.
const (
	FailureSourceUnavailable = "source_unavailable"
	FailureNotSupported      = "not_supported"
	FailureOther             = "failed"
)

// Enqueue submits a refresh of one library, or of every enabled one when categoryID is 0.
func Enqueue(ctx context.Context, queue *jobs.Queue, categoryID int64) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(Payload{CategoryID: categoryID})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("discover: payload: %w", err)
	}
	key, priority := actionKey, jobs.PriorityLow
	if categoryID != 0 {
		key, priority = actionKey+":"+strconv.FormatInt(categoryID, 10), jobs.PriorityNormal
	}
	return queue.Enqueue(ctx, jobs.Request{Type: JobType, ActionKey: key, Priority: priority, Payload: payload})
}

// Configuration is where the refresh reads Discover's settings and libraries.
type Configuration interface {
	Global() settings.DiscoverGlobal
	Libraries(ctx context.Context) ([]settings.LibraryDiscover, error)
	Category(id int64) (settings.Category, bool)
}

// Pool is where a refresh keeps what it admitted.
type Pool interface {
	ReplaceFeed(ctx context.Context, ref library.FeedRef, items []library.PoolItem, state library.FeedState, at int64) error
	SetFeedState(ctx context.Context, ref library.FeedRef, state library.FeedState) error
}

// Sources answers a media type's recommendation feeds; false for a type that has none.
type Sources interface {
	Feeds(mediaType string) (media.Recommending, bool)
}

// Judge answers what a library knows of its own rules and of one item.
type Judge interface {
	// RatingLimit is the library's maximum rating as its type reads certification texts.
	RatingLimit(category settings.Category) RatingLimit
	// DefaultLanguages are the original languages a library recommends when none were chosen.
	DefaultLanguages(category settings.Category) []string
	// FactsOf says what the library holds of the item and which work, 0 when none.
	FactsOf(ctx context.Context, category settings.Category, item media.Recommendation) (facts Facts, workID int64, err error)
}

// Service refreshes the pool.
type Service struct {
	Config  Configuration
	Pool    Pool
	Sources Sources
	Judge   Judge
	// Collections names the members of held collections; nil leaves the collections list empty.
	Collections CollectionSource
	// AutoAdd adds what the gates admit once a library is refreshed; nil leaves auto-add off.
	AutoAdd *AutoAdd
	Clock   clock.Clock
	Logger  *slog.Logger
}

func (s Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// Run is the job: every enabled list of every enabled library, or of the one the payload names; a failing feed keeps its
// old rows and records why, and the others go on.
func (s Service) Run(ctx context.Context, job jobs.Job) error {
	var payload Payload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Terminalf("discover.payload", string(JobType), err)
		}
	}
	global := s.Config.Global()
	if !global.Enabled {
		return nil
	}
	libraries, err := s.Config.Libraries(ctx)
	if err != nil {
		return fmt.Errorf("discover: libraries: %w", err)
	}
	for _, lib := range libraries {
		if !lib.Enabled || (payload.CategoryID != 0 && lib.CategoryID != payload.CategoryID) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("discover.context", string(JobType), err)
		}
		category, found := s.Config.Category(lib.CategoryID)
		if !found {
			continue
		}
		s.refreshLibrary(ctx, global, lib, category)
	}
	return nil
}

func (s Service) refreshLibrary(ctx context.Context, global settings.DiscoverGlobal, lib settings.LibraryDiscover, category settings.Category) {
	module, ok := s.Sources.Feeds(category.MediaType)
	if !ok {
		return
	}
	usable, _ := module.RecommendationFeeds(ctx)
	rules := s.rulesOf(global, lib, category)
	for _, setting := range lib.Types {
		if !setting.Enabled {
			continue
		}
		if setting.Type == string(media.RecommendCollections) {
			s.refreshCollections(ctx, category, setting, rules)
			continue
		}
		for _, feed := range usable {
			if string(feed.Type) != setting.Type || (feed.Kind != "" && slices.Contains(lib.DisabledKinds, feed.Kind)) {
				continue
			}
			s.refreshFeed(ctx, category, lib, setting, feed, module, rules)
		}
	}
	s.runAutoAdd(ctx, global, lib, category)
}

// rulesOf is what a library keeps out: its exclusions joined with the instance's, its languages and its rating limit.
func (s Service) rulesOf(global settings.DiscoverGlobal, lib settings.LibraryDiscover, category settings.Category) Rules {
	rules := Rules{
		ExcludeGenres: Union(global.ExcludeGenres, lib.ExcludeGenres), ExcludeNetworks: Union(global.ExcludeNetworks, lib.ExcludeNetworks),
		OriginalLanguages: lib.OriginalLanguages, Rating: s.Judge.RatingLimit(category),
	}
	if !lib.LanguagesSet {
		rules.OriginalLanguages = s.Judge.DefaultLanguages(category)
	}
	return rules
}

func (s Service) refreshFeed(ctx context.Context, category settings.Category, lib settings.LibraryDiscover, setting settings.DiscoverTypeSetting,
	feed media.RecommendationFeed, module media.Recommending, rules Rules) {
	ref := library.FeedRef{CategoryID: category.ID, Kind: feed.Kind, Type: setting.Type}
	now := s.Clock.Now().Unix()
	var admitted []library.PoolItem
	state := library.FeedState{FeedRef: ref, RefreshedAt: now}
	for page := 1; page <= maxPages && int64(len(admitted)) < setting.Amount; page++ {
		items, more, err := module.Recommend(ctx, media.RecommendQuery{
			CategoryID: category.ID, Feed: feed, Page: page, ClassicAgeYears: int(lib.ClassicAgeYears), Languages: rules.OriginalLanguages,
			ExcludeGenres: rules.ExcludeGenres, MaxAge: maxAgeOf(category),
		})
		if err != nil {
			state.LastError = failureOf(err)
			s.logger().WarnContext(ctx, "recommendation feed failed", slog.Int64("category_id", category.ID), slog.String("type", setting.Type),
				slog.String("source", feed.Source), slog.String("code", state.LastError), slog.Any("error", err))
			if recordErr := s.Pool.SetFeedState(ctx, ref, state); recordErr != nil {
				s.logger().WarnContext(ctx, "recommendation feed state", slog.Any("error", recordErr))
			}
			return
		}
		state.PagesRead = int64(page)
		for _, item := range items {
			if int64(len(admitted)) >= setting.Amount {
				break
			}
			if item.Kind == "" {
				item.Kind = feed.Kind
			}
			facts, workID, err := s.Judge.FactsOf(ctx, category, item)
			if err != nil {
				s.logger().WarnContext(ctx, "recommendation facts", slog.Int64("category_id", category.ID), slog.String("external_id", item.ExternalID), slog.Any("error", err))
				continue
			}
			if Admit(item, rules, facts).Admitted {
				admitted = append(admitted, poolItemOf(item, workID))
			}
		}
		if !more {
			state.Exhausted = true
			break
		}
	}
	if err := s.Pool.ReplaceFeed(ctx, ref, admitted, state, now); err != nil {
		s.logger().WarnContext(ctx, "recommendation pool", slog.Int64("category_id", category.ID), slog.String("type", setting.Type), slog.Any("error", err))
	}
}

func maxAgeOf(category settings.Category) int64 {
	if category.MaxAge == nil {
		return -1
	}
	return *category.MaxAge
}

func failureOf(err error) string {
	switch {
	case errors.Is(err, media.ErrSourceUnavailable):
		return FailureSourceUnavailable
	case errors.Is(err, media.ErrNotWired):
		return FailureNotSupported
	}
	return FailureOther
}

func poolItemOf(item media.Recommendation, workID int64) library.PoolItem {
	return library.PoolItem{
		Kind: item.Kind, Provider: item.Provider, ExternalID: item.ExternalID, Title: item.Title, Subtitle: item.Subtitle, Overview: item.Overview,
		PosterURL: item.PosterURL, Year: item.Year, ReleasedOn: item.ReleasedOn, Popularity: item.Popularity, Rating: item.Rating, Votes: item.Votes,
		OriginalLanguage: item.Traits.OriginalLanguage, Certification: item.Traits.Rating, Networks: item.Networks, Genres: item.Traits.Genres, WorkID: workID,
	}
}
