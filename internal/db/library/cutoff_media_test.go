package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func TestCutoffPage_OnlyMusicAlbumsAreJudgedAsAlbums(t *testing.T) {
	opened, _ := newLibrary(t)
	album := createWork(t, opened, "music", "An Album")
	season := createWork(t, opened, "tv", "A Series")
	for _, workID := range []int64{album.ID, season.ID} {
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			_, err := q.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
				WorkID: workID, CategoryID: 1, ProfileID: 1, Monitored: 1, Dirty: 1, SatisfactionKind: "SET",
				RequiredCount: sql.NullInt64{Int64: 1, Valid: true}, State: "COMPLETE",
			})
			return err
		})
	}
	page, err := library.NewDesired(opened).CutoffPage(t.Context(), 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].WorkID != album.ID {
		t.Errorf("cutoff page = %+v, want only the music album: a season is not an album", page)
	}
}
