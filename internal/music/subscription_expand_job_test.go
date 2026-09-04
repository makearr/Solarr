package music_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/metacache"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
	"github.com/makearr/MediaNexus/migrations"
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

func newSubscriptionExpandJob(t *testing.T, source musicbrainz.Source) (
	*music.SubscriptionExpandJob, *store.Store, *librarydb.Queries,
) {
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
		Categories:       fixedCategory{ID: 1, MediaType: "music"},
		Provider:         provider,
		DefaultProfileID: 99, Clock: fake, Logger: slog.New(slog.DiscardHandler),
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
		Person: library.PersonIdentity{Name: "Katupoken", SortName: "Katupoken"},
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
		Person: library.PersonIdentity{Name: "Unknown Artist", SortName: "Unknown Artist"},
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
