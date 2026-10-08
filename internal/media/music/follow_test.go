package music_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// fakeDiscographySource answers one canned discography per test.
type fakeDiscographySource struct {
	groups []musicbrainz.ReleaseGroup
	err    error
	calls  int
}

func (f *fakeDiscographySource) Name() string { return "fake" }
func (f *fakeDiscographySource) Album(context.Context, string, string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	return musicbrainz.ReleaseGroup{}, musicbrainz.Release{}, errors.New("not used")
}
func (f *fakeDiscographySource) ArtistReleaseGroups(context.Context, string) ([]musicbrainz.ReleaseGroup, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.groups, nil
}

// fixedCategory is CategoryFinder over one fixed category, standing in for
// the settings.Cache snapshot a real process asks fresh every run.
type fixedCategory settings.Category

func (f fixedCategory) CategoryForMediaType(mediaType string) (settings.Category, bool) {
	if mediaType != "music" {
		return settings.Category{}, false
	}
	return settings.Category(f), true
}

func (f fixedCategory) Category(id int64) (settings.Category, bool) {
	if id != f.ID {
		return settings.Category{}, false
	}
	return settings.Category(f), true
}

// newFollowing builds music's follow over a migrated library and a provider answering from source.
func newFollowing(t *testing.T, source musicbrainz.Source, categories music.CategoryFinder) (music.Following, *store.Store) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	fake := clock.NewFakeAtEpoch()

	libraryStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open library: %v", err)
	}
	t.Cleanup(func() { _ = libraryStore.Close() })
	libraryOwner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations.Library: %v", err)
	}
	if err := store.Migrate(t.Context(), libraryStore, backups, libraryOwner); err != nil {
		t.Fatalf("Migrate library: %v", err)
	}

	metadataStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "metadata", Path: filepath.Join(directory, "metadata.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open metadata: %v", err)
	}
	t.Cleanup(func() { _ = metadataStore.Close() })
	metadataOwner, err := migrations.Metadata()
	if err != nil {
		t.Fatalf("migrations.Metadata: %v", err)
	}
	if err := store.Migrate(t.Context(), metadataStore, backups, metadataOwner); err != nil {
		t.Fatalf("Migrate metadata: %v", err)
	}

	desired := library.NewDesired(libraryStore)
	provider := musicbrainz.New(musicbrainz.Config{
		Sources: []musicbrainz.Source{source}, Cache: metacache.New(metadataStore),
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	return music.Following{
		People: library.NewPeople(libraryStore), Identity: library.NewIdentity(libraryStore), Desired: &desired,
		Categories: categories, Discography: provider, DefaultProfileID: 99, Clock: fake,
	}, libraryStore
}

// artistWithID creates a person with a MusicBrainz artist id.
func artistWithID(t *testing.T, opened *store.Store, artistMBID string) int64 {
	t.Helper()
	personID, err := library.NewPeople(opened).ResolvePerson(t.Context(), clock.NewFakeAtEpoch().Now(), library.PersonLink{
		Provider: music.ProviderArtist, ExternalID: artistMBID, Person: library.PersonIdentity{Name: "Katupoken"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	return personID
}

func candidateIDs(candidates []media.FollowCandidate) []string {
	var ids []string
	for _, candidate := range candidates {
		ids = append(ids, candidate.ExternalID)
	}
	return ids
}

// The follow admits by its options, else the library's release types, newest first; live releases stay out.
func TestFollowing_AdmitsByTheOptionsElseTheLibrarysTypes(t *testing.T) {
	source := &fakeDiscographySource{groups: []musicbrainz.ReleaseGroup{
		{MBID: "rg-old", Title: "First", PrimaryType: "Album", FirstReleaseDate: "1999"},
		{MBID: "rg-new", Title: "Latest", PrimaryType: "Album", FirstReleaseDate: "2021-05-01"},
		{MBID: "rg-ep", Title: "Short", PrimaryType: "EP", FirstReleaseDate: "2010"},
		{MBID: "rg-live", Title: "Live From a Tent", PrimaryType: "Album", SecondaryTypes: []string{"Live"}},
	}}
	following, opened := newFollowing(t, source, fixedCategory{ID: 1, MediaType: "music", MonitorReleaseTypes: []string{"Album"}})
	artist := artistWithID(t, opened, "katupoken-mbid")

	candidates, err := following.FollowCandidates(t.Context(), media.Follow{SubjectType: "person", SubjectID: artist, CategoryID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateIDs(candidates); !slices.Equal(got, []string{"rg-new", "rg-old"}) {
		t.Errorf("by the library's types = %v, want the two albums, newest first", got)
	}
	candidates, err = following.FollowCandidates(t.Context(), media.Follow{
		SubjectType: "person", SubjectID: artist, CategoryID: 1, FiltersJSON: `{"include_primary_types":["EP"],"exclude_secondary_types":[]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := candidateIDs(candidates); !slices.Equal(got, []string{"rg-ep"}) {
		t.Errorf("by the follow's own types = %v, want the EP", got)
	}
}

// Adding makes a monitored album item in the follow's library; from then on the album is held, so a follow never
// brings back an album the user unmonitored (Invariant 71).
func TestFollowing_AddsIntoTheFollowsLibraryAndThenHoldsIt(t *testing.T) {
	source := &fakeDiscographySource{groups: []musicbrainz.ReleaseGroup{{MBID: "rg-album", Title: "KI Pecezinu", PrimaryType: "Album"}}}
	categories := twoCategories{first: settings.Category{ID: 1, MediaType: "music"}, second: settings.Category{ID: 2, MediaType: "music"}}
	following, opened := newFollowing(t, source, categories)
	artist := artistWithID(t, opened, "katupoken-mbid")
	followed := media.Follow{SubjectType: "person", SubjectID: artist, CategoryID: 2}

	candidates, err := following.FollowCandidates(t.Context(), followed)
	if err != nil || len(candidates) != 1 || candidates[0].Held {
		t.Fatalf("candidates = %+v, %v; want the album, not yet held", candidates, err)
	}
	if err := following.AddFollowed(t.Context(), followed, candidates[0]); err != nil {
		t.Fatal(err)
	}
	queries := librarydb.New(opened.Read())
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{Provider: "musicbrainz:release-group", ExternalID: "rg-album"})
	if err != nil {
		t.Fatal(err)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil || len(items) != 1 || items[0].Monitored != 1 || items[0].CategoryID != 2 || items[0].ProfileID != 99 {
		t.Fatalf("items = %+v, %v; want one monitored album item in library 2 with the default profile", items, err)
	}
	candidates, err = following.FollowCandidates(t.Context(), followed)
	if err != nil || len(candidates) != 1 || !candidates[0].Held {
		t.Errorf("after adding, candidates = %+v, %v; want the album held", candidates, err)
	}
	candidates, err = following.FollowCandidates(t.Context(), media.Follow{SubjectType: "person", SubjectID: artist, CategoryID: 1})
	if err != nil || len(candidates) != 1 || candidates[0].Held {
		t.Errorf("in the other library, candidates = %+v, %v; want the album, not held there", candidates, err)
	}
}

func TestFollowing_AnArtistWithoutAnIDCannotBeFollowedYet(t *testing.T) {
	following, opened := newFollowing(t, &fakeDiscographySource{}, fixedCategory{ID: 1, MediaType: "music"})
	person, err := library.NewPeople(opened).ResolveFolderArtist(t.Context(), clock.NewFakeAtEpoch().Now(), "local", "No Id Yet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := following.FollowCandidates(t.Context(), media.Follow{SubjectType: "person", SubjectID: person, CategoryID: 1}); !errors.Is(err, media.ErrNoFollowSource) {
		t.Errorf("err = %v, want media.ErrNoFollowSource", err)
	}
}

type twoCategories struct{ first, second settings.Category }

func (c twoCategories) CategoryForMediaType(mediaType string) (settings.Category, bool) {
	if mediaType != "music" {
		return settings.Category{}, false
	}
	return c.first, true
}

func (c twoCategories) Category(id int64) (settings.Category, bool) {
	switch id {
	case c.first.ID:
		return c.first, true
	case c.second.ID:
		return c.second, true
	default:
		return settings.Category{}, false
	}
}
