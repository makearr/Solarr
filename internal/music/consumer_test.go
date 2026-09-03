package music_test

import (
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/scan"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
	"github.com/makearr/MediaNexus/migrations"
)

const testRoot = "/media/music"

var observedAt = time.Unix(1_700_000_000, 0).UTC()

// newConsumer builds a Consumer over a real (temp file) library.db, the way
// internal/library's own tests do, plus an in-memory filesystem the consumer reads
// the quick hash from.
func newConsumer(t *testing.T) (music.Consumer, *store.Store, *fsx.Mem) {
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

	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	return consumer, opened, fsys
}

func writeMusicFile(t *testing.T, fsys *fsx.Mem, relPath string, content []byte) {
	t.Helper()
	absolute := path.Join(testRoot, relPath)
	if err := fsys.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	file, err := fsys.Create(absolute)
	if err != nil {
		t.Fatalf("Create(%q): %v", absolute, err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatalf("Write(%q): %v", absolute, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q): %v", absolute, err)
	}
}

// observedFile builds a scan.Observed the way scan.Job's batch() does: Probe.Path is
// the absolute path the probe was asked for, which is what BatchTags keys on.
func observedFile(relPath string, size int64, tags mediatool.Tags) scan.Observed {
	return scan.Observed{
		File:  scan.Found{RelPath: relPath, SizeBytes: size, ModTime: observedAt},
		Probe: mediatool.Probe{Path: path.Join(testRoot, relPath), Tags: tags},
	}
}

func TestObserve_IdentifiedFileRegistersPhysicallyAndCreatesAWork(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	content := []byte("katupoken - ki pecezinu - 01 airbag")
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Katupoken/KI Pecezinu/01 Sesoge.flac", int64(len(content)), mediatool.Tags{
		"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID,
	})}

	if err := consumer.Observe(t.Context(), root, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	path, err := queries.GetPathByLocation(t.Context(), librarydb.GetPathByLocationParams{
		RootFolderID: 1, RelPath: "Katupoken/KI Pecezinu/01 Sesoge.flac",
	})
	if err != nil {
		t.Fatalf("GetPathByLocation: %v", err)
	}
	object, err := queries.GetMediaObject(t.Context(), path.MediaObjectID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	if object.SizeBytes != int64(len(content)) {
		t.Errorf("SizeBytes = %d, want %d", object.SizeBytes, len(content))
	}

	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if work.MediaType != "music" || work.CanonicalTitle != "KI Pecezinu" {
		t.Errorf("work = %+v, want music/KI Pecezinu", work)
	}

	// The temporary bridge category filtering needs (library.WorkFilter's
	// RootFolderIDs) until desired_item exists for music: the root folder this
	// file was scanned from must be recorded against the work it resolved to.
	roots, err := library.NewIdentity(opened).RootFoldersForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("RootFoldersForWork: %v", err)
	}
	if len(roots) != 1 || roots[0] != 1 {
		t.Errorf("RootFoldersForWork = %v, want [1]", roots)
	}
}

func TestObserve_ReleaseMBIDCreatesAPlaceholderEdition(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	content := []byte("katupoken - ki pecezinu - 01 airbag")
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Katupoken/KI Pecezinu/01 Sesoge.flac", int64(len(content)), mediatool.Tags{
		"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID,
		"musicbrainz_albumid": releaseMBID,
	})}

	if err := consumer.Observe(t.Context(), root, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}

	edition, err := queries.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: "musicbrainz:release", ExternalID: releaseMBID,
	})
	if err != nil {
		t.Fatalf("GetEditionByExternalID: %v", err)
	}
	if edition.WorkID != work.ID {
		t.Errorf("edition.WorkID = %d, want %d", edition.WorkID, work.ID)
	}
	if edition.Kind != "unknown" || edition.Label != releaseMBID {
		t.Errorf("edition = %+v, want kind=unknown label=%s", edition, releaseMBID)
	}
}

