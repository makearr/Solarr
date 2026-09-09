package main

import (
	"context"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/library"
)

// tracksAdapter satisfies apiserver.LibraryTracksReader over
// library.TrackParts. Neither package imports the other (worksAdapter's own
// doc comment explains why), so something in between has to translate.
type tracksAdapter struct{ tracks library.TrackParts }

func (a tracksAdapter) ListLibraryTracks(
	ctx context.Context, filter apiserver.TracksFilter, cursor apiserver.TracksCursor, albumPageSize int,
) ([]apiserver.TrackRow, apiserver.TracksCursor, bool, error) {
	rows, next, hasMore, err := a.tracks.List(ctx,
		library.TrackFilter{RootFolderIDs: filter.RootFolderIDs, CategoryIDs: filter.CategoryIDs},
		library.TrackAlbumCursor{
			ArtistSortName: cursor.ArtistSortName, AlbumSortTitle: cursor.AlbumSortTitle, AlbumID: cursor.AlbumID,
		}, albumPageSize)
	if err != nil {
		return nil, apiserver.TracksCursor{}, false, err
	}

	views := make([]apiserver.TrackRow, len(rows))
	for index, row := range rows {
		view := apiserver.TrackRow{ID: row.ID, AlbumTitle: row.AlbumTitle}
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
