package discover

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
)

// CollectionSource names the members of collections a library holds a part of that it does not want yet.
type CollectionSource interface {
	Candidates(ctx context.Context, category settings.Category, limit int64) ([]media.Recommendation, error)
}

// refreshCollections admits a library's missing collection members like any feed; the rating is what the member row stores, so unknown.
func (s Service) refreshCollections(ctx context.Context, category settings.Category, setting settings.DiscoverTypeSetting, rules Rules) {
	if s.Collections == nil {
		return
	}
	ref := library.FeedRef{CategoryID: category.ID, Type: setting.Type}
	now := s.Clock.Now().Unix()
	state := library.FeedState{FeedRef: ref, RefreshedAt: now, PagesRead: 1, Exhausted: true}
	// A rejected candidate must not starve the list, so read more than the list holds.
	candidates, err := s.Collections.Candidates(ctx, category, setting.Amount*4)
	if err != nil {
		state.LastError = FailureOther
		s.logger().WarnContext(ctx, "collection recommendations failed", slog.Int64("category_id", category.ID), slog.Any("error", err))
		if recordErr := s.Pool.SetFeedState(ctx, ref, state); recordErr != nil {
			s.logger().WarnContext(ctx, "recommendation feed state", slog.Any("error", recordErr))
		}
		return
	}
	var admitted []library.PoolItem
	for _, item := range candidates {
		if int64(len(admitted)) >= setting.Amount {
			break
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
	if err := s.Pool.ReplaceFeed(ctx, ref, admitted, state, now); err != nil {
		s.logger().WarnContext(ctx, "recommendation pool", slog.Int64("category_id", category.ID), slog.String("type", setting.Type), slog.Any("error", err))
	}
}
