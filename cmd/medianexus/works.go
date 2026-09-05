package main

import (
	"context"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/library"
)

// worksAdapter satisfies apiserver.WorksReader over library.Works. Neither
// package imports the other (apiserver.WorksReader's doc comment explains why),
// so something in between has to translate -- the same reason cmd/medianexus
// already exists for job and scan wiring.
type worksAdapter struct{ works library.Works }

func (a worksAdapter) ListWorks(
	ctx context.Context, filter apiserver.WorksFilter, cursor apiserver.WorksCursor, pageSize int,
) ([]apiserver.WorkRow, error) {
	rows, err := a.works.List(ctx,
		library.WorkFilter{
			RootFolderIDs: filter.RootFolderIDs,
			CategoryIDs:   filter.CategoryIDs,
			Sort:          library.WorkSort(filter.Sort),
			Direction:     library.SortDirection(filter.Direction),
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
