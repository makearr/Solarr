package music_test

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/paginate"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/migrations"
)

const testRoot = "/media/music"

var observedAt = time.Unix(1_700_000_000, 0).UTC()

// newConsumer builds a Consumer over a real (temp file) library.db, the way
// internal/db/library's own tests do, plus an in-memory filesystem the consumer reads
// the quick hash from.
func newConsumer(t *testing.T) (music.Consumer, *store.Store, *fsx.Mem) {
	t.Helper()
	opened, fsys, fake := newTestLibrary(t)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Unidentified: library.NewUnidentified(opened),
		Clock:        fake, Logger: slog.New(slog.DiscardHandler),
	})
	return consumer, opened, fsys
}

// newTestLibrary is a migrated temp library.db and an in-memory filesystem holding testRoot.
func newTestLibrary(t *testing.T) (*store.Store, *fsx.Mem, *clock.Fake) {
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
	return opened, fsys, fake
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

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
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

// ADR-39/todo 4.2: the claimed half is derived from the release folder's own
// name at scan time, not from the file's tags or bytes -- this is the
// producer RecordClaimed exists for, proven end to end through Observe
// rather than just at the parse.Parse/RecordClaimed unit level.
func TestObserve_RecordsClaimedAttributesFromTheReleaseFolderName(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	content := []byte("katupoken - ki pecezinu - 01 airbag")
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu (1997) [FLAC]/01 Sesoge.flac", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile(
		"Katupoken/KI Pecezinu (1997) [FLAC]/01 Sesoge.flac", int64(len(content)), mediatool.Tags{},
	)}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	libraryPath, err := queries.GetPathByLocation(t.Context(), librarydb.GetPathByLocationParams{
		RootFolderID: 1, RelPath: "Katupoken/KI Pecezinu (1997) [FLAC]/01 Sesoge.flac",
	})
	if err != nil {
		t.Fatalf("GetPathByLocation: %v", err)
	}

	claim, found, err := library.NewPhysical(opened).ClaimedAttributesFor(t.Context(), libraryPath.MediaObjectID)
	if err != nil {
		t.Fatalf("ClaimedAttributesFor: %v", err)
	}
	if !found {
		t.Fatal("ClaimedAttributesFor found = false, want a claim recorded from the release folder name")
	}
	if claim.AudioCodec != "flac" || !claim.Lossless {
		t.Errorf("claim = %+v, want AudioCodec flac, Lossless true", claim)
	}
}

