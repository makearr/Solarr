package main

import (
	"context"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/library"
)

// artistCreditRole is the one credit role the Artists navigation level
// filters on -- music.CreditRoleArtist's own value, but internal/library
// cannot import internal/music (the dependency arrow points the other way),
// the same reason library.creditRoleArtist exists.
const artistCreditRole = "artist"

// artistsAdapter satisfies apiserver.LibraryArtistsReader over
// library.People. Neither package imports the other (worksAdapter's own doc
// comment explains why), so something in between has to translate.
type artistsAdapter struct{ people library.People }

func (a artistsAdapter) ListLibraryArtists(
	ctx context.Context, filter apiserver.ArtistsFilter, cursor apiserver.ArtistsCursor, pageSize int,
) ([]apiserver.ArtistRow, error) {
	rows, err := a.people.ListPeople(ctx,
		library.PersonFilter{
			Role: artistCreditRole, RootFolderIDs: filter.RootFolderIDs, CategoryIDs: filter.CategoryIDs,
		},
		library.PersonCursor{SortName: cursor.SortName, ID: cursor.ID}, pageSize)
	if err != nil {
		return nil, err
	}

	views := make([]apiserver.ArtistRow, len(rows))
	for index, row := range rows {
		views[index] = apiserver.ArtistRow{ID: row.ID, Name: row.Name, SortName: row.SortName}
	}
	return views, nil
}
