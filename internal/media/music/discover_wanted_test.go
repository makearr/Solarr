package music_test

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/migrations"
)

const discoveryCategoryID = 7

// fakeCategories is music.Categories over a fixed map, standing in for the
// settings.Cache snapshot a real process asks fresh every batch.
type fakeCategories map[int64]settings.Category

func (f fakeCategories) Category(id int64) (settings.Category, bool) {
	category, found := f[id]
	return category, found
}

func newConsumerWithDiscovery(t *testing.T, monitorDiscovered string) (music.Consumer, *store.Store, *fsx.Mem) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(testRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	desired := library.NewDesired(opened)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Unidentified: library.NewUnidentified(opened),
		Clock:        fake, Logger: slog.New(slog.DiscardHandler),
		Desired: &desired,
		Categories: fakeCategories{
			discoveryCategoryID: {ID: discoveryCategoryID, MonitorDiscovered: monitorDiscovered},
		},
		DefaultProfileID: 99,
	})
	return consumer, opened, fsys
}

func createWorkPart(t *testing.T, opened *store.Store, workID int64, mediaRef, sortKey, title string) {
	t.Helper()
	err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := librarydb.New(tx).CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID: workID, MediaRef: mediaRef, SortKey: sortKey,
			Title: sql.NullString{String: title, Valid: true}, Monitored: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("createWorkPart: %v", err)
	}
}

func getWorkByExternalID(t *testing.T, opened *store.Store, provider, externalID string) librarydb.Work {
	t.Helper()
	work, err := librarydb.New(opened.Read()).GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: provider, ExternalID: externalID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	return work
}

func TestDiscoverWanted_CreatesAMonitoredAlbumItemForANewlyIdentifiedWork(t *testing.T) {
	consumer, opened, fsys := newConsumerWithDiscovery(t, settings.MonitorDiscoveredMonitored)
	content := []byte("katupoken - ki pecezinu - 01 airbag")
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Katupoken/KI Pecezinu/01 Sesoge.flac", int64(len(content)), mediatool.Tags{
		"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID, "tracknumber": "1",
	})}

	if err := consumer.Observe(t.Context(), root, discoveryCategoryID, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	work := getWorkByExternalID(t, opened, "musicbrainz:release-group", releaseGroupMBID)
	queries := librarydb.New(opened.Read())
	items, err := queries.ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForWork: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want exactly one desired item, got %d", len(items))
	}
	item := items[0]
	if item.SatisfactionKind != "SET" || item.CategoryID != discoveryCategoryID || item.ProfileID != 99 {
		t.Errorf("unexpected item: %+v", item)
	}
	if item.Monitored != 1 {
		t.Error("want the item monitored under the 'monitored' policy")
	}
	// No tracklist exists yet (EnrichJob has not run), so required_count must
	// stay unknown rather than a guessed zero.
	if item.RequiredCount.Valid {
		t.Errorf("want required_count unknown before any work_part exists, got %v", item.RequiredCount)
	}
}

func TestDiscoverWanted_NonePolicySkipsDiscoveryButStillIdentifies(t *testing.T) {
	consumer, opened, fsys := newConsumerWithDiscovery(t, settings.MonitorDiscoveredNone)
	content := []byte("katupoken - ki pecezinu - 01 airbag")
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Katupoken/KI Pecezinu/01 Sesoge.flac", int64(len(content)), mediatool.Tags{
		"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID,
	})}

	if err := consumer.Observe(t.Context(), root, discoveryCategoryID, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	work := getWorkByExternalID(t, opened, "musicbrainz:release-group", releaseGroupMBID)
	items, err := librarydb.New(opened.Read()).ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForWork: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("want no desired item under the 'none' policy, got %+v", items)
	}
}

// The real, end-to-end shape of section 19.6: a first sweep (before the
// tracklist is enriched) creates the album item with no alignment; once
// work_part rows exist, a later full sweep re-observing the same files
// backfills real per-track coverage -- exactly the ordering dependency
// discoverWanted's own doc comment describes.
func TestDiscoverWanted_ASecondSweepAfterEnrichmentBackfillsRealTrackCoverage(t *testing.T) {
	consumer, opened, fsys := newConsumerWithDiscovery(t, settings.MonitorDiscoveredMonitored)
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", []byte("track one, long enough"))
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/02 Zosidedu Bukerez.flac", []byte("track two, long enough"))

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	tags := func(track, title string) mediatool.Tags {
		return mediatool.Tags{
			"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID,
			"tracknumber": track, "title": title,
		}
	}
	batch := []scan.Observed{
		observedFile("Katupoken/KI Pecezinu/01 Sesoge.flac", 23, tags("1", "Sesoge")),
		observedFile("Katupoken/KI Pecezinu/02 Zosidedu Bukerez.flac", 23, tags("2", "Zosidedu Bukerez")),
	}

	if err := consumer.Observe(t.Context(), root, discoveryCategoryID, batch); err != nil {
		t.Fatalf("Observe (first sweep): %v", err)
	}

	work := getWorkByExternalID(t, opened, "musicbrainz:release-group", releaseGroupMBID)
	createWorkPart(t, opened, work.ID, "disc1:track1", "00010001", "Sesoge")
	createWorkPart(t, opened, work.ID, "disc1:track2", "00010002", "Zosidedu Bukerez")

	// A full sweep re-probes and re-observes every file regardless of the
	// watermark (scan.Job.Run's own doc comment), which is what makes this
	// second Observe call realistic rather than contrived.
	if err := consumer.Observe(t.Context(), root, discoveryCategoryID, batch); err != nil {
		t.Fatalf("Observe (second sweep): %v", err)
	}

	queries := librarydb.New(opened.Read())
	items, err := queries.ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForWork: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want exactly one album item, got %d", len(items))
	}
	item := items[0]
	if !item.RequiredCount.Valid || item.RequiredCount.Int64 != 2 {
		t.Errorf("want required_count 2 now that the tracklist exists, got %+v", item.RequiredCount)
	}
	if item.PresentCount != 2 {
		t.Errorf("want present_count 2 -- both real files aligned, got %d", item.PresentCount)
	}
	if item.State != "COMPLETE" {
		t.Errorf("want state COMPLETE, got %s", item.State)
	}

	files, err := queries.ListFilesForDesiredItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("ListFilesForDesiredItem: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want two desired_item_file rows, got %d", len(files))
	}
}