// Two files in the same release folder share one parse of that folder's
// name -- proven indirectly by both files' media_object ending up with the
// same claimed_attributes_id, which only interning the same claim twice
// produces.
func TestObserve_ClaimedAttributesShareOneInternedRowPerReleaseFolder(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	track1 := []byte("track one")
	track2 := []byte("track two")
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu (1997) [FLAC]/01 Sesoge.flac", track1)
	writeMusicFile(t, fsys, "Katupoken/KI Pecezinu (1997) [FLAC]/02 Zosidedu Bukerez.flac", track2)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{
		observedFile("Katupoken/KI Pecezinu (1997) [FLAC]/01 Sesoge.flac", int64(len(track1)), mediatool.Tags{}),
		observedFile("Katupoken/KI Pecezinu (1997) [FLAC]/02 Zosidedu Bukerez.flac", int64(len(track2)), mediatool.Tags{}),
	}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	first, err := queries.GetPathByLocation(t.Context(), librarydb.GetPathByLocationParams{
		RootFolderID: 1, RelPath: "Katupoken/KI Pecezinu (1997) [FLAC]/01 Sesoge.flac",
	})
	if err != nil {
		t.Fatalf("GetPathByLocation: %v", err)
	}
	second, err := queries.GetPathByLocation(t.Context(), librarydb.GetPathByLocationParams{
		RootFolderID: 1, RelPath: "Katupoken/KI Pecezinu (1997) [FLAC]/02 Zosidedu Bukerez.flac",
	})
	if err != nil {
		t.Fatalf("GetPathByLocation: %v", err)
	}

	firstObject, err := queries.GetMediaObject(t.Context(), first.MediaObjectID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	secondObject, err := queries.GetMediaObject(t.Context(), second.MediaObjectID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	if !firstObject.ClaimedAttributesID.Valid || !secondObject.ClaimedAttributesID.Valid {
		t.Fatalf("claimed_attributes_id not set: first=%+v second=%+v", firstObject, secondObject)
	}
	if firstObject.ClaimedAttributesID.Int64 != secondObject.ClaimedAttributesID.Int64 {
		t.Errorf("claimed_attributes_id = %d and %d, want the same interned row",
			firstObject.ClaimedAttributesID.Int64, secondObject.ClaimedAttributesID.Int64)
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

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
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

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
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

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
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

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
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

// With a Searcher configured, a file with real ARTIST/ALBUM tags but no MBID at
// all -- most scene-released music -- still resolves to a work, through
// SearchIdentifier rather than MBIDIdentifier.
func TestObserve_SearchIdentifierResolvesAFileWithNoMBIDWhenASearcherIsConfigured(t *testing.T) {
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
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
		Unidentified: library.NewUnidentified(opened), Search: &fakeSearcher{
			matches: []musicbrainz.ReleaseGroupMatch{
				{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: releaseGroupMBID, Title: "The Sezo Siluko"},
					ArtistCredit: "Vutu Doci"},
			},
		},
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})

	content := []byte("a scene-release FLAC, tagged but with no MusicBrainz ids")
	writeMusicFile(t, fsys, "Vutu Doci-The Sezo Siluko-WEB-FLAC-2018-TiMES/01 Moving.flac", content)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Vutu Doci-The Sezo Siluko-WEB-FLAC-2018-TiMES/01 Moving.flac",
		int64(len(content)), mediatool.Tags{"artist": "Vutu Doci", "album": "The Sezo Siluko", "date": "1978"})}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	if _, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	}); err != nil {
		t.Fatalf("GetWorkByExternalID: %v, want the search match resolved to a work", err)
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

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
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

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
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

// F29: a file the chain could not resolve at all is queued for manual
// identification, not silently dropped.
func TestObserve_UnresolvedFileIsQueuedForManualIdentification(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	content := []byte("an unidentified file, tagged with nothing useful")
	writeMusicFile(t, fsys, "Unsorted/track.mp3", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("Unsorted/track.mp3", int64(len(content)), mediatool.Tags{})}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	rows, err := library.NewUnidentified(opened).List(t.Context(), nil, paginate.Cursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("queue = %+v, want exactly one entry", rows)
	}
	if rows[0].RelPath != "Unsorted/track.mp3" || rows[0].RootFolderID != 1 {
		t.Errorf("queued entry = %+v, want Unsorted/track.mp3 under root 1", rows[0])
	}
	if len(rows[0].Candidates) != 0 {
		t.Errorf("Candidates = %+v, want none -- nothing in the file matched anything", rows[0].Candidates)
	}
}

// F29: a file that later resolves (a rescan after it was re-tagged, e.g.) must
// leave the queue -- it is not history, only what still needs a human.
func TestObserve_ResolvingOnASecondSweepClearsTheQueueEntry(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	content := []byte("first untagged, then tagged")
	writeMusicFile(t, fsys, "Unsorted/track.mp3", content)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	untagged := []scan.Observed{observedFile("Unsorted/track.mp3", int64(len(content)), mediatool.Tags{})}
	if err := consumer.Observe(t.Context(), root, 0, untagged); err != nil {
		t.Fatalf("Observe (untagged): %v", err)
	}

	tagged := []scan.Observed{observedFile("Unsorted/track.mp3", int64(len(content)), mediatool.Tags{
		"album": "KI Pecezinu", "musicbrainz_releasegroupid": releaseGroupMBID,
	})}
	if err := consumer.Observe(t.Context(), root, 0, tagged); err != nil {
		t.Fatalf("Observe (tagged): %v", err)
	}

	rows, err := library.NewUnidentified(opened).List(t.Context(), nil, paginate.Cursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("queue = %+v, want empty -- the second sweep resolved it", rows)
	}
}

// An untagged track beside tracks that identified to one album is that album's:
// the test library had 127 such files in the unidentified queue.
func TestObserve_AnUnidentifiedFileJoinsTheAlbumItsFolderSiblingsAgreeOn(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	tagged := []byte("kebilegev bibihi 01, tagged")
	untagged := []byte("kebilegev bibihi 07, untagged")
	writeMusicFile(t, fsys, "P!po/Kebilegev Bibihi/01.m4a", tagged)
	writeMusicFile(t, fsys, "P!po/Kebilegev Bibihi/07.m4a", untagged)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{
		observedFile("P!po/Kebilegev Bibihi/01.m4a", int64(len(tagged)), mediatool.Tags{
			"album": "Kebilegev Bibihi", "musicbrainz_releasegroupid": releaseGroupMBID,
		}),
		observedFile("P!po/Kebilegev Bibihi/07.m4a", int64(len(untagged)), mediatool.Tags{"album": "Kebilegev Bibihi"}),
	}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queue, err := library.NewUnidentified(opened).List(t.Context(), nil, paginate.Cursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queue) != 0 {
		t.Errorf("queue = %+v, want the untagged sibling identified, not queued", queue)
	}
	work, err := librarydb.New(opened.Read()).GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	locations, err := library.NewIdentity(opened).WorkFileLocations(t.Context(), []int64{work.ID})
	if err != nil {
		t.Fatalf("WorkFileLocations: %v", err)
	}
	if len(locations) != 2 {
		t.Errorf("work files = %+v, want both tracks under the one album", locations)
	}
}

// Siblings that disagree prove nothing about a file between them.
func TestObserve_SiblingsThatDisagreeLeaveAnUnidentifiedFileQueued(t *testing.T) {
	consumer, opened, fsys := newConsumer(t)
	first, second, untagged := []byte("album a track"), []byte("album b track"), []byte("untagged track")
	writeMusicFile(t, fsys, "Mixed/a.m4a", first)
	writeMusicFile(t, fsys, "Mixed/b.m4a", second)
	writeMusicFile(t, fsys, "Mixed/c.m4a", untagged)

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{
		observedFile("Mixed/a.m4a", int64(len(first)), mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}),
		observedFile("Mixed/b.m4a", int64(len(second)), mediatool.Tags{"musicbrainz_releasegroupid": "11111111-2222-3333-4444-555555555555"}),
		observedFile("Mixed/c.m4a", int64(len(untagged)), mediatool.Tags{}),
	}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queue, err := library.NewUnidentified(opened).List(t.Context(), nil, paginate.Cursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queue) != 1 || queue[0].RelPath != "Mixed/c.m4a" {
		t.Errorf("queue = %+v, want only the untagged file, left for manual review", queue)
	}
}

