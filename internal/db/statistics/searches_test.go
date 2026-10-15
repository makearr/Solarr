package statistics_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
)

// Searches are counted per library since a time with how many found something; retention removes the old ones.
func TestCandidates_SearchesAreCountedPerLibraryAndRetired(t *testing.T) {
	candidates := statistics.NewCandidates(openStatistics(t))
	ctx := t.Context()
	for _, s := range []struct {
		category int64
		found    bool
		at       int64
	}{{1, false, 100}, {1, true, 2_000}, {1, false, 3_000}, {2, true, 3_000}} {
		if err := candidates.RecordSearch(ctx, s.category, 9, s.found, time.Unix(s.at, 0)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := candidates.SearchesByCategory(ctx, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	figure := func(category int64, key string, n int64) statistics.Figure {
		return statistics.Figure{Metric: library.StatSearches, Dimension: statistics.Dimension(category, key), Value: float64(n), Count: n}
	}
	want := []statistics.Figure{
		figure(1, library.SearchesKeySearched, 2), figure(1, library.SearchesKeyFound, 1),
		figure(2, library.SearchesKeySearched, 1), figure(2, library.SearchesKeyFound, 1),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("searches = %+v, want %+v", got, want)
	}
	retired, err := candidates.Retire(ctx, time.Unix(5_000, 0), time.Unix(2_500, 0), 10)
	if err != nil || retired.Searches != 2 {
		t.Fatalf("retired %+v, %v; want the two searches before 2500", retired, err)
	}
	if left, _ := candidates.SearchesByCategory(ctx, 0); len(left) != 4 || left[0].Count != 1 {
		t.Errorf("after retention %+v, want one search left in each library", left)
	}
}
