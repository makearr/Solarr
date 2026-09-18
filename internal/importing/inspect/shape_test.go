package inspect_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/inspect"
)

// A unit of exactly one media file is a single, never an album.
func TestShapeOf_OneFileIsSingle(t *testing.T) {
	tr := newTree(t, "Artist/Track.flac")
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 1 {
		t.Fatalf("got %d units, want 1", len(session.Units))
	}
	if shape := inspect.ShapeOf(session.Units[0]); shape != inspect.ShapeSingle {
		t.Errorf("ShapeOf = %q, want single", shape)
	}
}

// Several files in one directory, no disc-subfolder evidence, is a set -- an
// album's worth of tracks in one place.
func TestShapeOf_SeveralFilesWithNoDiscEvidenceIsSet(t *testing.T) {
	tr := newTree(t,
		"Artist/Album/01-track.flac", "Artist/Album/02-track.flac", "Artist/Album/03-track.flac",
	)
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 1 {
		t.Fatalf("got %d units, want 1: %+v", len(session.Units), session.Units)
	}
	if shape := inspect.ShapeOf(session.Units[0]); shape != inspect.ShapeSet {
		t.Errorf("ShapeOf = %q, want set", shape)
	}
}

// The exact CD1/CD2 merge case TestRun_DiscSubfoldersMergeIntoOneUnit already
// proves GROUP handles -- ShapeOf must read its own SignalDiscSubfolder evidence
// and report multi_disc, not set, even though the file count alone cannot tell the
// two apart.
func TestShapeOf_MergedDiscSubfoldersIsMultiDisc(t *testing.T) {
	tr := newTree(t,
		"Dipa Cogaf/The Gupe/CD1/01-track.flac", "Dipa Cogaf/The Gupe/CD1/02-track.flac",
		"Dipa Cogaf/The Gupe/CD2/01-track.flac", "Dipa Cogaf/The Gupe/CD2/02-track.flac",
	)
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 1 {
		t.Fatalf("got %d units, want 1 merged disc set: %+v", len(session.Units), session.Units)
	}
	if shape := inspect.ShapeOf(session.Units[0]); shape != inspect.ShapeMultiDisc {
		t.Errorf("ShapeOf = %q, want multi_disc", shape)
	}
}

// Mutation guard: swapping the disc-subfolder check away from the real signal
// (e.g. inferring multi-disc from file count instead) must fail this -- four files
// with no disc evidence at all is a large flat album, still a plain set.
func TestShapeOf_FourFlatFilesWithNoDiscEvidenceStaysSet(t *testing.T) {
	tr := newTree(t,
		"Artist/Album/01.flac", "Artist/Album/02.flac", "Artist/Album/03.flac", "Artist/Album/04.flac",
	)
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 1 {
		t.Fatalf("got %d units, want 1: %+v", len(session.Units), session.Units)
	}
	if shape := inspect.ShapeOf(session.Units[0]); shape != inspect.ShapeSet {
		t.Errorf("ShapeOf = %q, want set: four flat files is not disc evidence", shape)
	}
}