// newAlbumConsumer is a Consumer with album matching over lookup and per-file search over searcher.
func newAlbumConsumer(t *testing.T, lookup music.AlbumLookup, searcher music.Searcher) (music.Consumer, *store.Store, *fsx.Mem) {
	t.Helper()
	opened, fsys, fake := newTestLibrary(t)
	attempts := library.NewAttempts(opened)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Unidentified: library.NewUnidentified(opened), Attempts: &attempts, Albums: lookup, Search: searcher,
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	return consumer, opened, fsys
}

// untaggedTraumaBatch is the folder with every MBID stripped, the case album matching exists for.
func untaggedTraumaBatch(t *testing.T, fsys *fsx.Mem) []scan.Observed {
	t.Helper()
	batch := make([]scan.Observed, len(traumaTracks))
	for i, track := range traumaTracks {
		relPath := fmt.Sprintf("P!po/Kebilegev Bibihi (2017)/P!po - %02d - %s.m4a", i+1, track.title)
		content := []byte("untagged " + track.title)
		writeMusicFile(t, fsys, relPath, content)
		batch[i] = observedFile(relPath, int64(len(content)), mediatool.Tags{
			"title": track.title, "album_artist": "P!po", "album": "Kebilegev Bibihi", "track": fmt.Sprint(i + 1),
		})
		batch[i].Probe.Measured.Duration = time.Duration(track.seconds) * time.Second
	}
	return batch
}

