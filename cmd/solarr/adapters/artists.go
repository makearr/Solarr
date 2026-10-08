package adapters

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/listfilter"
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
	where, err := listfilter.Parse(filter.Where, library.PersonFields([]string{artistCreditRole}))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	rows, err := a.People.List(ctx,
		library.PeopleFilter{
			Visible: filter.CategoryIDs, Roles: []string{artistCreditRole}, Monitored: filter.Monitored, Where: where,
			Desc: filter.Descending,
		},
		library.PeopleCursor{Name: cursor.SortName, ID: cursor.ID, Set: cursor.SortName != "" || cursor.ID != 0}, pageSize)
	if err != nil {
		return nil, err
	}

	views := make([]apiserver.ArtistRow, len(rows))
	for index, row := range rows {
		views[index] = apiserver.ArtistRow{
			ID: row.ID, Name: row.Name, SortName: row.SortName,
			AlbumsWanted: row.Wanted, AlbumsComplete: row.Complete,
			Downloading: row.Downloading,
		}
	}
	return views, nil
}
