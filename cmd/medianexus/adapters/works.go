package adapters

import (
	"context"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/identify/priority"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// Works satisfies apiserver.WorksReader over library.Works. Clock resolves
// Fresh's cutoff date -- library.WorkFilter never calls time.Now() itself
// (forbidigo), so this is the one place that boundary is crossed.
type Works struct {
	Works library.Works
	Clock clock.Clock
}

func (a Works) ListWorks(
	ctx context.Context, filter apiserver.WorksFilter, cursor apiserver.WorksCursor, pageSize int,
) ([]apiserver.WorkRow, error) {
	var freshSince string
	if filter.Fresh {
		cutoff := a.Clock.Now().AddDate(0, 0, -priority.DefaultNewReleaseDays)
		freshSince = cutoff.Format(time.DateOnly)
	}
	rows, err := a.Works.List(ctx,
		library.WorkFilter{
			RootFolderIDs: filter.RootFolderIDs,
			CategoryIDs:   filter.CategoryIDs,
			Sort:          library.WorkSort(filter.Sort),
			Direction:     library.SortDirection(filter.Direction),
			FreshSince:    freshSince,
		},
		library.WorkCursor{
			SortTitle: cursor.SortTitle, SortYear: cursor.SortYear, SortAdded: cursor.SortAdded, ID: cursor.ID,
		},
		pageSize)
	if err != nil {
		return nil, err
	}

	views := make([]apiserver.WorkRow, len(rows))
	for index, row := range rows {
		view := apiserver.WorkRow{
			ID: row.ID, MediaType: row.MediaType, Title: row.CanonicalTitle, SortTitle: row.SortTitle,
			AddedAt: time.Unix(row.AddedAt, 0), UpdatedAt: time.Unix(row.UpdatedAt, 0),
		}
		if row.Year.Valid {
			view.Year = &row.Year.Int64
		}
		if row.Artist.Valid {
			view.Artist = &row.Artist.String
		}
		views[index] = view
	}
	return views, nil
}