func traumaLookup(groups ...string) *fakeAlbumLookup {
	lookup := &fakeAlbumLookup{
		searches: map[string][]musicbrainz.ReleaseGroupMatch{},
		releases: map[string][]musicbrainz.Release{},
		// groups answers a release group recalled by id alone, which is how a
		// seed arrives -- from an identified sibling, or from a fingerprint.
		groups: map[string]musicbrainz.ReleaseGroup{},
	}
	for _, group := range groups {
		lookup.groups[group] = musicbrainz.ReleaseGroup{MBID: group, Title: "Kebilegev Bibihi", PrimaryType: "Album"}
		lookup.searches["P!po/Kebilegev Bibihi"] = append(lookup.searches["P!po/Kebilegev Bibihi"],
			musicbrainz.ReleaseGroupMatch{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: group, Title: "Kebilegev Bibihi", PrimaryType: "Album"}, ArtistCredit: "P!po"})
		lookup.releases[group] = []musicbrainz.Release{releaseOf("release-of-"+group, "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks)}
	}
	return lookup
}

func TestObserve_AnUntaggedFolderIsIdentifiedAsAnAlbumWithItsEdition(t *testing.T) {
	consumer, opened, fsys := newAlbumConsumer(t, traumaLookup(releaseGroupMBID), nil)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}

	if err := consumer.Observe(t.Context(), root, 0, untaggedTraumaBatch(t, fsys)); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: releaseGroupMBID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v, want the folder identified as the album", err)
	}
	if _, err := queries.GetEditionByExternalID(t.Context(), librarydb.GetEditionByExternalIDParams{
		Provider: music.ProviderRelease, ExternalID: "release-of-" + releaseGroupMBID,
	}); err != nil {
		t.Errorf("GetEditionByExternalID: %v, want the matched release as the edition", err)
	}
	locations, err := library.NewIdentity(opened).WorkFileLocations(t.Context(), []int64{work.ID})
	if err != nil || len(locations) != len(traumaTracks) {
		t.Errorf("work files = %d (err %v), want all %d files", len(locations), err, len(traumaTracks))
	}
	queue, err := library.NewUnidentified(opened).List(t.Context(), nil, paginate.Cursor{}, 50)
	if err != nil || len(queue) != 0 {
		t.Errorf("queue = %+v (err %v), want empty", queue, err)
	}
}

// Two albums fitting equally are put to the user, never settled by a weaker per-file guess.
func TestObserve_AnAmbiguousAlbumIsQueuedNotGuessedPerFile(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: releaseGroupMBID, Title: "Kebilegev Bibihi"}, ArtistCredit: "P!po"},
	}}
	other := "11111111-2222-3333-4444-555555555555"
	consumer, opened, fsys := newAlbumConsumer(t, traumaLookup(releaseGroupMBID, other), searcher)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}

	if err := consumer.Observe(t.Context(), root, 0, untaggedTraumaBatch(t, fsys)); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queue, err := library.NewUnidentified(opened).List(t.Context(), nil, paginate.Cursor{}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(queue) != len(traumaTracks) {
		t.Fatalf("queue holds %d files, want all %d", len(queue), len(traumaTracks))
	}
	var offered []string
	for _, candidate := range queue[0].Candidates {
		offered = append(offered, candidate.ExternalID)
	}
	slices.Sort(offered)
	want := []string{releaseGroupMBID, other}
	slices.Sort(want)
	if !slices.Equal(offered, want) {
		t.Errorf("candidates = %v, want both albums offered", offered)
	}
}

