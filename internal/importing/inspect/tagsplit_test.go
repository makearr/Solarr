package inspect_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/importing/inspect"
)

// keyedGrouper groups paths sharing a key, the way a media type's rule would; paths without a key are untagged,
// joining the only group or forming an ambiguous one of their own.
func keyedGrouper(kind inspect.SignalKind, keys map[string]string) inspect.TagGrouper {
	return func(relPaths []string) (inspect.TagGroups, bool) {
		var grouped inspect.TagGroups
		index := map[string]int{}
		var untagged []int
		for i, relPath := range relPaths {
			key, found := keys[relPath]
			if !found {
				untagged = append(untagged, i)
				continue
			}
			if _, seen := index[key]; !seen {
				index[key] = len(grouped.Groups)
				grouped.Groups = append(grouped.Groups, nil)
				grouped.Signals = append(grouped.Signals, inspect.Signal{Kind: kind, Detail: key})
			}
			grouped.Groups[index[key]] = append(grouped.Groups[index[key]], i)
		}
		switch {
		case len(grouped.Groups) == 0:
			return inspect.TagGroups{}, false
		case len(untagged) == 0:
		case len(grouped.Groups) == 1:
			grouped.Groups[0] = append(grouped.Groups[0], untagged...)
		default:
			grouped.Groups = append(grouped.Groups, untagged)
			grouped.Signals = append(grouped.Signals, inspect.Signal{Kind: inspect.SignalDirectoryBoundary})
			grouped.Ambiguous = true
		}
		return grouped, true
	}
}

func unitSizes(units []inspect.Unit) []int {
	sizes := make([]int, len(units))
	for i, unit := range units {
		sizes[i] = len(unit.Media)
	}
	return sizes
}

func runTagged(t *testing.T, tr *tree, tags inspect.TagGrouper) []inspect.Unit {
	t.Helper()
	session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{Tags: tags})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return session.Units
}

func TestSplitByTags_TwoAlbumsInOneFlatFolderWithUniqueNumbersAreTwoUnits(t *testing.T) {
	tr := newTree(t, "flat/a1.mp3", "flat/a2.mp3", "flat/b1.mp3", "flat/b2.mp3", "flat/b3.mp3")
	tags := keyedGrouper(inspect.SignalTagAgreement, map[string]string{
		"flat/a1.mp3": "Alpha", "flat/a2.mp3": "Alpha", "flat/b1.mp3": "Beta", "flat/b2.mp3": "Beta", "flat/b3.mp3": "Beta",
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

func TestSplitByTags_UntaggedFilesBesideSeveralAlbumsAreAmbiguousAndKept(t *testing.T) {
	tr := newTree(t, "flat/a1.mp3", "flat/b1.mp3", "flat/x.mp3")
	tags := keyedGrouper(inspect.SignalTagAgreement, map[string]string{"flat/a1.mp3": "Alpha", "flat/b1.mp3": "Beta"})
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

func TestSplitByTags_AGrouperThatLosesOrRepeatsAFileIsNotTrusted(t *testing.T) {
	tr := newTree(t, "flat/a1.mp3", "flat/a2.mp3", "flat/b1.mp3")
	for name, grouped := range map[string]inspect.TagGroups{
		"loses":    {Groups: [][]int{{0}, {1}}, Signals: []inspect.Signal{{}, {}}},
		"repeats":  {Groups: [][]int{{0, 1}, {1, 2}}, Signals: []inspect.Signal{{}, {}}},
		"swaps":    {Groups: [][]int{{0, 0}, {2}}, Signals: []inspect.Signal{{}, {}}},
		"overruns": {Groups: [][]int{{0, 1}, {3}}, Signals: []inspect.Signal{{}, {}}},
		"negative": {Groups: [][]int{{0, 1}, {-1}}, Signals: []inspect.Signal{{}, {}}},
		"unsigned": {Groups: [][]int{{0, 1}, {2}}, Signals: []inspect.Signal{{}}},
	} {
		units := runTagged(t, tr, func([]string) (inspect.TagGroups, bool) { return grouped, true })
		if got := unitSizes(units); len(got) != 1 || got[0] != 3 {
			t.Errorf("%s: unit sizes = %v, want the whole folder as one unit", name, got)
		}
	}
}

func TestWalk_TagsForReadsEachClusterAndAFailureFallsBackToNoTags(t *testing.T) {
	tr := newTree(t, "flat/a1.mp3", "flat/a2.mp3", "flat/b1.mp3")
	grouper := keyedGrouper(inspect.SignalTagAgreement, map[string]string{"flat/a1.mp3": "Alpha", "flat/a2.mp3": "Alpha", "flat/b1.mp3": "Beta"})
	var asked []string
	for _, tc := range []struct {
		name string
		read func(context.Context, []string) (inspect.TagGrouper, error)
		want int
	}{
		{"reads", func(_ context.Context, relPaths []string) (inspect.TagGrouper, error) {
			asked = relPaths
			return grouper, nil
		}, 2},
		{"fails", func(context.Context, []string) (inspect.TagGrouper, error) { return nil, errors.New("no ffprobe") }, 1},
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

func TestRun_DiscFoldersWithATitleOrAnotherMediumMerge(t *testing.T) {
	for _, pair := range [][2]string{
		{"CD1 - Live in Paris", "CD2 - Encore"}, {"Disc 1", "Disc 2 (Bonus)"}, {"DVD1", "DVD2"}, {"BD 1", "BD 2"},
		{"Digital Media 01", "Digital Media 02"}, {"Side A", "Side B"},
	} {
		tr := newTree(t, "Box/"+pair[0]+"/01.flac", "Box/"+pair[1]+"/01.flac")
		session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
		if err != nil {
			t.Fatalf("%v: Run: %v", pair, err)
		}
		if len(session.Units) != 1 {
			t.Errorf("%v: %d units, want 1 merged set", pair, len(session.Units))
		}
	}
}

func TestRun_FoldersThatOnlyLookLikeDiscsStayApart(t *testing.T) {
	for _, name := range []string{"CD Singles", "Volume", "Disco", "Side Project"} {
		tr := newTree(t, "Artist/"+name+"/01.flac", "Artist/Other Album/01.flac")
		session, err := inspect.Run(t.Context(), tr.fsys, root, inspect.Options{})
		if err != nil {
			t.Fatalf("%s: Run: %v", name, err)
		}
		if len(session.Units) != 2 {
			t.Errorf("%s: %d units, want 2 separate albums", name, len(session.Units))
		}
	}
}
