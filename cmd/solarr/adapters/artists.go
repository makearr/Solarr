package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// artistCreditRole is the one credit role the Artists navigation level
// filters on -- music.CreditRoleArtist's own value, but internal/db/library
// cannot import internal/media/music (the dependency arrow points the other way),
// the same reason library.creditRoleArtist exists.
const artistCreditRole = "artist"

// Artists satisfies apiserver.LibraryArtistsReader over library.People.
type Artists struct{ People library.People }

func (a Artists) ListLibraryArtists(
	ctx context.Context, filter apiserver.ArtistsFilter, cursor apiserver.ArtistsCursor, pageSize int,
) ([]apiserver.ArtistRow, error) {
	rows, err := a.People.ListPeople(ctx,
		library.PersonFilter{
			Role: artistCreditRole, RootFolderIDs: filter.RootFolderIDs, CategoryIDs: filter.CategoryIDs,
			Descending: filter.Descending,
		},
		library.PersonCursor{SortName: cursor.SortName, ID: cursor.ID}, pageSize)
	if err != nil {
		return nil, err
	}

	views := make([]apiserver.ArtistRow, len(rows))
	for index, row := range rows {
		views[index] = apiserver.ArtistRow{
			ID: row.ID, Name: row.Name, SortName: row.SortName,
			AlbumsWanted: row.AlbumsWanted, AlbumsComplete: row.AlbumsComplete,
			Downloading: row.Downloading,
		}
	}
	return views, nil
}