// No MUSICBRAINZ_ALBUMID tag means no edition row -- the release-group id alone
// (or, as here, no id at all beyond the group) says nothing about which pressing.
func TestObserve_NoReleaseMBIDMeansNoEdition(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	content := []byte("katupoken - ki pecezinu - 01 airbag, no release id")
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Katupoken/KI Pecezinu/01 Sesoge.flac", int64(len(content)), mediatool.Tags{
		"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID,
	})}

	if err := consumer.Observe(t.Context(), root, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	// Checked by listing every edition under the work, not by asking for one
	// specific external id: a mutation that built an edition keyed on the empty
	// string instead of skipping it would still pass a check scoped to releaseMBID.
	editions, err := queries.ListEditions(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListEditions: %v", err)
	}
	if len(editions) != 0 {
		t.Errorf("editions = %+v, want none -- no release MBID was present", editions)
	}
}

func TestObserve_TwoTracksOfOneAlbumShareOneWork(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", []byte("track one, long enough"))
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu/02 Zosidedu Bukerez.flac", []byte("track two, long enough"))

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{
		observedFile("Katupoken/KI Pecezinu/01 Sesoge.flac", 23, mediatool.Tags{
			"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID,
		}),
		observedFile("Katupoken/KI Pecezinu/02 Zosidedu Bukerez.flac", 23, mediatool.Tags{
			"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID,
		}),
	}

	if err := consumer.Observe(t.Context(), root, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	works, err := library.NewWorks(opened).List(t.Context(), library.WorkFilter{}, library.WorkCursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(works) != 1 {
		t.Errorf("work count = %d, want 1 (both tracks share one release group)", len(works))
	}
}

func TestObserve_NoMBIDStillRegistersThePhysicalFileButNoWork(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	content := []byte("an unidentified file, tagged with nothing useful")
	writeMusicFile(t, fsys, "Unsorted/track.mp3", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Unsorted/track.mp3", int64(len(content)), mediatool.Tags{})}

	if err := consumer.Observe(t.Context(), root, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	if _, err := queries.GetPathByLocation(t.Context(), librarydb.GetPathByLocationParams{
		RootFolderID: 1, RelPath: "Unsorted/track.mp3",
	}); err != nil {
		t.Fatalf("GetPathByLocation: %v", err)
	}

	works, err := library.NewWorks(opened).List(t.Context(), library.WorkFilter{}, library.WorkCursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(works) != 0 {
		t.Errorf("work count = %d, want 0 -- nothing here carries an identity", len(works))
	}
}

// A file the probe already marked unreadable must be skipped entirely: quick-hashing
// it would just fail a second time for the same reason.
func TestObserve_UnreadableProbeIsSkippedEntirely(t *testing.T) {
	consumer, opened, _ := newConsumer(t)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{{
		File:  scan.Found{RelPath: "Broken/track.flac", SizeBytes: 10, ModTime: observedAt},
		Probe: mediatool.Probe{Err: os.ErrPermission},
	}}

	if err := consumer.Observe(t.Context(), root, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	if _, err := queries.GetPathByLocation(t.Context(), librarydb.GetPathByLocationParams{
		RootFolderID: 1, RelPath: "Broken/track.flac",
	}); err == nil {
		t.Error("an unreadable file was registered anyway")
	}
}

// No album tag falls back to the containing directory, the way a Lidarr-managed
// library already names an album folder.
func TestObserve_MissingAlbumTagFallsBackToTheDirectoryName(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	content := []byte("a file with only a release group id, no album tag")
	writeMusicFile(t, fsys, "Katupoken/Zor A/01.flac", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Katupoken/Zor A/01.flac", int64(len(content)), mediatool.Tags{
		"musicbrainz_releasegroupid": releaseGroupMBID,
	})}

	if err := consumer.Observe(t.Context(), root, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if work.CanonicalTitle != "Zor A" {
		t.Errorf("CanonicalTitle = %q, want the directory name %q", work.CanonicalTitle, "Zor A")
	}
}
