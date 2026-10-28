package book

import (
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func openFollowLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, directory, owner); err != nil {
		t.Fatal(err)
	}
	return opened
}

func TestFollowingAnAuthorOffersTheirBooksFromTheFollowsYearOrEveryOneWithTheBackCatalogue(t *testing.T) {
	opened := openFollowLibrary(t)
	now := clock.NewFakeAtEpoch().Now()
	author, err := library.NewPeople(opened).ResolvePerson(t.Context(), now, library.PersonLink{
		Provider: "openlibrary:author", ExternalID: "OL2001A", Person: library.PersonIdentity{Name: "Mara Quell"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewSeries(opened).Upsert(t.Context(), now, library.SeriesWrite{
		Work: library.WorkIdentity{MediaType: "book", CanonicalTitle: "The Lantern Keeper", SortTitle: "lantern keeper"},
		IDs:  []library.ExternalIDWrite{{Provider: ProviderOpenLibrary, ExternalID: "OL1001W"}},
	}); err != nil {
		t.Fatal(err)
	}
	following := Following{
		Authors: AuthorWorks{Format: Ebook, People: library.NewPeople(opened), Reader: library.NewSeriesReader(opened), Source: &fakeOpenLibrary{}},
		Catalog: Catalog{Service: Service{Format: Ebook, Series: library.NewSeries(opened)}},
	}
	follow := media.Follow{SubjectType: "person", SubjectID: author, CategoryID: 5, BackCatalogue: true}
	got, err := following.FollowCandidates(t.Context(), follow)
	if err != nil {
		t.Fatal(err)
	}
	want := []media.FollowCandidate{
		{Provider: ProviderOpenLibrary, ExternalID: "OL1002W", Title: "The Glass Orchard", Year: 1991, PosterURL: "https://covers.openlibrary.org/b/id/7-L.jpg", Role: RoleAuthor},
		{Provider: ProviderOpenLibrary, ExternalID: "OL1001W", Title: "The Lantern Keeper", Year: 1987, Role: RoleAuthor, Held: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("back catalogue = %+v\nwant %+v", got, want)
	}

	follow.BackCatalogue, follow.CreatedAt = false, time.Date(1992, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	if got, _ := following.FollowCandidates(t.Context(), follow); len(got) != 0 {
		t.Errorf("new books of a 1992 follow = %+v, want none: both came out before", got)
	}
	follow.CreatedAt = time.Date(1991, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	if got, _ := following.FollowCandidates(t.Context(), follow); len(got) != 1 || got[0].ExternalID != "OL1002W" {
		t.Errorf("new books from 1991 = %+v, want The Glass Orchard alone", got)
	}
	follow.Roles = []string{RoleTranslator}
	if got, _ := following.FollowCandidates(t.Context(), follow); len(got) != 0 {
		t.Errorf("a translator's follow = %+v, want none: Open Library lists books by author", got)
	}
}