// An album nobody has catalogued still gets today's per-file search.
func TestObserve_NoAlbumCandidateFallsBackToPerFileSearch(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: releaseGroupMBID, Title: "Kebilegev Bibihi"}, ArtistCredit: "P!po"},
	}}
	consumer, opened, fsys := newAlbumConsumer(t, &fakeAlbumLookup{}, searcher)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}

	if err := consumer.Observe(t.Context(), root, 0, untaggedTraumaBatch(t, fsys)); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	if _, err := librarydb.New(opened.Read()).GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderReleaseGroup, ExternalID: releaseGroupMBID,
	}); err != nil {
		t.Fatalf("GetWorkByExternalID: %v, want the per-file search to have resolved it", err)
	}
}

// A manual decision must be able to show what every pass concluded and which albums it weighed.
func TestObserve_AnAmbiguousFolderRecordsEveryPassWithItsCandidates(t *testing.T) {
	other := "11111111-2222-3333-4444-555555555555"
	consumer, opened, fsys := newAlbumConsumer(t, traumaLookup(releaseGroupMBID, other), nil)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}

	if err := consumer.Observe(t.Context(), root, 0, untaggedTraumaBatch(t, fsys)); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queue, err := library.NewUnidentified(opened).List(t.Context(), nil, paginate.Cursor{}, 50)
	if err != nil || len(queue) == 0 {
		t.Fatalf("queue = %+v (err %v), want the folder queued", queue, err)
	}
	attempts, err := library.NewAttempts(opened).List(t.Context(), queue[0].MediaObjectID)
	if err != nil {
		t.Fatalf("List attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != music.AttemptQueued {
		t.Fatalf("attempts = %+v, want one queued run", attempts)
	}
	var passes []string
	for _, pass := range attempts[0].Passes {
		passes = append(passes, pass.Pass+":"+pass.Outcome)
	}
	if want := []string{"embedded_tags:none", "album_match:ambiguous"}; !slices.Equal(passes, want) {
		t.Errorf("passes = %v, want %v", passes, want)
	}
	album := attempts[0].Passes[1]
	if len(album.Candidates) != 2 {
		t.Fatalf("album candidates = %+v, want both rival albums", album.Candidates)
	}
	titled := slices.ContainsFunc(album.Candidates[0].Evidence, func(e library.AttemptEvidence) bool {
		return e.Kind == music.EvidenceTitle && e.Detail == "Kebilegev Bibihi"
	})
	if !titled {
		t.Errorf("evidence = %+v, want the album's title for a human to choose by", album.Candidates[0].Evidence)
	}
}

// Files their embedded MBIDs settle cost nothing to record.
func TestObserve_AFileItsEmbeddedTagsIdentifyRecordsNoAttempt(t *testing.T) {
	consumer, opened, fsys := newAlbumConsumer(t, &fakeAlbumLookup{}, nil)
	content := []byte("tagged")
	writeMusicFile(t, fsys, "P!po/Kebilegev Bibihi/01.m4a", content)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("P!po/Kebilegev Bibihi/01.m4a", int64(len(content)),
		mediatool.Tags{"album": "Kebilegev Bibihi", "musicbrainz_releasegroupid": releaseGroupMBID})}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "P!po/Kebilegev Bibihi/01.m4a", SizeBytes: int64(len(content)), QuickHash: mustQuickHash(t, fsys, "P!po/Kebilegev Bibihi/01.m4a", int64(len(content))), FirstSeenAt: observedAt},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	attempts, err := library.NewAttempts(opened).List(t.Context(), registered[0].MediaObjectID)
	if err != nil || len(attempts) != 0 {
		t.Errorf("attempts = %+v (err %v), want none", attempts, err)
	}
}

