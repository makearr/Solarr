package orglogos_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/orglogos"
	"github.com/makearr/Solarr/migrations"
)

type fakeSource struct{ asked [][]string }

func (f *fakeSource) OrganisationLogos(_ context.Context, names []string) (map[string]string, error) {
	f.asked = append(f.asked, names)
	return map[string]string{"Lantern House": "https://commons.wikimedia.org/wiki/Special:FilePath/L.svg?width=500"}, nil
}

func openLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler)})
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

func TestAnOrganisationIsAskedOnceByNameAndOneAskedByIDIsLeftAlone(t *testing.T) {
	opened := openLibrary(t)
	organisations := library.NewOrganisations(opened)
	now := clock.NewFakeAtEpoch().Now()
	works, err := library.NewIdentity(opened).ResolveWorks(t.Context(), now, []library.WorkLink{
		{Provider: "openlibrary", ExternalID: "a", Confidence: 1, Work: library.WorkIdentity{MediaType: "book", CanonicalTitle: "A", SortTitle: "a"}},
		{Provider: "openlibrary", ExternalID: "b", Confidence: 1, Work: library.WorkIdentity{MediaType: "book", CanonicalTitle: "B", SortTitle: "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher, _, err := organisations.SetWorkOrganisation(t.Context(), now, works[0], "publisher", "openlibrary", &library.OrganisationLink{Kind: "publisher", Name: "Lantern House"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := organisations.SetWorkOrganisation(t.Context(), now, works[1], "label", "musicbrainz",
		&library.OrganisationLink{Kind: "label", Name: "Label B", Provider: "musicbrainz:label", ExternalID: "label-b"}); err != nil {
		t.Fatal(err)
	}
	source := &fakeSource{}
	job := orglogos.Job{Organisations: organisations, Source: source, SkipProvider: "musicbrainz:label", Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler)}
	for range 2 {
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatal(err)
		}
	}
	if want := [][]string{{"Lantern House"}}; !reflect.DeepEqual(source.asked, want) {
		t.Errorf("asked = %v, want %v (the label with an id is left to its own job, nothing is asked twice)", source.asked, want)
	}
	profile, err := organisations.Get(t.Context(), publisher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://commons.wikimedia.org/wiki/Special:FilePath/L.svg?width=500"; profile.LogoURL != want {
		t.Errorf("logo = %q, want %q", profile.LogoURL, want)
	}
}
