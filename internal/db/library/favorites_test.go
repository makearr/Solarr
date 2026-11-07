package library_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestFavorites_AreKeptPerUserAndKindAndSetIsIdempotent(t *testing.T) {
	opened, _ := newLibrary(t)
	favorites := library.NewFavorites(opened, clock.NewFakeAtEpoch())

	for _, step := range []struct {
		user     int64
		kind     string
		id       int64
		favorite bool
	}{
		{1, library.FavoriteWork, 10, true}, {1, library.FavoriteWork, 10, true}, {1, library.FavoriteWork, 11, true},
		{2, library.FavoriteWork, 12, true}, {1, library.FavoritePerson, 10, true}, {1, library.FavoriteWork, 11, false},
		{1, library.FavoriteWork, 99, false},
	} {
		if err := favorites.Set(t.Context(), step.user, step.kind, step.id, step.favorite); err != nil {
			t.Fatalf("Set(%+v): %v", step, err)
		}
	}

	for _, want := range []struct {
		user int64
		kind string
		ids  []int64
	}{
		{1, library.FavoriteWork, []int64{10}}, {2, library.FavoriteWork, []int64{12}},
		{1, library.FavoritePerson, []int64{10}}, {1, library.FavoriteCollection, []int64{}},
	} {
		got, err := favorites.IDs(t.Context(), want.user, want.kind)
		if err != nil {
			t.Fatalf("IDs: %v", err)
		}
		if !slices.Equal(got, want.ids) {
			t.Errorf("IDs(user %d, %s) = %v, want %v", want.user, want.kind, got, want.ids)
		}
	}
}

func TestFavorites_AnUnknownKindIsRefusedByTheTable(t *testing.T) {
	opened, _ := newLibrary(t)
	if err := library.NewFavorites(opened, clock.NewFakeAtEpoch()).Set(t.Context(), 1, "genre", 1, true); err == nil {
		t.Fatal("hearting a kind the table does not know succeeded")
	}
	if library.IsFavoriteSubject("genre") || !library.IsFavoriteSubject(library.FavoriteOrganisation) {
		t.Error("IsFavoriteSubject disagrees with the table")
	}
}

func TestListWorks_FavoriteNarrowsToTheViewersHearts(t *testing.T) {
	opened, _ := newLibrary(t)
	favorites := library.NewFavorites(opened, clock.NewFakeAtEpoch())
	hearted := createWork(t, opened, "music", "amber rivers")
	createWork(t, opened, "music", "broken signals")
	for _, step := range []struct{ user, id int64 }{{1, hearted.ID}, {2, hearted.ID + 1}} {
		if err := favorites.Set(t.Context(), step.user, library.FavoriteWork, step.id, true); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	page, err := library.NewWorks(opened).List(t.Context(), library.WorkFilter{FavoriteUserID: 1}, library.WorkCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 1 || page[0].ID != hearted.ID {
		t.Fatalf("user 1's favorites = %+v, want only work %d", page, hearted.ID)
	}
	all, err := library.NewWorks(opened).List(t.Context(), library.WorkFilter{}, library.WorkCursor{}, 100)
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered list = %d works, %v; want 2", len(all), err)
	}
}

func TestListPeople_FavoriteNarrowsToTheViewersHearts(t *testing.T) {
	opened, _ := newLibrary(t)
	favorites := library.NewFavorites(opened, clock.NewFakeAtEpoch())
	hearted := createPerson(t, opened, "Ada Voss")
	createPerson(t, opened, "Bo Lind")
	if err := favorites.Set(t.Context(), 1, library.FavoritePerson, hearted.ID, true); err != nil {
		t.Fatalf("Set: %v", err)
	}

	rows, err := library.NewPeople(opened).List(t.Context(), library.PeopleFilter{FavoriteUserID: 1}, library.PeopleCursor{}, 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != hearted.ID {
		t.Fatalf("user 1's favorite people = %+v, want only %d", rows, hearted.ID)
	}
}
