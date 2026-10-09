package statistics_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func openStatistics(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{Name: "statistics", Path: filepath.Join(directory, "statistics.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Statistics()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}
	return opened
}

// A rollup replaces its metrics whole, and a library reads only its own figures.
func TestRollup_ReplacesAndReadsPerLibrary(t *testing.T) {
	rollup := statistics.NewRollup(openStatistics(t))
	if empty, err := rollup.Empty(t.Context()); err != nil || !empty {
		t.Fatalf("a new rollup: empty %v, %v", empty, err)
	}
	figures := []statistics.Figure{
		{Metric: "albums_by_state", Dimension: statistics.Dimension(1, "MISSING"), Value: 1, Count: 1},
		{Metric: "albums_by_state", Dimension: statistics.Dimension(10, "COMPLETE"), Value: 4, Count: 4},
	}
	for range 2 {
		if err := rollup.Replace(t.Context(), []string{"albums_by_state"}, figures, 5_000); err != nil {
			t.Fatal(err)
		}
	}
	got, err := rollup.ForCategory(t.Context(), "albums_by_state", 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []statistics.CategoryFigure{{Key: "MISSING", Value: 1, Count: 1, ComputedAt: 5_000}}; !reflect.DeepEqual(got, want) {
		t.Errorf("library 1 = %+v, want %+v (not doubled, not library 10's)", got, want)
	}
}

// Only artists with albums missing are listed, most missing first; a library's rows are replaced, not added to.
func TestRollup_IncompleteDiscographiesMostMissingFirst(t *testing.T) {
	rollup := statistics.NewRollup(openStatistics(t))
	rows := []statistics.DiscographyCoverage{{PersonID: 7, Held: 1, Total: 2}, {PersonID: 8, Held: 1, Total: 6}, {PersonID: 9, Held: 4, Total: 4}}
	for range 2 {
		if err := rollup.SetDiscographyCoverage(t.Context(), 1, rows, 9_000); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := rollup.IncompleteDiscographies(t.Context(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []statistics.DiscographyCoverage{{PersonID: 8, Held: 1, Total: 6}, {PersonID: 7, Held: 1, Total: 2}}; !reflect.DeepEqual(listed, want) {
		t.Errorf("incomplete = %+v, want %+v", listed, want)
	}
}
