package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// Search satisfies apiserver.SearchReader over library.Search, translating its errors to apiserver sentinels.
type Search struct{ Search library.Search }

func (a Search) SearchWorks(
	ctx context.Context, filter apiserver.SearchFilter, limit int,
) ([]apiserver.SearchResultRow, error) {
	results, err := a.Search.Works(ctx, filter.Query, filter.MediaType, filter.RootFolderIDs, filter.CategoryIDs, limit)
	if err != nil {
		if errors.Is(err, library.ErrQueryTooShort) {
			return nil, apiserver.ErrQueryTooShort
		}
		return nil, err
	}

	rows := make([]apiserver.SearchResultRow, len(results))
	for index, result := range results {
		row := apiserver.SearchResultRow{
			Work: apiserver.WorkRow{
				ID: result.Work.ID, MediaType: result.Work.MediaType, Title: result.Work.CanonicalTitle,
				SortTitle: result.Work.SortTitle,
				AddedAt:   time.Unix(result.Work.AddedAt, 0), UpdatedAt: time.Unix(result.Work.UpdatedAt, 0),
			},
			Score: result.Score,
		}
		if result.Work.Year.Valid {
			row.Work.Year = &result.Work.Year.Int64
		}
		rows[index] = row
	}
	return rows, nil
}
