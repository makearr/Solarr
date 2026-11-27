package discover

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// AutoAddPool is the pool and the auto-add record a library's auto-add reads and writes.
type AutoAddPool interface {
	List(ctx context.Context, query library.RecommendationQuery) ([]library.PoolRecommendation, error)
	RecordAutoAdd(ctx context.Context, record library.AutoAddRecord) error
	AutoAddedSince(ctx context.Context, categoryID, since int64) (int64, error)
	WasAutoAdded(ctx context.Context, categoryID int64, kind, provider, externalID string) (bool, error)
}

// AutoAdd is what adds recommendations by itself; a nil Service.AutoAdd leaves auto-add off.
type AutoAdd struct {
	Pool AutoAddPool
	// Adders answers a media type's add.
	Adders func(mediaType string) (media.Adding, bool)
	// SearchAllowed is false while the mode stops automatic searches, so an add never asks for one then.
	SearchAllowed func() bool
	// AfterAdd runs once a library added something, so its counts follow; nil does nothing.
	AfterAdd func(ctx context.Context)
}

// autoAddCandidates bounds how many pool items one list considers a run; the daily ceiling is far lower.
const autoAddCandidates = 200

// PreviewItem is one pool item and what auto-add would do with it: add it, or why it is held back.
type PreviewItem struct {
	Item     library.PoolRecommendation
	Type     string
	Decision Decision
}

// AutoAddPreview answers what auto-add would add now with these unsaved settings, and for the rest each reason; it writes nothing.
func (s Service) AutoAddPreview(ctx context.Context, lib settings.LibraryDiscover) ([]PreviewItem, error) {
	if s.AutoAdd == nil {
		return nil, nil
	}
	category, found := s.Config.Category(lib.CategoryID)
	if !found {
		return nil, fmt.Errorf("discover: no library %d", lib.CategoryID)
	}
	return s.autoAddVerdicts(ctx, s.Config.Global(), lib, category)
}

// runAutoAdd adds what the gates admit, best rated first, up to the daily ceiling.
func (s Service) runAutoAdd(ctx context.Context, global settings.DiscoverGlobal, lib settings.LibraryDiscover, category settings.Category) {
	if s.AutoAdd == nil || !lib.AutoAdd {
		return
	}
	adder, ok := s.AutoAdd.Adders(category.MediaType)
	if !ok {
		return
	}
	verdicts, err := s.autoAddVerdicts(ctx, global, lib, category)
	if err != nil {
		s.logger().WarnContext(ctx, "auto-add", slog.Int64("category_id", category.ID), slog.Any("error", err))
		return
	}
	search := lib.AutoAddSearch && (s.AutoAdd.SearchAllowed == nil || s.AutoAdd.SearchAllowed())
	added := false
	for _, verdict := range verdicts {
		if !verdict.Decision.Admitted {
			continue
		}
		item := verdict.Item
		result, err := adder.AddWork(ctx, media.AddRequest{
			CategoryID: category.ID, Kind: item.Kind, Provider: item.Provider, ExternalID: item.ExternalID, Monitored: true, Search: search,
		})
		if errors.Is(err, media.ErrInvalidAdd) {
			s.logger().WarnContext(ctx, "auto-add refused", slog.Int64("category_id", category.ID), slog.String("title", item.Title), slog.Any("error", err))
			continue
		}
		if err != nil {
			s.logger().WarnContext(ctx, "auto-add failed", slog.Int64("category_id", category.ID), slog.Any("error", err))
			break
		}
		added = true
		record := library.AutoAddRecord{
			CategoryID: category.ID, Kind: item.Kind, Provider: item.Provider, ExternalID: item.ExternalID, Title: item.Title, Type: verdict.Type,
			Rating: item.Rating, Votes: item.Votes, WorkID: result.WorkID, AddedAt: s.Clock.Now().Unix(),
		}
		if err := s.AutoAdd.Pool.RecordAutoAdd(ctx, record); err != nil {
			s.logger().WarnContext(ctx, "auto-add record", slog.Int64("category_id", category.ID), slog.Any("error", err))
		}
		s.logger().InfoContext(ctx, "recommendation added automatically", slog.Int64("category_id", category.ID), slog.String("title", item.Title),
			slog.String("type", verdict.Type), slog.Float64("rating", item.Rating), slog.Int64("votes", item.Votes))
	}
	if added && s.AutoAdd.AfterAdd != nil {
		s.AutoAdd.AfterAdd(ctx)
	}
}