func mustQuickHash(t *testing.T, fsys *fsx.Mem, relPath string, size int64) string {
	t.Helper()
	hash, err := library.QuickHash(fsys, path.Join(testRoot, relPath), size)
	if err != nil {
		t.Fatalf("QuickHash: %v", err)
	}
	return hash
}

// newFingerprintConsumer adds ADR-29's fingerprint rung to the scan chain.
func newFingerprintConsumer(
	t *testing.T, prints *fakeFingerprinter, lookup *fakeLookup,
) (music.Consumer, *store.Store, *fsx.Mem) {
	t.Helper()
	opened, fsys, fake := newTestLibrary(t)
	attempts := library.NewAttempts(opened)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Unidentified: library.NewUnidentified(opened), Attempts: &attempts,
		Fingerprints: prints, AcoustID: lookup,
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	return consumer, opened, fsys
}

// untaggedFile is a file no earlier pass can name: no MBIDs, and no searcher configured.
func untaggedFile(t *testing.T, fsys *fsx.Mem) []scan.Observed {
	t.Helper()
	content := []byte("a rip with no tags worth the name")
	writeMusicFile(t, fsys, "unknown/track01.flac", content)
	return []scan.Observed{observedFile("unknown/track01.flac", int64(len(content)), mediatool.Tags{})}
}

func TestObserve_FingerprintIdentifiesAFileNoEarlierPassCould(t *testing.T) {
	prints := &fakeFingerprinter{print: mediatool.Fingerprint{Chromaprint: "AQADtE", Duration: 215 * time.Second}}
	lookup := &fakeLookup{enabled: true, matches: []acoustid.Match{
		{Score: 0.97, ReleaseGroupMBIDs: []string{releaseGroupMBID}},
	}}
	consumer, opened, fsys := newFingerprintConsumer(t, prints, lookup)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}

	if err := consumer.Observe(t.Context(), root, 0, untaggedFile(t, fsys)); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	if _, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	}); err != nil {
		t.Fatalf("GetWorkByExternalID: %v, want the fingerprint match resolved to a work", err)
	}
	if prints.calls != 1 || lookup.calls != 1 {
		t.Errorf("fingerprint calls = %d, lookup calls = %d, want one each", prints.calls, lookup.calls)
	}
	if passes := passesFor(t, opened, 1, "unknown/track01.flac", fsys); !slices.Contains(passes, "fingerprint:identified") {
		t.Errorf("passes = %v, want the fingerprint pass recorded as identified", passes)
	}
}

// A file skipped for want of a key is not a file AcoustID could not name, and the
// attempt record is what tells them apart once a key is entered (D12).
func TestObserve_NoAcoustIDKeyRecordsTheSkipRatherThanSilence(t *testing.T) {
	prints := &fakeFingerprinter{print: mediatool.Fingerprint{Chromaprint: "AQADtE", Duration: 215 * time.Second}}
	lookup := &fakeLookup{enabled: false}
	consumer, opened, fsys := newFingerprintConsumer(t, prints, lookup)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}

	if err := consumer.Observe(t.Context(), root, 0, untaggedFile(t, fsys)); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	if prints.calls != 0 {
		t.Errorf("fingerprint calls = %d, want none -- decoding audio for a lookup that cannot happen", prints.calls)
	}
	passes := passesFor(t, opened, 1, "unknown/track01.flac", fsys)
	if !slices.Contains(passes, "fingerprint:disabled_no_key") {
		t.Errorf("passes = %v, want the skip recorded", passes)
	}
}

