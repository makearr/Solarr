package inspect_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/importing/inspect"
)

// walkAll runs a Walker to completion from the beginning and collects every
// emitted cluster into one flat slice, the shape most of these tests want to
// assert against.
func walkAll(t *testing.T, tr *tree, opts inspect.WalkOptions) ([]inspect.Unit, inspect.WalkResult) {
	t.Helper()
	var units []inspect.Unit
	result, err := inspect.NewWalker().Walk(t.Context(), tr.fsys, root, inspect.WalkState{}, opts,
		func(_ context.Context, batch []inspect.Unit) error {
			units = append(units, batch...)
			return nil
		})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return units, result
}

// Walker must group exactly the same as Run for every one of Run's own hard
// cases -- it is a different traversal strategy over the same rules, not a
// different set of rules.

func TestWalker_ArtistAlbumTrackAtDepthThree(t *testing.T) {
	tr := newTree(t, "Katupoken/KI Pecezinu/01 Sesoge.flac")
	units, result := walkAll(t, tr, inspect.WalkOptions{})
	if len(units) != 1 {
		t.Fatalf("got %d units, want 1: %+v", len(units), units)
	}
	if units[0].Directory != "Katupoken/KI Pecezinu" {
		t.Errorf("Directory = %q, want Katupoken/KI Pecezinu", units[0].Directory)
	}
	if !result.Completed {
		t.Error("Completed = false, want true for an uninterrupted walk")
	}
}

func TestWalker_DiscSubfoldersMergeIntoOneUnit(t *testing.T) {
	tr := newTree(t,
		"Dipa Cogaf/The Gupe/CD1/01-track.flac", "Dipa Cogaf/The Gupe/CD1/02-track.flac",
		"Dipa Cogaf/The Gupe/CD2/01-track.flac", "Dipa Cogaf/The Gupe/CD2/02-track.flac",
	)
	units, _ := walkAll(t, tr, inspect.WalkOptions{})
	if len(units) != 1 {
		t.Fatalf("got %d units, want 1 merged disc set: %+v", len(units), units)
	}
	if len(units[0].Media) != 4 {
		t.Errorf("got %d media files, want all 4 tracks merged", len(units[0].Media))
	}
	if units[0].Directory != "Dipa Cogaf/The Gupe" {
		t.Errorf("Directory = %q, want the parent of CD1/CD2", units[0].Directory)
	}
	found := false
	for _, s := range units[0].Evidence.Signals {
		if s.Kind == inspect.SignalDiscSubfolder {
			found = true
		}
	}
	if !found {
		t.Error("merged unit carries no SignalDiscSubfolder evidence")
	}
}

func TestWalker_TwoUnrelatedAlbumsFlatWithDisagreeingTagsSplitDecisively(t *testing.T) {
	tr := newTree(t, "Unsorted/a.flac", "Unsorted/b.flac", "Unsorted/c.flac", "Unsorted/d.flac")
	tagged := taggedReader(map[string]inspect.Tags{
		"Unsorted/a.flac": {Found: true, Artist: "Katupoken", Album: "KI Pecezinu"},
		"Unsorted/b.flac": {Found: true, Artist: "Katupoken", Album: "KI Pecezinu"},
		"Unsorted/c.flac": {Found: true, Artist: "Migf", Album: "Gesuhu"},
		"Unsorted/d.flac": {Found: true, Artist: "Migf", Album: "Gesuhu"},
	})
	units, _ := walkAll(t, tr, inspect.WalkOptions{Tags: tagged})
	if len(units) != 2 {
		t.Fatalf("got %d units, want 2 (split by disagreeing tags): %+v", len(units), units)
	}
}

func TestWalker_DiscographyTwoLevelsDownProducesOneUnitPerAlbum(t *testing.T) {
	tr := newTree(t,
		"Artist/Album1/01.flac", "Artist/Album1/02.flac",
		"Artist/Album2/01.flac", "Artist/Album2/02.flac",
		"Artist/Album3/01.flac",
	)
	units, _ := walkAll(t, tr, inspect.WalkOptions{})
	if len(units) != 3 {
		t.Fatalf("got %d units, want one per album: %+v", len(units), units)
	}
}

