package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// Recommendations satisfies apiserver.DiscoverPool over library.db's recommendation pool.
type Recommendations struct{ Pool library.Recommendations }

func discoverItem(row library.PoolRecommendation) apiserver.DiscoverItem {
	return apiserver.DiscoverItem{
		ID: row.ID, CategoryID: row.CategoryID, Kind: row.Kind, Provider: row.Provider, ExternalID: row.ExternalID, Title: row.Title, Subtitle: row.Subtitle,
		Overview: row.Overview, PosterURL: row.PosterURL, Year: row.Year, Votes: row.Votes, WorkID: row.WorkID, Popularity: row.Popularity, Rating: row.Rating,
		Genres: row.Genres, Types: row.Types,
	}
}

// List implements apiserver.DiscoverPool.
func (a Recommendations) List(ctx context.Context, query apiserver.DiscoverQuery) ([]apiserver.DiscoverItem, error) {
	request := library.RecommendationQuery{
		CategoryIDs: query.CategoryIDs, Kind: query.Kind, Type: query.Type, Genre: query.Genre, Title: query.Title, UserID: query.UserID, Limit: query.Limit,
	}
	if query.HasCursor {
		request.After = &library.RecommendationCursor{Popularity: query.AfterPopularity, ID: query.AfterID}
	}
	rows, err := a.Pool.List(ctx, request)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.DiscoverItem, len(rows))
	for index, row := range rows {
		out[index] = discoverItem(row)
	}
	return out, nil
}

// Item implements apiserver.DiscoverPool.
func (a Recommendations) Item(ctx context.Context, id int64) (apiserver.DiscoverItem, bool, error) {
	row, found, err := a.Pool.Item(ctx, id)
	if err != nil || !found {
		return apiserver.DiscoverItem{}, false, err
	}
	return discoverItem(row), true, nil
}

// Genres implements apiserver.DiscoverPool.
func (a Recommendations) Genres(ctx context.Context, categoryID int64, kind string) ([]apiserver.DiscoverGenre, error) {
	rows, err := a.Pool.Genres(ctx, categoryID, kind)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.DiscoverGenre, len(rows))
	for index, row := range rows {
		out[index] = apiserver.DiscoverGenre{Genre: row.Genre, Items: row.Items}
	}
	return out, nil
}

// FeedStates implements apiserver.DiscoverPool.
func (a Recommendations) FeedStates(ctx context.Context, categoryID int64) ([]apiserver.DiscoverFeedState, error) {
	rows, err := a.Pool.FeedStates(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.DiscoverFeedState, len(rows))
	for index, row := range rows {
		source := row.Source
		if provider, found := settings.MetadataProviderByKey(row.Source); found {
			source = provider.Label
		}
		out[index] = apiserver.DiscoverFeedState{Type: row.Type, Kind: row.Kind, Source: source, LastError: row.LastError, Exhausted: row.Exhausted, RefreshedAt: row.RefreshedAt}
	}
	return out, nil
}

// Dismiss implements apiserver.DiscoverPool.
func (a Recommendations) Dismiss(ctx context.Context, userID int64, mediaType string, item apiserver.DiscoverItem) error {
	return a.Pool.Dismiss(ctx, library.RecommendationDismissal{
		UserID: userID, MediaType: mediaType, Kind: item.Kind, Provider: item.Provider, ExternalID: item.ExternalID, Title: item.Title,
	})
}

// Restore implements apiserver.DiscoverPool.
func (a Recommendations) Restore(ctx context.Context, userID int64, kind, provider, externalID string) error {
	return a.Pool.Restore(ctx, userID, kind, provider, externalID)
}

// Dismissals implements apiserver.DiscoverPool.
func (a Recommendations) Dismissals(ctx context.Context, userID int64) ([]apiserver.DiscoverDismissal, error) {
	rows, err := a.Pool.Dismissals(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.DiscoverDismissal, len(rows))
	for index, row := range rows {
		out[index] = apiserver.DiscoverDismissal{MediaType: row.MediaType, Kind: row.Kind, Provider: row.Provider, ExternalID: row.ExternalID, Title: row.Title, CreatedAt: row.CreatedAt}
	}
	return out, nil
}
