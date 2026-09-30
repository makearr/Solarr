package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/priority"
	"github.com/makearr/Solarr/internal/platform/clock"
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
		freshSince = FreshSinceCutoff(a.Clock)
	}
	rows, err := a.Works.List(ctx,
		library.WorkFilter{
			RootFolderIDs:   filter.RootFolderIDs,
			CategoryIDs:     filter.CategoryIDs,
			Sort:            library.WorkSort(filter.Sort),
			Direction:       library.SortDirection(filter.Direction),
			FreshSince:      freshSince,
			WantedOnly:      filter.Wanted,
			CutoffUnmetOnly: filter.CutoffUnmet,
		},
		library.WorkCursor{
			SortTitle: cursor.SortTitle, SortYear: cursor.SortYear, SortAdded: cursor.SortAdded,
			SortArtist: cursor.SortArtist, SortSearched: cursor.SortSearched, SortImported: cursor.SortImported, ID: cursor.ID,
		},
		pageSize)
	if err != nil {
		return nil, err
	}

	views := make([]apiserver.WorkRow, len(rows))
	for index, row := range rows {
		view := apiserver.WorkRow{
			ID: row.ID, MediaType: row.MediaType, Title: row.CanonicalTitle, SortTitle: row.SortTitle,
			ArtistSortName: row.ArtistSortName,
			AddedAt:        time.Unix(row.AddedAt, 0), UpdatedAt: time.Unix(row.UpdatedAt, 0),
		}
		if row.Year.Valid {
			view.Year = &row.Year.Int64
		}
		if row.Artist.Valid {
			view.Artist = &row.Artist.String
		}
		if row.WantedState.Valid {
			view.WantedState = &row.WantedState.String
		}
		if row.PresentCount.Valid {
			view.PresentCount = &row.PresentCount.Int64
		}
		if row.RequiredCount.Valid {
			view.RequiredCount = &row.RequiredCount.Int64
		}
		if row.AvailableFrom.Valid {
			at := time.Unix(row.AvailableFrom.Int64, 0).UTC()
			view.AvailableFrom = &at
		}
		view.Downloading = row.Downloading
		view.OnDisk = row.OnDisk
		view.Unverified = row.Unverified
		view.ArrivedAt = row.ArrivedAt
		view.ArtistCover = row.ArtistCover
		view.Monitored, view.Description, view.SizeBytes = row.Monitored, row.Description, row.SizeBytes
		if row.LastSearchedAt != 0 {
			at := time.Unix(row.LastSearchedAt, 0).UTC()
			view.LastSearchedAt = &at
		}
		views[index] = view
	}
	return views, nil
}

// FreshSinceCutoff resolves ADR-55's freshness window to a cutoff date
// string -- the one place library.WorkFilter.FreshSince's day-precision
// format boundary is crossed from a raw clock, shared by the Library list's
// own fresh=true filter (ListWorks above) and the dashboard's fresh-works
// counter sweep (cmd/solarr/app's freshCountsJob), so the two can never
// disagree about what "fresh" means.
func FreshSinceCutoff(cl clock.Clock) string {
	return cl.Now().AddDate(0, 0, -priority.DefaultNewReleaseDays).Format(time.DateOnly)
}