// The exact case ADR-48's own text names for resumability: a whole-library
// walk interrupted part-way must not redo, or lose, work already committed.
func TestWalker_ResumesFromCursorWithoutReemittingOrMissingWork(t *testing.T) {
	tr := newTree(t,
		"Artist/Album1/01.flac", "Artist/Album1/02.flac",
		"Artist/Album2/01.flac",
		"Artist/Album3/01.flac",
	)

	var firstPass []string
	result, err := inspect.NewWalker().Walk(t.Context(), tr.fsys, root, inspect.WalkState{}, inspect.WalkOptions{},
		func(_ context.Context, batch []inspect.Unit) error {
			firstPass = append(firstPass, batch[0].Directory)
			if batch[0].Directory == "Artist/Album2" {
				return errInterrupted
			}
			return nil
		})
	if err == nil {
		t.Fatal("Walk succeeded, want the injected interruption to have stopped it")
	}
	if len(firstPass) != 2 {
		t.Fatalf("emitted %d clusters before interruption, want 2 (Album1, Album2): %v", len(firstPass), firstPass)
	}
	interruptedCursor := result.State.Cursor
	if interruptedCursor != "Artist/Album1" {
		t.Fatalf("cursor after interruption = %q, want Artist/Album1 (the last one committed)", interruptedCursor)
	}

	var secondPass []string
	result2, err := inspect.NewWalker().Walk(t.Context(), tr.fsys, root,
		inspect.WalkState{Cursor: interruptedCursor}, inspect.WalkOptions{},
		func(_ context.Context, batch []inspect.Unit) error {
			secondPass = append(secondPass, batch[0].Directory)
			return nil
		})
	if err != nil {
		t.Fatalf("resumed Walk: %v", err)
	}
	if !result2.Completed {
		t.Error("resumed walk did not complete")
	}
	want := []string{"Artist/Album2", "Artist/Album3"}
	if len(secondPass) != len(want) {
		t.Fatalf("resumed pass emitted %v, want %v", secondPass, want)
	}
	for i, dir := range want {
		if secondPass[i] != dir {
			t.Errorf("resumed pass emitted %v, want %v", secondPass, want)
			break
		}
	}
}

// A cursor naming a directory this run's traversal never reaches (deleted or
// renamed between runs) must fail loudly rather than silently skip everything
// after it -- the one failure mode worse than redoing work.
func TestWalker_ErrorsWhenTheResumeCursorIsNeverFound(t *testing.T) {
	tr := newTree(t, "Artist/Album1/01.flac")
	_, err := inspect.NewWalker().Walk(t.Context(), tr.fsys, root,
		inspect.WalkState{Cursor: "Artist/Vanished"}, inspect.WalkOptions{},
		func(context.Context, []inspect.Unit) error { return nil })
	if err == nil {
		t.Fatal("Walk succeeded despite a resume cursor that was never reached")
	}
}

// ContainerSegments must reach the same answer as Run's global computation,
// even though Walker never holds every Unit in memory at once to compute it.
func TestWalker_ContainerSegmentsMatchTheWholeSessionRule(t *testing.T) {
	tr := newTree(t,
		"downloads/Artist1/Album1/01.flac",
		"downloads/Artist2/Album2/01.flac",
	)
	_, result := walkAll(t, tr, inspect.WalkOptions{})
	found := false
	for _, s := range result.ContainerSegments {
		if s == "downloads" {
			found = true
		}
	}
	if !found {
		t.Errorf("ContainerSegments = %v, want \"downloads\" (shared by every unit)", result.ContainerSegments)
	}
}

