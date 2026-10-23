package library_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// A move takes a work's items to the target with its profile, planned again; a copy gives the target the same items,
// unmonitored, covered by the same files; a move over an earlier copy leaves one set of items (plan 10-libraries L3).
func TestLibraryMove_MovesAndCopiesAWorksItems(t *testing.T) {
	opened, queries := newLibrary(t)
	desired := library.NewDesired(opened)
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, sampleSeries())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 1, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	summary := func() []string {
		items, err := queries.ListDesiredItemsForWork(t.Context(), result.WorkID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, item := range items {
			out = append(out, fmt.Sprintf("%d:%s:%d:%d", item.CategoryID, item.MediaRef, item.ProfileID, item.Monitored))
		}
		slices.Sort(out)
		return out
	}

	if copied, err := desired.CopyWorkToLibrary(t.Context(), result.WorkID, 1, 2, 5); err != nil || copied != 1 {
		t.Fatalf("CopyWorkToLibrary = %d, %v; want 1", copied, err)
	}
	if got, want := summary(), []string{"1:s01:1:1", "2:s01:5:0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after the copy: %v, want %v (the copy unmonitored, the target's profile)", got, want)
	}
	if again, err := desired.CopyWorkToLibrary(t.Context(), result.WorkID, 1, 2, 5); err != nil || again != 0 {
		t.Errorf("a second copy added %d, %v; want nothing", again, err)
	}

	if moved, err := desired.MoveWorkToLibrary(t.Context(), result.WorkID, 1, 3, 4); err != nil || moved != 1 {
		t.Fatalf("MoveWorkToLibrary = %d, %v; want 1", moved, err)
	}
	if got, want := summary(), []string{"2:s01:5:0", "3:s01:4:1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after the move: %v, want %v", got, want)
	}
	if _, err := desired.MoveWorkToLibrary(t.Context(), result.WorkID, 3, 2, 4); err != nil {
		t.Fatal(err)
	}
	if got, want := summary(), []string{"2:s01:5:0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a move onto its own copy: %v, want the one set of items there", got)
	}
}
