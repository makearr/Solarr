package music_test

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/importing/inspect"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
	"github.com/makearr/MediaNexus/migrations"
)

const (
	commitSourceRoot = "/downloads/inbox"
	commitDestRoot   = "/library"
	commitCategoryID = 3
	commitDestRootID = 5
)

// fakePlacementRoots is music.PlacementRoots over a fixed map, standing in
// for the settings.Cache snapshot a real process asks fresh.
type fakePlacementRoots struct {
	roots         map[int64]settings.RootFolder
	categoryRoots []settings.CategoryRoot
}

func (f fakePlacementRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	root, found := f.roots[id]
	return root, found
}

func (f fakePlacementRoots) CategoryRoots() []settings.CategoryRoot { return f.categoryRoots }

// newCommitter builds a Committer over a real (temp file) library.db and an
// in-memory filesystem, with one destination root accepting placements for
// commitCategoryID -- the same shape newConsumerWithDiscovery already uses
// for scan-time discovery, since CommitUnit shares DiscoverWanted with it.
func newCommitter(t *testing.T, accepts bool) (music.Committer, *store.Store, *fsx.Mem) {
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
	if err := fsys.MkdirAll(commitSourceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fsys.MkdirAll(commitDestRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	desired := library.NewDesired(opened)
	plentyFree := int64(1 << 30)
	roots := fakePlacementRoots{
		roots: map[int64]settings.RootFolder{
			commitDestRootID: {
				ID: commitDestRootID, Path: commitDestRoot, Online: true, Writable: true,
				FreeBytes: &plentyFree, TotalBytes: &plentyFree,
			},
		},
	}
	if accepts {
		roots.categoryRoots = []settings.CategoryRoot{{CategoryID: commitCategoryID, RootFolderID: commitDestRootID, AcceptsNew: true}}
	}

	committer := music.NewCommitter(music.CommitterConfig{
		FS: fsys, Roots: roots, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Desired: &desired,
		Categories: fakeCategories{
			commitCategoryID: {ID: commitCategoryID, MonitorDiscovered: settings.MonitorDiscoveredMonitored},
		},
		Clock: fake, DefaultProfileID: 42,
	})
	return committer, opened, fsys
}

func writeCommitSourceFile(t *testing.T, fsys *fsx.Mem, relPath string, content []byte) {
	t.Helper()
	absolute := path.Join(commitSourceRoot, relPath)
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

func readAt(t *testing.T, fsys *fsx.Mem, absolutePath string) string {
	t.Helper()
	f, err := fsys.Open(absolutePath)
	if err != nil {
		t.Fatalf("Open(%q): %v", absolutePath, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll(%q): %v", absolutePath, err)
	}
	return string(data)
}

func testUnit(directory string, files []inspect.File) inspect.Unit {
	return inspect.Unit{Directory: directory, Media: files}
}

func TestCommitUnit_PlacesFilesRegistersAndDiscoversWithoutTouchingTheSource(t *testing.T) {
	committer, opened, fsys := newCommitter(t, true)

	track1 := []byte("airbag bytes")
	track2 := []byte("subterranean bytes")
	writeCommitSourceFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", track1)
	writeCommitSourceFile(t, fsys, "Katupoken/KI Pecezinu/02 Cufobekezepo Fusatebu Conat.flac", track2)

	unit := testUnit("Katupoken/KI Pecezinu", []inspect.File{
		{RelPath: "Katupoken/KI Pecezinu/01 Sesoge.flac", SizeBytes: int64(len(track1))},
		{RelPath: "Katupoken/KI Pecezinu/02 Cufobekezepo Fusatebu Conat.flac", SizeBytes: int64(len(track2))},
	})
	probes := []mediatool.Probe{
		{
			Path: path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/01 Sesoge.flac"),
			Tags: mediatool.Tags{
				"album": "KI Pecezinu", "albumartist": "Katupoken", "date": "1997-06-16",
				"tracknumber": "1", "title": "Sesoge",
			},
		},
		{
			Path: path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/02 Cufobekezepo Fusatebu Conat.flac"),
			Tags: mediatool.Tags{
				"album": "KI Pecezinu", "albumartist": "Katupoken", "date": "1997-06-16",
				"tracknumber": "2", "title": "Cufobekezepo Fusatebu Conat",
			},
		},
	}
	tags := music.UnitTagSourceFor(commitSourceRoot, probes)
	best := model.Identification{
		Strategy: "embedded-tag", Confidence: 0.95,
		ExternalID: &model.ExternalID{Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID, Confidence: 0.95},
	}

	placed, err := committer.CommitUnit(t.Context(), commitSourceRoot, unit, best, tags, commitCategoryID)
	if err != nil {
		t.Fatalf("CommitUnit: %v", err)
	}
	if !placed.WorkCreated {
		t.Error("WorkCreated = false, want true for a brand new work")
	}
	if placed.WorkID == 0 {
		t.Error("WorkID = 0, want a real id")
	}
	if len(placed.MediaObjectIDs) != 2 {
		t.Fatalf("len(MediaObjectIDs) = %d, want 2", len(placed.MediaObjectIDs))
	}

	// The source must be untouched -- PlaceOne never deletes or renames it.
	if readAt(t, fsys, path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/01 Sesoge.flac")) != string(track1) {
		t.Error("source file 1 was modified or removed by CommitUnit")
	}
	if readAt(t, fsys, path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/02 Cufobekezepo Fusatebu Conat.flac")) != string(track2) {
		t.Error("source file 2 was modified or removed by CommitUnit")
	}

	physical := library.NewPhysical(opened)
	wantContent := map[int64]string{placed.MediaObjectIDs[0]: string(track1), placed.MediaObjectIDs[1]: string(track2)}
	for _, mediaObjectID := range placed.MediaObjectIDs {
		exists, err := physical.Exists(t.Context(), mediaObjectID)
		if err != nil || !exists {
			t.Fatalf("media object %d does not exist: err=%v", mediaObjectID, err)
		}
		paths, err := physical.PathsForObject(t.Context(), mediaObjectID)
		if err != nil {
			t.Fatalf("PathsForObject(%d): %v", mediaObjectID, err)
		}
		if len(paths) != 1 {
			t.Fatalf("media object %d has %d paths, want 1", mediaObjectID, len(paths))
		}
		if paths[0].RootFolderID != commitDestRootID {
			t.Errorf("media object %d placed on root %d, want %d", mediaObjectID, paths[0].RootFolderID, commitDestRootID)
		}
		destAbs := path.Join(commitDestRoot, paths[0].RelPath)
		if got := readAt(t, fsys, destAbs); got != wantContent[mediaObjectID] {
			t.Errorf("placed file %q content = %q, want %q", destAbs, got, wantContent[mediaObjectID])
		}
	}

	work, err := librarydb.New(opened.Read()).GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID,
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if work.ID != placed.WorkID || work.CanonicalTitle != "KI Pecezinu" {
		t.Errorf("work = %+v, want id %d and title %q", work, placed.WorkID, "KI Pecezinu")
	}

	items, err := librarydb.New(opened.Read()).ListDesiredItemsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListDesiredItemsForWork: %v", err)
	}
	if len(items) != 1 || items[0].SatisfactionKind != "SET" || items[0].CategoryID != commitCategoryID {
		t.Errorf("desired items = %+v, want one SET item in category %d", items, commitCategoryID)
	}
}

// A second unit resolving to the same external id must match the existing
// work rather than creating a second one -- WorkCreated must say so, since
// a later revert must never delete a work another unit (or this same one,
// on a second file) still depends on.
func TestCommitUnit_MatchesAnExistingWorkOnASecondUnit(t *testing.T) {
	committer, _, fsys := newCommitter(t, true)
	track := []byte("airbag bytes")
	writeCommitSourceFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", track)
	unit := testUnit("Katupoken/KI Pecezinu", []inspect.File{
		{RelPath: "Katupoken/KI Pecezinu/01 Sesoge.flac", SizeBytes: int64(len(track))},
	})
	probes := []mediatool.Probe{{
		Path: path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/01 Sesoge.flac"),
		Tags: mediatool.Tags{"album": "KI Pecezinu", "albumartist": "Katupoken", "tracknumber": "1", "title": "Sesoge"},
	}}
	tags := music.UnitTagSourceFor(commitSourceRoot, probes)
	best := model.Identification{
		Confidence: 0.95,
		ExternalID: &model.ExternalID{Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID, Confidence: 0.95},
	}

	first, err := committer.CommitUnit(t.Context(), commitSourceRoot, unit, best, tags, commitCategoryID)
	if err != nil {
		t.Fatalf("first CommitUnit: %v", err)
	}
	if !first.WorkCreated {
		t.Fatal("first commit: WorkCreated = false, want true")
	}

	track2 := []byte("subterranean bytes")
	writeCommitSourceFile(t, fsys, "Katupoken/KI Pecezinu/02 Cufobekezepo Fusatebu Conat.flac", track2)
	unit2 := testUnit("Katupoken/KI Pecezinu", []inspect.File{
		{RelPath: "Katupoken/KI Pecezinu/02 Cufobekezepo Fusatebu Conat.flac", SizeBytes: int64(len(track2))},
	})
	probes2 := []mediatool.Probe{{
		Path: path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/02 Cufobekezepo Fusatebu Conat.flac"),
		Tags: mediatool.Tags{
			"album": "KI Pecezinu", "albumartist": "Katupoken", "tracknumber": "2",
			"title": "Cufobekezepo Fusatebu Conat",
		},
	}}
	tags2 := music.UnitTagSourceFor(commitSourceRoot, probes2)

	second, err := committer.CommitUnit(t.Context(), commitSourceRoot, unit2, best, tags2, commitCategoryID)
	if err != nil {
		t.Fatalf("second CommitUnit: %v", err)
	}
	if second.WorkCreated {
		t.Error("second commit: WorkCreated = true, want false (must match the existing work)")
	}
	if second.WorkID != first.WorkID {
		t.Errorf("second commit resolved to work %d, want %d", second.WorkID, first.WorkID)
	}
}

func TestCommitUnit_NoRootAcceptsThisCategory(t *testing.T) {
	committer, _, fsys := newCommitter(t, false) // no CategoryRoot attached at all
	track := []byte("airbag bytes")
	writeCommitSourceFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", track)
	unit := testUnit("Katupoken/KI Pecezinu", []inspect.File{
		{RelPath: "Katupoken/KI Pecezinu/01 Sesoge.flac", SizeBytes: int64(len(track))},
	})
	probes := []mediatool.Probe{{
		Path: path.Join(commitSourceRoot, "Katupoken/KI Pecezinu/01 Sesoge.flac"),
		Tags: mediatool.Tags{"album": "KI Pecezinu", "tracknumber": "1"},
	}}
	tags := music.UnitTagSourceFor(commitSourceRoot, probes)
	best := model.Identification{
		Confidence: 0.95,
		ExternalID: &model.ExternalID{Provider: "musicbrainz:release-group", ExternalID: releaseGroupMBID, Confidence: 0.95},
	}

	_, err := committer.CommitUnit(t.Context(), commitSourceRoot, unit, best, tags, commitCategoryID)
	if !errors.Is(err, music.ErrNoPlacement) {
		t.Fatalf("CommitUnit error = %v, want ErrNoPlacement", err)
	}
}

func TestCommitUnit_RefusesAnIdentificationWithNoExternalID(t *testing.T) {
	committer, _, fsys := newCommitter(t, true)
	track := []byte("airbag bytes")
	writeCommitSourceFile(t, fsys, "Katupoken/KI Pecezinu/01 Sesoge.flac", track)
	unit := testUnit("Katupoken/KI Pecezinu", []inspect.File{
		{RelPath: "Katupoken/KI Pecezinu/01 Sesoge.flac", SizeBytes: int64(len(track))},
	})
	tags := music.UnitTagSourceFor(commitSourceRoot, nil)

	_, err := committer.CommitUnit(t.Context(), commitSourceRoot, unit, model.Identification{Confidence: 0.95}, tags, commitCategoryID)
	if err == nil {
		t.Fatal("CommitUnit succeeded with no ExternalID on the identification")
	}
}
