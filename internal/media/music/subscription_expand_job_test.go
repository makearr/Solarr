package music_test

import (
	"context"
	"encoding/json"
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
	"github.com/makearr/Solarr/internal/jobs"
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

func newSubscriptionExpandJob(t *testing.T, source musicbrainz.Source) (
	*music.SubscriptionExpandJob, *store.Store, *librarydb.Queries,
) {
	t.Helper()
	return newSubscriptionExpandJobIn(t, source, fixedCategory{ID: 1, MediaType: "music"})
}

func newSubscriptionExpandJobIn(t *testing.T, source musicbrainz.Source, categories music.CategoryFinder) (
	*music.SubscriptionExpandJob, *store.Store, *librarydb.Queries,
) {
	t.Helper()
	return newSubscriptionExpandJobSearching(t, source, categories, nil)
}

func newSubscriptionExpandJobSearching(
	t *testing.T, source musicbrainz.Source, categories music.CategoryFinder, search music.ArtistSearch,
) (*music.SubscriptionExpandJob, *store.Store, *librarydb.Queries) {
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
	job := music.NewSubscriptionExpandJob(music.SubscriptionExpandConfig{
		Subscriptions: library.NewSubscriptions(libraryStore), People: library.NewPeople(libraryStore),
		Identity: library.NewIdentity(libraryStore), Desired: &desired,
		Categories:       categories,
		Provider:         provider,
		DefaultProfileID: 99, Clock: fake, Logger: slog.New(slog.DiscardHandler), Search: search,
	})
	return job, libraryStore, librarydb.New(libraryStore.Read())
}

// subscribedArtist creates a monitored person with a MusicBrainz artist id
// and an enabled music subscription for them, the same shape
// People.Subscribe produces (POST /people/{id}/subscribe).
func subscribedArtist(t *testing.T, opened *store.Store, artistMBID string) int64 {
	t.Helper()
	people := library.NewPeople(opened)
	personID, err := people.ResolvePerson(t.Context(), clock.NewFakeAtEpoch().Now(), library.PersonLink{
		Provider: music.ProviderArtist, ExternalID: artistMBID,
		Person: library.PersonIdentity{Name: "Katupoken"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if _, err := people.Subscribe(t.Context(), clock.NewFakeAtEpoch().Now(), personID, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return personID
}

func TestSubscriptionExpandJob_CreatesAnAlbumItemPerAdmittedReleaseGroup(t *testing.T) {
	source := &fakeDiscographySource{groups: []musicbrainz.ReleaseGroup{
		{MBID: "rg-album", Title: "KI Pecezinu"},
		{MBID: "rg-live", Title: "Live From a Tent", SecondaryTypes: []string{"Live"}},
	}}
	job, opened, queries := newSubscriptionExpandJob(t, source)
	subscribedArtist(t, opened, "katupoken-mbid")

	if err := job.Run(t.Context(), jobs.Job{Type: music.SubscriptionExpandJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-album",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID(rg-album): %v", err)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForWork: %v", err)
	}
	if len(items) != 1 || items[0].Monitored != 1 {
		t.Fatalf("want one monitored album item for KI Pecezinu, got %+v", items)
	}

	// The live release is excluded by the default filter -- no work at all,
	// not just an unmonitored item.
	if _, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-live",
	}); err == nil {
		t.Error("want no work created for the filtered-out live release")
	}
}

// Invariant 71, exercised through the whole job: unmonitoring an admitted
// album must survive a second expand run.
func TestSubscriptionExpandJob_NeverRecreatesAnItemTheUserUnmonitored(t *testing.T) {
	source := &fakeDiscographySource{groups: []musicbrainz.ReleaseGroup{{MBID: "rg-album", Title: "KI Pecezinu"}}}
	job, opened, queries := newSubscriptionExpandJob(t, source)
	subscribedArtist(t, opened, "katupoken-mbid")

	if err := job.Run(t.Context(), jobs.Job{Type: music.SubscriptionExpandJobType}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-album",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListDesiredItemsForWork: %v / %+v", err, items)
	}
	itemID := items[0].ID

	desired := library.NewDesired(opened)
	if err := desired.SetItemMonitored(t.Context(), itemID, false); err != nil {
		t.Fatalf("SetItemMonitored: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.SubscriptionExpandJobType}); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	items, err = queries.ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForWork after second run: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want still exactly one item, not a second one created, got %+v", items)
	}
	if items[0].Monitored != 0 {
		t.Error("the user's unmonitor must survive a second subscription expand run")
	}
}

func TestSubscriptionExpandJob_SkipsASubscriptionWithNoArtistExternalID(t *testing.T) {
	source := &fakeDiscographySource{groups: []musicbrainz.ReleaseGroup{{MBID: "rg-album", Title: "KI Pecezinu"}}}
	job, opened, _ := newSubscriptionExpandJob(t, source)

	// A person with no musicbrainz:artist external id at all.
	people := library.NewPeople(opened)
	personID, err := people.ResolvePerson(t.Context(), clock.NewFakeAtEpoch().Now(), library.PersonLink{
		Provider: "manual", ExternalID: "no-mbid",
		Person: library.PersonIdentity{Name: "Unknown Artist"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if _, err := people.Subscribe(t.Context(), clock.NewFakeAtEpoch().Now(), personID, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.SubscriptionExpandJobType}); err != nil {
		t.Fatalf("Run: %v, want the missing-external-id subscription isolated rather than failing the sweep", err)
	}
	if source.calls != 0 {
		t.Errorf("discography calls = %d, want 0 -- there is no artist id to browse with", source.calls)
	}
}

// twoCategories answers two music libraries, so a test can tell "the
// subscription's own library" apart from "the first music library".
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

// An artist added to the second music library expands into that one, not
// into whichever library of that media type comes first (2026-09-19).
func TestSubscriptionExpandJob_ExpandsIntoTheLibraryTheArtistWasAddedTo(t *testing.T) {
	source := &fakeDiscographySource{groups: []musicbrainz.ReleaseGroup{{MBID: "rg-album", Title: "KI Pecezinu"}}}
	job, opened, queries := newSubscriptionExpandJobIn(t, source, twoCategories{
		first:  settings.Category{ID: 1, MediaType: "music", Name: "Music"},
		second: settings.Category{ID: 2, MediaType: "music", Name: "Vinyl rips"},
	})

	people := library.NewPeople(opened)
	personID, err := people.ResolvePerson(t.Context(), clock.NewFakeAtEpoch().Now(), library.PersonLink{
		Provider: music.ProviderArtist, ExternalID: "katupoken-mbid",
		Person: library.PersonIdentity{Name: "Katupoken"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if _, err := people.Subscribe(t.Context(), clock.NewFakeAtEpoch().Now(), personID, 1,
		library.SubscriptionRequest{MediaTypes: []string{"music"}, CategoryID: 2}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := job.Run(t.Context(), jobs.Job{Type: music.SubscriptionExpandJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: "rg-album",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	items, err := queries.ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForWork: %v", err)
	}
	if len(items) != 1 || items[0].CategoryID != 2 {
		t.Fatalf("want one item in library 2, got %+v", items)
	}
}

// The add dialog's "Start search for missing albums" (D13): one artist's albums added at once, then
// searched -- the search only once the albums exist, and never for another subscription.
func TestSubscriptionExpandJob_AddsOneArtistsAlbumsThenSearchesThem(t *testing.T) {
	source := &fakeDiscographySource{groups: []musicbrainz.ReleaseGroup{{MBID: "rg-album", Title: "KI Pecezinu"}}}
	var searched [][2]int64
	var queries *librarydb.Queries
	job, opened, queries := newSubscriptionExpandJobSearching(t, source, fixedCategory{ID: 1, MediaType: "music"},
		func(ctx context.Context, categoryID, personID int64) error {
			if _, err := queries.GetWorkByExternalID(ctx, librarydb.GetWorkByExternalIDParams{
				Provider: "musicbrainz:release-group", ExternalID: "rg-album",
			}); err != nil {
				t.Errorf("searched before the album was added: %v", err)
			}
			searched = append(searched, [2]int64{categoryID, personID})
			return nil
		})
	personID := subscribedArtist(t, opened, "katupoken-mbid")
	subscriptions, err := library.NewSubscriptions(opened).ListEnabled(t.Context())
	if err != nil || len(subscriptions) != 1 {
		t.Fatalf("ListEnabled = %v, %v", subscriptions, err)
	}

	for _, search := range []bool{false, true} {
		payload, _ := json.Marshal(music.SubscriptionExpandPayload{SubscriptionID: subscriptions[0].ID, Search: search})
		if err := job.Run(t.Context(), jobs.Job{Type: music.SubscriptionExpandJobType, Payload: payload}); err != nil {
			t.Fatalf("Run(search=%v): %v", search, err)
		}
	}
	if want := [][2]int64{{subscriptions[0].CategoryID, personID}}; !slices.Equal(searched, want) {
		t.Errorf("searched %v, want once for the artist, and only when asked", searched)
	}
}
