package inspect_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/importing/inspect"
)

func tagsByPath(table map[string]inspect.Tags) inspect.TagReader {
	return func(relPath string) inspect.Tags { return table[relPath] }
}

func unitSizes(units []inspect.Unit) []int {
	sizes := make([]int, len(units))
	for i, unit := range units {
		sizes[i] = len(unit.Media)
	}
	return sizes
}

func runTagged(t *testing.T, tr *tree, tags inspect.TagReader) []inspect.Unit {
	t.Helper()
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{Tags: tags})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return session.Units
}

func TestSplitByTags_TwoAlbumsInOneFlatFolderWithUniqueNumbersAreTwoUnits(t *testing.T) {
	tr := newTree(t, "flat/a1.mp3", "flat/a2.mp3", "flat/b1.mp3", "flat/b2.mp3", "flat/b3.mp3")
	tags := tagsByPath(map[string]inspect.Tags{
		"flat/a1.mp3": {Found: true, Album: "Alpha", AlbumArtist: "Ixu Band"}, "flat/a2.mp3": {Found: true, Album: "Alpha", AlbumArtist: "Ixu Band"},
		"flat/b1.mp3": {Found: true, Album: "Beta", AlbumArtist: "Ixu Band"}, "flat/b2.mp3": {Found: true, Album: "Beta", AlbumArtist: "Ixu Band"},
		"flat/b3.mp3": {Found: true, Album: "Beta", AlbumArtist: "Ixu Band"},
	})
	units := runTagged(t, tr, tags)
	if got := unitSizes(units); len(got) != 2 || got[0]+got[1] != 5 {
		t.Fatalf("unit sizes = %v, want two units holding 5 files", got)
	}
	for _, unit := range units {
		if unit.Evidence.Signals[0].Kind != inspect.SignalTagAgreement || unit.Evidence.Ambiguous {
			t.Errorf("evidence = %+v, want tag agreement, not ambiguous", unit.Evidence)
		}
	}
}

func TestSplitByTags_FilesWithoutAReleaseIDJoinTheReleaseOfTheSameAlbum(t *testing.T) {
	paths := make([]string, 12)
	table := map[string]inspect.Tags{}
	for i := range paths {
		paths[i] = "album/" + string(rune('a'+i)) + ".mp3"
		table[paths[i]] = inspect.Tags{Found: true, Album: "Gamma", AlbumArtist: "Ixu Band", ReleaseID: "rel-1"}
		if i >= 10 {
			table[paths[i]] = inspect.Tags{Found: true, Album: "gamma ", AlbumArtist: "Ixu Band"}
		}
	}
	units := runTagged(t, newTree(t, paths...), tagsByPath(table))
	if got := unitSizes(units); len(got) != 1 || got[0] != 12 {
		t.Fatalf("unit sizes = %v, want one unit of 12", got)
	}
	if kind := units[0].Evidence.Signals[0].Kind; kind != inspect.SignalEmbeddedID {
		t.Errorf("signal = %q, want the embedded id", kind)
	}
}

func TestSplitByTags_ADiscMarkerInTheAlbumTagDoesNotSplitADiscSet(t *testing.T) {
	tr := newTree(t, "Set/CD1/1.mp3", "Set/CD1/2.mp3", "Set/CD2/1.mp3", "Set/CD2/2.mp3")
	tags := tagsByPath(map[string]inspect.Tags{
		"Set/CD1/1.mp3": {Found: true, Album: "Delta (Disc 1)", AlbumArtist: "Ixu Band"}, "Set/CD1/2.mp3": {Found: true, Album: "Delta (Disc 1)", AlbumArtist: "Ixu Band"},
		"Set/CD2/1.mp3": {Found: true, Album: "Delta (Disc 2)", AlbumArtist: "Ixu Band"}, "Set/CD2/2.mp3": {Found: true, Album: "Delta [CD 2]", AlbumArtist: "Ixu Band"},
	})
	if got := unitSizes(runTagged(t, tr, tags)); len(got) != 1 || got[0] != 4 {
		t.Fatalf("unit sizes = %v, want one unit of 4", got)
	}
}

func TestSplitByTags_AnUntaggedFileBesideTaggedOnesJoinsThem(t *testing.T) {
	paths := make([]string, 12)
	table := map[string]inspect.Tags{}
	for i := range paths {
		paths[i] = "album/" + string(rune('a'+i)) + ".mp3"
		if i < 11 {
			table[paths[i]] = inspect.Tags{Found: true, Album: "Epsilon", AlbumArtist: "Ixu Band"}
		}
	}
	if got := unitSizes(runTagged(t, newTree(t, paths...), tagsByPath(table))); len(got) != 1 || got[0] != 12 {
		t.Fatalf("unit sizes = %v, want one unit of 12", got)
	}
}

func TestSplitByTags_UntaggedFilesBesideSeveralAlbumsAreAmbiguousAndKept(t *testing.T) {
	tr := newTree(t, "flat/a1.mp3", "flat/b1.mp3", "flat/x.mp3")
	tags := tagsByPath(map[string]inspect.Tags{
		"flat/a1.mp3": {Found: true, Album: "Alpha", AlbumArtist: "Ixu Band"}, "flat/b1.mp3": {Found: true, Album: "Beta", AlbumArtist: "Ixu Band"},
	})
	units := runTagged(t, tr, tags)
	if got := unitSizes(units); len(got) != 3 {
		t.Fatalf("unit sizes = %v, want 3 units, none lost", got)
	}
	for _, unit := range units {
		if !unit.Evidence.Ambiguous {
			t.Errorf("unit %v not ambiguous", mediaPaths(unit))
		}
	}
}

func TestWalk_TagsForReadsEachClusterAndAFailureFallsBackToNoTags(t *testing.T) {
	tr := newTree(t, "flat/a1.mp3", "flat/a2.mp3", "flat/b1.mp3")
	table := map[string]inspect.Tags{
		"flat/a1.mp3": {Found: true, Album: "Alpha", AlbumArtist: "Ixu Band"}, "flat/a2.mp3": {Found: true, Album: "Alpha", AlbumArtist: "Ixu Band"},
		"flat/b1.mp3": {Found: true, Album: "Beta", AlbumArtist: "Ixu Band"},
	}
	var asked []string
	for _, tc := range []struct {
		name string
		read func(context.Context, []string) (inspect.TagReader, error)
		want int
	}{
		{"reads", func(_ context.Context, relPaths []string) (inspect.TagReader, error) {
			asked = relPaths
			return tagsByPath(table), nil
		}, 2},
		{"fails", func(context.Context, []string) (inspect.TagReader, error) { return nil, errors.New("no ffprobe") }, 1},
	} {
		var units []inspect.Unit
		_, err := inspect.NewWalker().Walk(t.Context(), tr.fsys, root, inspect.WalkState{}, inspect.WalkOptions{TagsFor: tc.read}, func(_ context.Context, batch []inspect.Unit) error {
			units = append(units, batch...)
			return nil
		})
		if err != nil {
			t.Fatalf("%s: Walk: %v", tc.name, err)
		}
		if len(units) != tc.want {
			t.Errorf("%s: %d units, want %d", tc.name, len(units), tc.want)
		}
	}
	if len(asked) != 3 {
		t.Errorf("TagsFor asked for %v, want the cluster's 3 media files", asked)
	}
}