// The rung is last for a reason: fingerprinting decodes audio and then makes a
// network call, so a file an earlier pass names must never reach it.
func TestObserve_AFileItsTagsIdentifyIsNeverFingerprinted(t *testing.T) {
	prints := &fakeFingerprinter{print: mediatool.Fingerprint{Chromaprint: "AQADtE", Duration: 215 * time.Second}}
	lookup := &fakeLookup{enabled: true}
	consumer, _, fsys := newFingerprintConsumer(t, prints, lookup)
	content := []byte("tagged")
	writeMusicFile(t, fsys, "P!po/Kebilegev Bibihi/01.m4a", content)
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	batch := []scan.Observed{observedFile("P!po/Kebilegev Bibihi/01.m4a", int64(len(content)),
		mediatool.Tags{"album": "Kebilegev Bibihi", "musicbrainz_releasegroupid": releaseGroupMBID})}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if prints.calls != 0 || lookup.calls != 0 {
		t.Errorf("fingerprint calls = %d, lookup calls = %d, want none for a file its tags named",
			prints.calls, lookup.calls)
	}
}

// passesFor reads one file's recorded passes as "pass:outcome" strings.
func passesFor(t *testing.T, opened *store.Store, rootID int64, relPath string, fsys *fsx.Mem) []string {
	t.Helper()
	info, err := fsys.Stat(path.Join(testRoot, relPath))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), rootID, []library.PhysicalFile{{
		RelPath: relPath, SizeBytes: info.Size,
		QuickHash: mustQuickHash(t, fsys, relPath, info.Size), FirstSeenAt: observedAt,
	}})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	attempts, err := library.NewAttempts(opened).List(t.Context(), registered[0].MediaObjectID)
	if err != nil {
		t.Fatalf("List attempts: %v", err)
	}
	var passes []string
	for _, attempt := range attempts {
		for _, pass := range attempt.Passes {
			passes = append(passes, pass.Pass+":"+pass.Outcome)
		}
	}
	return passes
}

// The case the rung exists for, end to end: a folder whose files carry no tags at
// all is named by its audio, and the names are then matched against whole
// tracklists like any other album -- which is the only way to tell which of the
// many release groups a recording appears on this folder actually is.
func TestObserve_FingerprintNamesAFolderAndTheAlbumMatcherIdentifiesIt(t *testing.T) {
	prints := &fakeFingerprinter{byPath: map[string]mediatool.Fingerprint{}}
	lookup := &fakeLookup{enabled: true, byPrint: map[string][]acoustid.Match{}}
	batch := make([]scan.Observed, len(traumaTracks))

	opened, fsys, fake := newTestLibrary(t)
	for i, track := range traumaTracks {
		relPath := fmt.Sprintf("Unknown/Unknown Album/%02d.m4a", i+1)
		content := []byte("untagged " + track.title)
		writeMusicFile(t, fsys, relPath, content)
		batch[i] = observedFile(relPath, int64(len(content)), mediatool.Tags{})
		batch[i].Probe.Measured.Duration = time.Duration(track.seconds) * time.Second

		fingerprint := fmt.Sprintf("AQAD-%d", i)
		prints.byPath[path.Join(testRoot, relPath)] = mediatool.Fingerprint{
			Chromaprint: fingerprint, Duration: batch[i].Probe.Measured.Duration,
		}
		lookup.byPrint[fingerprint] = []acoustid.Match{{
			Score: 0.97,
			Recordings: []acoustid.Recording{{
				MBID: "recording-" + track.title, Title: track.title, Artist: "P!po",
				// Every track names the album; the first also names a compilation it
				// appears on, which must not win by being seen first.
				ReleaseGroupMBIDs: releaseGroupsFor(i, releaseGroupMBID),
			}},
		}}
	}

	attempts := library.NewAttempts(opened)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Unidentified: library.NewUnidentified(opened), Attempts: &attempts,
		Albums:       traumaLookup(releaseGroupMBID),
		Fingerprints: prints, AcoustID: lookup,
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v, want the album the fingerprinted names matched", err)
	}
	// Named after the record, not after the directory it was ripped into.
	if work.CanonicalTitle != "Kebilegev Bibihi" {
		t.Errorf("CanonicalTitle = %q, want the matched release group's own title", work.CanonicalTitle)
	}
	if prints.calls != 1 {
		t.Errorf("Fingerprint calls = %d, want one call for the whole batch", prints.calls)
	}
	passes := passesFor(t, opened, 1, "Unknown/Unknown Album/01.m4a", fsys)
	if !slices.Contains(passes, "fingerprint:identified") {
		t.Errorf("passes = %v, want the fingerprint pass recorded as identified", passes)
	}
}

