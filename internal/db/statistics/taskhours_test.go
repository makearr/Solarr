package statistics_test

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/statistics"
)

// A run that spans hours is split over them, counted once where it began; what is flushed survives a new instance.
func TestTaskHours_SplitARunOverTheHoursItSpannedAndKeepThem(t *testing.T) {
	opened := openStatistics(t)
	hours := statistics.NewTaskHours(opened)
	ctx := t.Context()
	start := time.Date(2026, 11, 10, 9, 50, 0, 0, time.UTC)
	hours.RecordRun("library.scan", start, start.Add(25*time.Minute))
	hours.RecordRun("library.scan", start.Add(time.Hour), start.Add(time.Hour+time.Minute))
	hours.RecordRun("import.run", start, start.Add(2*time.Second))

	before, err := hours.Since(ctx, start.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := hours.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := statistics.NewTaskHours(opened).Since(ctx, start.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range [][]statistics.TaskHour{before, after} {
		sort.Slice(got, func(i, j int) bool {
			if !got[i].HourStart.Equal(got[j].HourStart) {
				return got[i].HourStart.Before(got[j].HourStart)
			}
			return got[i].JobType < got[j].JobType
		})
		nine, ten := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
		want := []statistics.TaskHour{
			{JobType: "import.run", HourStart: nine, RunMs: 2000, Runs: 1},
			{JobType: "library.scan", HourStart: nine, RunMs: 10 * 60 * 1000, Runs: 1},
			{JobType: "library.scan", HourStart: ten, RunMs: 16 * 60 * 1000, Runs: 1},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("hours = %+v, want %+v", got, want)
		}
	}

	if err := hours.Prune(ctx, time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	left, err := hours.Since(ctx, start.Add(-24*time.Hour))
	if err != nil || len(left) != 1 || left[0].JobType != "library.scan" {
		t.Fatalf("after pruning: %+v, %v; want only the 10:00 scan hour", left, err)
	}
}
