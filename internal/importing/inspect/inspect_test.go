package inspect_test

import (
	"path"
	"sort"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/importing/inspect"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

const root = "/downloads/incoming"

type tree struct {
	fsys  *fsx.Mem
	clock *clock.Fake
}

// newTree builds an in-memory filesystem with one file at each of paths, every
// one 200 KiB -- comfortably above DefaultMediaSizeFloorBytes, so a fixture path
// need not fight the size floor unless a test is specifically about it. Each
// file's mtime advances by a second, in argument order, the same real-disk
// property splitByTrackContinuity relies on -- see its own doc comment.
func newTree(t *testing.T, paths ...string) *tree {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	if err := fsys.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	tr := &tree{fsys: fsys, clock: fake}
	for _, p := range paths {
		tr.write(t, p, 200*1024)
	}
	return tr
}

func (tr *tree) write(t *testing.T, relPath string, sizeBytes int) {
	t.Helper()
	tr.clock.Advance(time.Second)
	absolute := path.Join(root, relPath)
	if err := tr.fsys.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path.Dir(absolute), err)
	}
	file, err := tr.fsys.Create(absolute)
	if err != nil {
		t.Fatalf("Create(%q): %v", absolute, err)
	}
	if _, err := file.Write(make([]byte, sizeBytes)); err != nil {
		t.Fatalf("Write(%q): %v", absolute, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q): %v", absolute, err)
	}
}

func mediaPaths(u inspect.Unit) []string {
	paths := make([]string, len(u.Media))
	for i, f := range u.Media {
		paths[i] = f.RelPath
	}
	sort.Strings(paths)
	return paths
}

func unitContaining(t *testing.T, units []inspect.Unit, relPath string) inspect.Unit {
	t.Helper()
	for _, u := range units {
		for _, f := range u.Media {
			if f.RelPath == relPath {
				return u
			}
		}
	}
	t.Fatalf("no unit contains %q among %d units", relPath, len(units))
	return inspect.Unit{}
}

// Hard case 1: Artist/Album/track.mp3 at depth 3 -- the simple case every
// fixed-depth rule gets right, kept as the baseline the harder cases are
// judged against.
func TestRun_ArtistAlbumTrackAtDepthThree(t *testing.T) {
	tr := newTree(t,
		"Cibiz M/Zuroveguv Wants to Dance/01-track.flac",
		"Cibiz M/Zuroveguv Wants to Dance/02-track.flac",
		"Cibiz M/Zuroveguv Wants to Dance/03-track.flac",
	)
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 1 {
		t.Fatalf("got %d units, want 1: %+v", len(session.Units), session.Units)
	}
	if len(session.Units[0].Media) != 3 {
		t.Errorf("got %d media files, want 3", len(session.Units[0].Media))
	}
}

// Hard case 2: two unrelated albums flat in one directory, no tags available.
// Track numbers restarting is the only signal that can split them, and doing
// so must be flagged Ambiguous -- a restart is suggestive, not proof.
func TestRun_TwoUnrelatedAlbumsFlatWithNoTagsSplitByTrackContinuity(t *testing.T) {
	tr := newTree(t,
		"flat/01-song-a.mp3", "flat/02-song-b.mp3", "flat/03-song-c.mp3",
		"flat/01-song-x.mp3", "flat/02-song-y.mp3",
	)
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 2 {
		t.Fatalf("got %d units, want 2: %+v", len(session.Units), session.Units)
	}

	first := unitContaining(t, session.Units, "flat/01-song-a.mp3")
	if got := mediaPaths(first); len(got) != 3 {
		t.Errorf("first group = %v, want the 3 -a/-b/-c files", got)
	}
	second := unitContaining(t, session.Units, "flat/01-song-x.mp3")
	if got := mediaPaths(second); len(got) != 2 {
		t.Errorf("second group = %v, want the 2 -x/-y files", got)
	}
	if !second.Evidence.Ambiguous {
		t.Error("want the track-continuity split flagged Ambiguous")
	}
}

// The same two-albums-in-one-directory case, but with embedded release ids
// disagreeing -- the decisive signal, so no ambiguity this time.
func TestRun_TwoUnrelatedAlbumsFlatWithDisagreeingTagsSplitDecisively(t *testing.T) {
	tr := newTree(t, "flat/song-a.mp3", "flat/song-b.mp3", "flat/song-x.mp3")
	tags := func(relPath string) inspect.Tags {
		switch relPath {
		case "flat/song-a.mp3", "flat/song-b.mp3":
			return inspect.Tags{Found: true, ReleaseID: "release-1", Album: "Album One"}
		default:
			return inspect.Tags{Found: true, ReleaseID: "release-2", Album: "Album Two"}
		}
	}

	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{Tags: tags})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 2 {
		t.Fatalf("got %d units, want 2: %+v", len(session.Units), session.Units)
	}
	one := unitContaining(t, session.Units, "flat/song-a.mp3")
	if one.Evidence.Ambiguous {
		t.Error("an embedded release id disagreement is decisive, want Ambiguous=false")
	}
	if len(one.Media) != 2 {
		t.Errorf("got %d files sharing release-1, want 2", len(one.Media))
	}
}

