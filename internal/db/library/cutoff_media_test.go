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

func TestCutoffItemsOfWork_AreThatWorksMonitoredItemsAfterTheCursorOnly(t *testing.T) {
	opened, _ := newLibrary(t)
	first := createWork(t, opened, "movie", "First")
	second := createWork(t, opened, "movie", "Second")
	var wanted []int64
	for _, entry := range []struct {
		workID    int64
		monitored int64
		state     string
	}{{first.ID, 1, "COMPLETE"}, {second.ID, 1, "COMPLETE"}, {first.ID, 0, "COMPLETE"}, {first.ID, 1, "MISSING"}, {first.ID, 1, "UPGRADABLE"}} {
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			item, err := q.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
				WorkID: entry.workID, CategoryID: 1, ProfileID: 1, Monitored: entry.monitored, Dirty: 1, SatisfactionKind: "SET",
				MediaRef: string(rune('a' + len(wanted))), RequiredCount: sql.NullInt64{Int64: 1, Valid: true}, State: entry.state,
			})
			wanted = append(wanted, item.ID)
			return err
		})
	}
	desired := library.NewDesired(opened)
	items, err := desired.CutoffItemsOfWork(t.Context(), first.ID, 0)
	if err != nil || len(items) != 2 || items[0].ID != wanted[0] || items[1].ID != wanted[4] {
		t.Fatalf("items = %+v, %v; want the first work's complete and upgradable monitored items", items, err)
	}
	if items, err = desired.CutoffItemsOfWork(t.Context(), first.ID, wanted[0]); err != nil || len(items) != 1 || items[0].ID != wanted[4] {
		t.Fatalf("after the first: %+v, %v", items, err)
	}
}