// autoAddVerdicts judges the pool items of every enabled list as one pool, best rated first (votes, then the pool's own
// popularity order, break ties); the daily ceiling counts the items this pass would add, so a preview and a run agree.
func (s Service) autoAddVerdicts(ctx context.Context, global settings.DiscoverGlobal, lib settings.LibraryDiscover, category settings.Category) ([]PreviewItem, error) {
	module, ok := s.Sources.Feeds(category.MediaType)
	if !ok {
		return nil, nil
	}
	usable := s.usableFeeds(ctx, module, category.ID)
	rules := s.rulesOf(global, lib, category)
	gates := AutoAddGates{MinRating: lib.MinRating, MinVotes: lib.MinVotes, PerDay: lib.AutoAddPerDay}
	addedToday, err := s.AutoAdd.Pool.AutoAddedSince(ctx, category.ID, s.Clock.Now().Add(-24*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	type candidate struct {
		item     library.PoolRecommendation
		listType string
	}
	var pool []candidate
	seen := map[string]bool{}
	for _, setting := range lib.Types {
		if !setting.Enabled {
			continue
		}
		items, err := s.AutoAdd.Pool.List(ctx, library.RecommendationQuery{CategoryIDs: []int64{category.ID}, Type: setting.Type, Limit: autoAddCandidates})
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := item.Kind + "\x00" + item.Provider + "\x00" + item.ExternalID
			if !seen[key] {
				seen[key] = true
				pool = append(pool, candidate{item: item, listType: setting.Type})
			}
		}
	}
	slices.SortStableFunc(pool, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(b.item.Rating, a.item.Rating), cmp.Compare(b.item.Votes, a.item.Votes), cmp.Compare(b.item.Popularity, a.item.Popularity))
	})
	var out []PreviewItem
	for _, one := range pool {
		verdict, err := s.judgeAutoAdd(ctx, category, rules, gates, usable, one.item, one.listType, addedToday)
		if err != nil {
			return nil, err
		}
		if verdict.Decision.Admitted {
			addedToday++
		}
		out = append(out, verdict)
	}
	return out, nil
}

func (s Service) judgeAutoAdd(ctx context.Context, category settings.Category, rules Rules, gates AutoAddGates, usable []media.RecommendationFeed,
	pooled library.PoolRecommendation, listType string, addedToday int64) (PreviewItem, error) {
	item := media.Recommendation{
		Kind: pooled.Kind, Provider: pooled.Provider, ExternalID: pooled.ExternalID, Title: pooled.Title, Rating: pooled.Rating, Votes: pooled.Votes,
		Networks: pooled.Networks, Traits: media.Traits{Genres: pooled.Genres, OriginalLanguage: pooled.OriginalLanguage, Rating: pooled.Certification},
	}
	facts, _, err := s.Judge.FactsOf(ctx, category, item)
	if err != nil {
		return PreviewItem{}, err
	}
	decision := Admit(item, rules, facts)
	addedBefore, err := s.AutoAdd.Pool.WasAutoAdded(ctx, category.ID, pooled.Kind, pooled.Provider, pooled.ExternalID)
	if err != nil {
		return PreviewItem{}, err
	}
	ofList := func(rated bool) bool {
		return slices.ContainsFunc(usable, func(feed media.RecommendationFeed) bool {
			return string(feed.Type) == listType && feed.Kind == pooled.Kind && feed.HasRating == rated
		})
	}
	// An unrated item of a list that also has an unrated source is taken to be that source's, so the rating gate skips it.
	hasRating := ofList(true) && (pooled.Rating > 0 || !ofList(false))
	auto := AutoAdmit(item, gates, AutoAddFacts{HasRating: hasRating, AddedBefore: addedBefore, AddedToday: addedToday})
	decision.Rejections = append(decision.Rejections, auto.Rejections...)
	decision.Admitted = len(decision.Rejections) == 0
	return PreviewItem{Item: pooled, Type: listType, Decision: decision}, nil
}
