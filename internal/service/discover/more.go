package discover

import (
	"context"
	"errors"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// ErrNothingToRead is a library with no enabled list to read further.
var ErrNothingToRead = errors.New("discover: no list to read further")

// morePages bounds the provider pages one Load more reads when the first ones admit nothing new.
const morePages = 3

// moreRankStride keeps appended ranks clear of the ranks a refresh gave the first pages.
const moreRankStride = 1000

// MoreResult is what one Load more found.
type MoreResult struct {
	// Added is how many items it put into the pool.
	Added int
	// Exhausted is every list read having no further page.
	Exhausted bool
}

// More reads the next provider page of a library's lists, one list or every enabled one with an empty listType, admits it by the
// same rules as a refresh and appends it to the pool.
func (s Service) More(ctx context.Context, categoryID int64, listType string) (MoreResult, error) {
	global := s.Config.Global()
	category, found := s.Config.Category(categoryID)
	libraries, err := s.Config.Libraries(ctx)
	if err != nil || !found {
		return MoreResult{}, ErrNothingToRead
	}
	index := slices.IndexFunc(libraries, func(one settings.LibraryDiscover) bool { return one.CategoryID == categoryID && one.Enabled })
	module, ok := s.Sources.Feeds(category.MediaType)
	if index < 0 || !ok {
		return MoreResult{}, ErrNothingToRead
	}
	lib := libraries[index]
	usable, _ := module.RecommendationFeeds(ctx)
	rules := s.rulesOf(global, lib, category)
	result := MoreResult{Exhausted: true}
	read := false
	for _, setting := range lib.Types {
		if !setting.Enabled || setting.Type == string(media.RecommendCollections) || (listType != "" && setting.Type != listType) {
			continue
		}
		for _, feed := range usable {
			if string(feed.Type) != setting.Type || (feed.Kind != "" && slices.Contains(lib.DisabledKinds, feed.Kind)) {
				continue
			}
			added, exhausted, err := s.moreOfFeed(ctx, category, lib, feed, module, rules)
			if err != nil {
				return result, err
			}
			read = true
			result.Added += added
			result.Exhausted = result.Exhausted && exhausted
		}
	}
	if !read {
		return MoreResult{}, ErrNothingToRead
	}
	return result, nil
}

func (s Service) moreOfFeed(ctx context.Context, category settings.Category, lib settings.LibraryDiscover, feed media.RecommendationFeed,
	module media.Recommending, rules Rules) (added int, exhausted bool, err error) {
	ref := library.FeedRef{CategoryID: category.ID, Kind: feed.Kind, Type: string(feed.Type)}
	state, err := s.Pool.FeedState(ctx, ref)
	if err != nil {
		return 0, false, err
	}
	if state.Exhausted || state.LastError != "" {
		return 0, state.Exhausted, nil
	}
	var admitted []library.PoolItem
	for read := 0; read < morePages && len(admitted) == 0 && !state.Exhausted; read++ {
		page := state.PagesRead + 1
		items, more, err := module.Recommend(ctx, media.RecommendQuery{
			CategoryID: category.ID, Feed: feed, Page: int(page), ClassicAgeYears: int(lib.ClassicAgeYears), Languages: rules.OriginalLanguages,
			ExcludeGenres: rules.ExcludeGenres, MaxAge: maxAgeOf(category),
		})
		if err != nil {
			state.LastError = failureOf(err)
			if recordErr := s.Pool.SetFeedState(ctx, ref, state); recordErr != nil {
				s.logger().WarnContext(ctx, "recommendation feed state", "error", recordErr)
			}
			return 0, false, nil
		}
		state.PagesRead, state.Exhausted = page, !more
		for _, item := range items {
			if item.Kind == "" {
				item.Kind = feed.Kind
			}
			facts, workID, err := s.Judge.FactsOf(ctx, category, item)
			if err != nil {
				continue
			}
			if Admit(item, rules, facts).Admitted {
				admitted = append(admitted, poolItemOf(item, workID))
			}
		}
	}
	state.RefreshedAt = s.Clock.Now().Unix()
	if err := s.Pool.AppendFeed(ctx, ref, admitted, (state.PagesRead-1)*moreRankStride, state, state.RefreshedAt); err != nil {
		return 0, false, err
	}
	return len(admitted), state.Exhausted, nil
}
