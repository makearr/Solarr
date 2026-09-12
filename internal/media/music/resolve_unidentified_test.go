package music_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/metadata/metacache"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/migrations"
)

func newResolveFixture(t *testing.T, source musicbrainz.Source) (
	*store.Store, library.Identity, library.Unidentified, musicbrainz.Provider, *clock.Fake,
) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	fake := clock.NewFakeAtEpoch()

	libraryStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
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
		Name: "metadata", Path: filepath.Join(directory, "metadata.db"), Logger: slog.New(slog.DiscardHandler),
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

	provider := musicbrainz.New(musicbrainz.Config{
		Sources: []musicbrainz.Source{source}, Cache: metacache.New(metadataStore),
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	return libraryStore, library.NewIdentity(libraryStore), library.NewUnidentified(libraryStore), provider, fake
}

// queueEntry registers a real media_object first (identification_queue's own
// foreign key requires one to already exist -- the same order
// music.Consumer's own scan-time write already follows) and returns its id.
func queueEntry(t *testing.T, opened *store.Store, unidentified library.Unidentified, rootFolderID int64, relPath, quickHash string) int64 {
	t.Helper()
	ids, err := library.NewPhysical(opened).RegisterBatch(t.Context(), rootFolderID, []library.PhysicalFile{
		{RelPath: relPath, SizeBytes: 1, QuickHash: quickHash, FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	mediaObjectID := ids[0].MediaObjectID

	err = unidentified.Record(t.Context(), []library.QueueEntry{{
		MediaObjectID: mediaObjectID, RootFolderID: rootFolderID, RelPath: relPath,
		Candidates: []model.Identification{{
			Strategy: "parse", Confidence: 0.4,
			ExternalID: &model.ExternalID{Provider: music.ProviderReleaseGroup, ExternalID: "rg-1", Confidence: 0.4},
		}},
		At: firstSeen.Unix(),
	}})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	return mediaObjectID
}

func TestResolveUnidentified_CreatesTheWorkLinksTheRootFolderAndClearsTheQueue(t *testing.T) {
	source := &fakeSource{rg: musicbrainz.ReleaseGroup{MBID: "rg-1", Title: "Zuroveguv Wants to Dance"}}
	opened, identity, unidentified, provider, fake := newResolveFixture(t, source)
	mediaObjectID := queueEntry(t, opened, unidentified, 7, "Artist/Album/01.flac", "h1")

	workID, err := music.ResolveUnidentified(t.Context(), fake, provider, identity, unidentified,
		mediaObjectID, music.ProviderReleaseGroup, "rg-1")
	if err != nil {
		t.Fatalf("ResolveUnidentified: %v", err)
	}
	if workID == 0 {
		t.Fatal("workID = 0, want a real id")
	}

	work := library.NewWorks(opened)
	detail, err := work.Detail(t.Context(), workID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if detail.Work.CanonicalTitle != "Zuroveguv Wants to Dance" {
		t.Errorf("CanonicalTitle = %q, want the real MusicBrainz title", detail.Work.CanonicalTitle)
	}
	if len(detail.RootFolderIDs) != 1 || detail.RootFolderIDs[0] != 7 {
		t.Errorf("RootFolderIDs = %v, want [7] -- the queued file's own root folder", detail.RootFolderIDs)
	}

	if _, err := unidentified.Get(t.Context(), mediaObjectID); err == nil {
		t.Error("queue entry still present after a successful resolve")
	}
}

// Resolving the same candidate twice (a double click, or a retry after a
// network blip left the client unsure) must not create a second work --
// ResolveWorks' own find-or-create idempotency already guarantees this, this
// test is what proves the manual-resolve path does not defeat it.
func TestResolveUnidentified_IsIdempotentOnTheSameExternalID(t *testing.T) {
	source := &fakeSource{rg: musicbrainz.ReleaseGroup{MBID: "rg-1", Title: "Zuroveguv Wants to Dance"}}
	opened, identity, unidentified, provider, fake := newResolveFixture(t, source)
	firstID := queueEntry(t, opened, unidentified, 7, "Artist/Album/01.flac", "h1")
	secondID := queueEntry(t, opened, unidentified, 7, "Artist/Album/02.flac", "h2")

	first, err := music.ResolveUnidentified(t.Context(), fake, provider, identity, unidentified,
		firstID, music.ProviderReleaseGroup, "rg-1")
	if err != nil {
		t.Fatalf("ResolveUnidentified (first): %v", err)
	}
	second, err := music.ResolveUnidentified(t.Context(), fake, provider, identity, unidentified,
		secondID, music.ProviderReleaseGroup, "rg-1")
	if err != nil {
		t.Fatalf("ResolveUnidentified (second): %v", err)
	}
	if first != second {
		t.Errorf("work ids = %d and %d, want the same work for the same external id", first, second)
	}
}

func TestResolveUnidentified_UnsupportedProviderIsRefused(t *testing.T) {
	source := &fakeSource{}
	opened, identity, unidentified, provider, fake := newResolveFixture(t, source)
	mediaObjectID := queueEntry(t, opened, unidentified, 7, "Artist/Album/01.flac", "h1")

	_, err := music.ResolveUnidentified(t.Context(), fake, provider, identity, unidentified,
		mediaObjectID, "some-other-provider", "x")
	if !errors.Is(err, music.ErrUnsupportedProvider) {
		t.Errorf("err = %v, want ErrUnsupportedProvider", err)
	}
}

func TestResolveUnidentified_MusicBrainzFailureLeavesTheQueueEntryInPlace(t *testing.T) {
	source := &fakeSource{err: errors.New("musicbrainz unavailable")}
	opened, identity, unidentified, provider, fake := newResolveFixture(t, source)
	mediaObjectID := queueEntry(t, opened, unidentified, 7, "Artist/Album/01.flac", "h1")

	if _, err := music.ResolveUnidentified(t.Context(), fake, provider, identity, unidentified,
		mediaObjectID, music.ProviderReleaseGroup, "rg-1"); err == nil {
		t.Fatal("want an error when the provider fails")
	}
	if _, err := unidentified.Get(t.Context(), mediaObjectID); err != nil {
		t.Errorf("Get: %v, want the queue entry still present after a failed resolve", err)
	}
}
