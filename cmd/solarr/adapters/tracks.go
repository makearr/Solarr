package adapters

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// Tracks satisfies apiserver.LibraryTracksReader over library.TrackParts.
type Tracks struct{ Tracks library.TrackParts }

func (a Tracks) ListLibraryTracks(
	ctx context.Context, filter apiserver.TracksFilter, cursor apiserver.TracksCursor, albumPageSize int,
) ([]apiserver.TrackRow, apiserver.TracksCursor, bool, error) {
	where, err := listfilter.Parse(filter.Where, library.TrackFields)
	if err != nil {
		return nil, apiserver.TracksCursor{}, false, fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	rows, next, hasMore, err := a.Tracks.List(ctx,
		library.TrackFilter{
			RootFolderIDs: filter.RootFolderIDs, CategoryIDs: filter.CategoryIDs, Descending: filter.Descending,
			Search: filter.Search, Where: where,
		},
		library.TrackAlbumCursor{
			ArtistSortName: cursor.ArtistSortName, AlbumSortTitle: cursor.AlbumSortTitle, AlbumID: cursor.AlbumID,
		}, albumPageSize)
	if err != nil {
		return nil, apiserver.TracksCursor{}, false, err
	}

	views := make([]apiserver.TrackRow, len(rows))
	for index, row := range rows {
		view := apiserver.TrackRow{ID: row.ID, AlbumWorkID: row.AlbumWorkID, AlbumTitle: row.AlbumTitle}
		if row.Title.Valid {
			view.Title = &row.Title.String
		}
		if row.AlbumYear.Valid {
			view.AlbumYear = &row.AlbumYear.Int64
		}
		if row.ArtistName.Valid {
			view.ArtistName = &row.ArtistName.String
		}
		views[index] = view
	}

	return views, apiserver.TracksCursor{
		ArtistSortName: next.ArtistSortName, AlbumSortTitle: next.AlbumSortTitle, AlbumID: next.AlbumID,
	}, hasMore, nil
}