// EstimateUnits agrees with a real Walk over the same tree when there is no
// tag evidence to disagree over -- the common case, since Job.Run's own
// WalkOptions{} never feeds Walk any tags either (todo 4.21).
func TestEstimateUnits_MatchesARealWalkWithNoTagEvidence(t *testing.T) {
	tr := newTree(t,
		"Katupoken/KI Pecezinu/01 Sesoge.flac",
		"Dipa Cogaf/The Gupe/CD1/01-track.flac", "Dipa Cogaf/The Gupe/CD2/01-track.flac",
	)
	units, _ := walkAll(t, tr, inspect.WalkOptions{})

	estimate, err := inspect.EstimateUnits(t.Context(), tr.fsys, root)
	if err != nil {
		t.Fatalf("EstimateUnits: %v", err)
	}
	if estimate != len(units) {
		t.Errorf("EstimateUnits = %d, want %d (matching the real walk)", estimate, len(units))
	}
}

// A directory only tag evidence would split (no structural signal at all --
// same album name, same track numbers, only the release id disagrees) is the
// one accepted inaccuracy: the estimate cannot see that far ahead of a real
// probe, so it undercounts one real unit as one.
func TestEstimateUnits_UndercountsATagOnlySplit(t *testing.T) {
	tr := newTree(t, "Messy/01 Track.flac", "Messy/02 Track.flac")
	estimate, err := inspect.EstimateUnits(t.Context(), tr.fsys, root)
	if err != nil {
		t.Fatalf("EstimateUnits: %v", err)
	}
	if estimate != 1 {
		t.Errorf("EstimateUnits = %d, want 1 (no structural signal to split on without tags)", estimate)
	}
}

var errInterrupted = &interruptedError{}

type interruptedError struct{}

func (*interruptedError) Error() string { return "injected interruption" }

// taggedReader is a TagReader over a fixed table, keyed by relative path.
func taggedReader(byPath map[string]inspect.Tags) inspect.TagReader {
	return func(relPath string) inspect.Tags { return byPath[relPath] }
}

// A walk limited to some files sees only those: the finished tracks of a download that stalled.
func TestWalker_OnlySeesTheFilesItIsGiven(t *testing.T) {
	tr := newTree(t, "Katupoken/KI Pecezinu/01 Sesoge.flac", "Katupoken/KI Pecezinu/02 Vagelo.flac", "Katupoken/KI Pecezinu/03 Rumi.flac")
	units, _ := walkAll(t, tr, inspect.WalkOptions{Only: map[string]bool{
		"Katupoken/KI Pecezinu/01 Sesoge.flac": true, "Katupoken/KI Pecezinu/03 Rumi.flac": true,
	}})
	if len(units) != 1 || len(units[0].Media) != 2 || units[0].Media[0].RelPath != "Katupoken/KI Pecezinu/01 Sesoge.flac" ||
		units[0].Media[1].RelPath != "Katupoken/KI Pecezinu/03 Rumi.flac" {
		t.Errorf("units = %+v, want one album of the two files given", units)
	}
}

// A download is untrusted: a symlink in it is never imported, so it cannot bring the file it points at into a library.
func TestWalker_AndListFilesNeverImportASymlink(t *testing.T) {
	tr := newTree(t, "Katupoken/KI Pecezinu/01 Sesoge.flac")
	if err := tr.fsys.MkdirAll("/config", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if file, err := tr.fsys.Create("/config/settings.db"); err != nil {
		t.Fatalf("Create: %v", err)
	} else {
		_, _ = file.Write(make([]byte, 300*1024))
		_ = file.Close()
	}
	for _, link := range []string{"Katupoken/KI Pecezinu/02 Vagelo.flac", "Katupoken/KI Pecezinu/album.nfo"} {
		if err := tr.fsys.Symlink("/config/settings.db", root+"/"+link); err != nil {
			t.Fatalf("Symlink: %v", err)
		}
	}

	units, _ := walkAll(t, tr, inspect.WalkOptions{})
	if len(units) != 1 || len(units[0].Media) != 1 || len(units[0].Other) != 0 {
		t.Errorf("units = %+v, want one album of the one real file", units)
	}
	files, err := inspect.ListFiles(t.Context(), tr.fsys, root, 0)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].RelPath != "Katupoken/KI Pecezinu/01 Sesoge.flac" {
		t.Errorf("ListFiles = %+v, want only the real file", files)
	}
}
