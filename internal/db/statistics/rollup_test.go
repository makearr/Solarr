package statistics_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// A metric with a row per artist is stored in 200-row writes, and a replace still leaves exactly the new rows (K-9).
func TestRollup_ReplacesALargeMetricInChunks(t *testing.T) {
	opened := openStatistics(t)
	rollup := statistics.NewRollup(opened)
	figures := func(n int) []statistics.Figure {
		list := make([]statistics.Figure, n)
		for i := range list {
			list[i] = statistics.Figure{Metric: "coverage", Dimension: statistics.Dimension(1, strconv.Itoa(i)), Value: 1, Count: 1}
		}
		return list
	}
	if err := rollup.ReplaceCategory(t.Context(), "coverage", 1, figures(450), 9); err != nil {
		t.Fatal(err)
	}
	before := opened.WritesRun()
	if err := rollup.ReplaceCategory(t.Context(), "coverage", 1, figures(420), 10); err != nil {
		t.Fatal(err)
	}
	if writes := opened.WritesRun() - before; writes != 3 {
		t.Errorf("replacing 420 figures ran %d writes, want 3 (200, 200, 20)", writes)
	}
	got, err := rollup.ForCategory(t.Context(), "coverage", 1)
	if err != nil || len(got) != 420 {
		t.Errorf("rows = %d, %v; want exactly the 420 new ones", len(got), err)
	}
}