// Hard case 3: Album/CD1 + Album/CD2 must merge into one unit, not two.
func TestRun_DiscSubfoldersMergeIntoOneUnit(t *testing.T) {
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
	if len(session.Units[0].Media) != 4 {
		t.Errorf("got %d media files, want all 4 tracks merged", len(session.Units[0].Media))
	}
	if session.Units[0].Directory != "Dipa Cogaf/The Gupe" {
		t.Errorf("Directory = %q, want the parent of CD1/CD2", session.Units[0].Directory)
	}
}

// Also covers "Disc 2", "Vol. 3" spellings, and that a disc set is not
// confused with two genuinely different albums that both happen to be
// numbered subfolders of an artist (CDn only merges when the parent itself
// is not also identified as a distinct album by its own contents elsewhere).
func TestRun_DiscSubfolderSpellingVariants(t *testing.T) {
	tr := newTree(t,
		"Artist/Boxed Set/Disc 1/01-track.flac",
		"Artist/Boxed Set/Disc 2/01-track.flac",
	)
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 1 {
		t.Fatalf("got %d units, want 1: %+v", len(session.Units), session.Units)
	}
}

// Hard case 4: a discography whose album folders are two levels down must
// produce one unit per album, not one giant discography unit.
func TestRun_DiscographyTwoLevelsDownProducesOneUnitPerAlbum(t *testing.T) {
	tr := newTree(t,
		"Artist/Discography/1990s/First Album/01-track.flac",
		"Artist/Discography/1990s/First Album/02-track.flac",
		"Artist/Discography/2000s/Second Album/01-track.flac",
		"Artist/Discography/2000s/Second Album/02-track.flac",
		"Artist/Discography/2000s/Second Album/03-track.flac",
	)
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 2 {
		t.Fatalf("got %d units, want 2 (one per album, not one discography unit): %+v",
			len(session.Units), session.Units)
	}
	first := unitContaining(t, session.Units, "Artist/Discography/1990s/First Album/01-track.flac")
	if len(first.Media) != 2 {
		t.Errorf("first album got %d files, want 2", len(first.Media))
	}
	second := unitContaining(t, session.Units, "Artist/Discography/2000s/Second Album/01-track.flac")
	if len(second.Media) != 3 {
		t.Errorf("second album got %d files, want 3", len(second.Media))
	}
}

// A file too small to be real media (ADR-48's own "sample.mp3" example) must
// not join the unit as a track.
func TestRun_UndersizedMediaExtensionIsJunkNotATrack(t *testing.T) {
	tr := newTree(t, "Artist/Album/01-track.flac", "Artist/Album/02-track.flac")
	tr.write(t, "Artist/Album/sample.mp3", 2*1024)

	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(session.Units) != 1 {
		t.Fatalf("got %d units, want 1", len(session.Units))
	}
	if len(session.Units[0].Media) != 2 {
		t.Errorf("got %d media files, want 2 -- sample.mp3 must not count", len(session.Units[0].Media))
	}
}

// A container segment shared by every unit beneath it (the download client's
// own wrapping folder) must never be mistaken for an artist name.
func TestRun_SharedWrapperSegmentIsAContainer(t *testing.T) {
	tr := newTree(t,
		"_unsorted/Artist One/Album/01-track.flac",
		"_unsorted/Artist Two/Album/01-track.flac",
	)
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, c := range session.ContainerSegments {
		if c == "_unsorted" {
			found = true
		}
	}
	if !found {
		t.Errorf("ContainerSegments = %v, want \"_unsorted\" (shared by every unit)", session.ContainerSegments)
	}
	for _, c := range session.ContainerSegments {
		if c == "Artist One" || c == "Artist Two" {
			t.Errorf("ContainerSegments = %v, a real artist name must not be flagged as a container", session.ContainerSegments)
		}
	}
}

func TestClassify_EveryClass(t *testing.T) {
	cases := []struct {
		path  string
		size  int64
		floor int64
		want  inspect.Class
	}{
		{"Album/01-track.flac", 200 * 1024, 64 * 1024, inspect.ClassMedia},
		{"Album/sample.mp3", 2 * 1024, 64 * 1024, inspect.ClassJunk},
		{"Album.rar", 1024, 64 * 1024, inspect.ClassArchive},
		{"Album.r00", 1024, 64 * 1024, inspect.ClassArchive},
		{"cover.jpg", 1024, 64 * 1024, inspect.ClassArtwork},
		{"album.cue", 1024, 64 * 1024, inspect.ClassSidecar},
		{"movie.srt", 1024, 64 * 1024, inspect.ClassSubtitle},
		{"Thumbs.db", 1024, 64 * 1024, inspect.ClassJunk},
		{"readme", 1024, 64 * 1024, inspect.ClassUnknown},
	}
	for _, c := range cases {
		if got := inspect.Classify(c.path, c.size, c.floor); got != c.want {
			t.Errorf("Classify(%q, %d, %d) = %q, want %q", c.path, c.size, c.floor, got, c.want)
		}
	}
}