// releaseGroupsFor gives the album to every track and a compilation to the first,
// so the seed order has to be decided by agreement rather than by first sight.
func releaseGroupsFor(index int, album string) []string {
	if index == 0 {
		return []string{"compilation-only-this-track-is-on", album}
	}
	return []string{album}
}

// AcoustID's database is crowd-contributed, and a real Heka track answered with a
// recording by a band called Badtown Boys, listed first (measured 2026-09-18). One
// file cannot tell which of its candidates is the wrong one; the folder can, because
// every other file names the same artist and none of them names that one.
func TestObserve_AWrongRecordingIsOutvotedByTheRestOfTheFolder(t *testing.T) {
	prints := &fakeFingerprinter{byPath: map[string]mediatool.Fingerprint{}}
	lookup := &fakeLookup{enabled: true, byPrint: map[string][]acoustid.Match{}}
	batch := make([]scan.Observed, len(traumaTracks))

	opened, fsys, fake := newTestLibrary(t)
	for i, track := range traumaTracks {
		relPath := fmt.Sprintf("Unknown/Unknown Album/%02d.m4a", i+1)
		content := []byte("untagged " + track.title)
		writeMusicFile(t, fsys, relPath, content)
		batch[i] = observedFile(relPath, int64(len(content)), mediatool.Tags{})
		batch[i].Probe.Measured.Duration = time.Duration(track.seconds) * time.Second

		fingerprint := fmt.Sprintf("AQAD-%d", i)
		prints.byPath[path.Join(testRoot, relPath)] = mediatool.Fingerprint{
			Chromaprint: fingerprint, Duration: batch[i].Probe.Measured.Duration,
		}
		right := acoustid.Recording{
			MBID: "recording-" + track.title, Title: track.title, Artist: "P!po",
			Duration:          time.Duration(track.seconds) * time.Second,
			ReleaseGroupMBIDs: []string{releaseGroupMBID},
		}
		recordings := []acoustid.Recording{right}
		if i == 0 {
			// Same length, wrong entirely, and first in the list.
			wrong := acoustid.Recording{
				MBID: "recording-bogus", Title: "Zek't Hefa Up On Me", Artist: "Badtown Boys",
				Duration:          time.Duration(track.seconds) * time.Second,
				ReleaseGroupMBIDs: []string{"another-fine-day"},
			}
			recordings = []acoustid.Recording{wrong, right}
		}
		lookup.byPrint[fingerprint] = []acoustid.Match{{Score: 0.97, Recordings: recordings}}
	}

	attempts := library.NewAttempts(opened)
	albums := traumaLookup(releaseGroupMBID)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Unidentified: library.NewUnidentified(opened), Attempts: &attempts,
		Albums:       albums,
		Fingerprints: prints, AcoustID: lookup,
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}

	if err := consumer.Observe(t.Context(), root, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	if _, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	}); err != nil {
		t.Fatalf("GetWorkByExternalID: %v, want the album the folder agreed on", err)
	}
	// The outvoted recording's own album is never even recalled: recall is capped
	// at ten release groups, so every seed a wrong link contributes costs a real
	// candidate its place.
	if slices.Contains(albums.asked, "album:another-fine-day") {
		t.Errorf("the outvoted recording's album was recalled: %v", albums.asked)
	}
}
