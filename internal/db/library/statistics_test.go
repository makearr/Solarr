package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

// The rollup is rebuilt each time, not added to, and a library reads only its own figures.
func TestStatistics_ComputeRebuildsAndReadsPerLibrary(t *testing.T) {
	opened, _ := newLibrary(t)
	statistics := library.NewStatistics(opened)
	work := createWork(t, opened, "music", "Album")
	createDesired(t, opened, work.ID, "SET", 10)
	other := createWork(t, opened, "music", "Elsewhere")
	createDesiredInCategory(t, opened, other.ID, 2)

	for range 2 {
		if err := statistics.Compute(t.Context(), map[int64]int64{}, 5_000); err != nil {
			t.Fatalf("Compute: %v", err)
		}
	}
	got, err := statistics.ForCategory(t.Context(), library.StatAlbumsByState, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "MISSING" || got[0].Count != 1 || got[0].ComputedAt != 5_000 {
		t.Fatalf("library 1 albums = %+v, want one MISSING computed at 5000, not doubled by the second run", got)
	}
	if other, _ := statistics.ForCategory(t.Context(), library.StatAlbumsByState, 3); len(other) != 0 {
		t.Errorf("a library with nothing got %+v", other)
	}
}
