package library_test

import (
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestPartOut_WaitsOutTheGraceOfItsConfidence(t *testing.T) {
	airs := time.Date(2026, 3, 10, 20, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                    string
		at                      int64
		confidence, date, basis string
		after                   time.Duration
		want                    bool
	}{
		{"a sure time, an hour on", airs.Unix(), "high", "2026-03-10", "", time.Hour, false},
		{"a sure time, past its two hours", airs.Unix(), "high", "2026-03-10", "", 2 * time.Hour, true},
		{"a likely time, past two hours", airs.Unix(), "medium", "2026-03-10", "", 3 * time.Hour, false},
		{"a likely time, past its twelve", airs.Unix(), "medium", "2026-03-10", "", 12 * time.Hour, true},
		{"a day alone, that evening", 0, "low", "2026-03-10", "", 0, false},
		{"a day alone, the next afternoon", 0, "low", "2026-03-10", "", 16 * time.Hour, true},
		{"a year alone, begun", 0, "unknown", "2026", "", 0, true},
		{"announced with no date", 0, "unknown", "", library.AvailabilityUnannounced, 0, false},
		{"never had a time: a track", 0, "unknown", "", "", 0, true},
	} {
		if got := library.PartOut(tc.at, tc.confidence, tc.date, tc.basis, airs.Add(tc.after)); got != tc.want {
			t.Errorf("%s: out = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A season owes only what has aired, and nothing is written when an episode airs: the re-count is what notices.
func TestRecountComingOut_ASeasonOwesAnEpisodeOnceItIsOut(t *testing.T) {
	airs := time.Date(2026, 3, 10, 20, 0, 0, 0, time.UTC)
	fake := clock.NewFake(airs.Add(-time.Hour))
	opened, queries := newLibraryAt(t, fake)
	write := sampleSeries()
	at := airs.Unix()
	write.Parts = []library.SeriesPartWrite{
		{MediaRef: "s01:e01", SortKey: "s01:e01", MonitoredIfNew: true, Date: "2026-03-03"},
		{MediaRef: "s01:e02", SortKey: "s01:e02", MonitoredIfNew: true, Date: "2026-03-10", AvailableAt: &at, AvailableConfidence: "high"},
		{MediaRef: "s01:e03", SortKey: "s01:e03", MonitoredIfNew: true, Date: "2026-03-17"},
		{MediaRef: "s01:e04", SortKey: "s01:e04", MonitoredIfNew: true, AvailableBasis: library.AvailabilityUnannounced},
	}
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	desired := library.NewDesired(opened)
	season, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{
		WorkID: result.WorkID, MediaRef: "s01", SortKey: "s01", CategoryID: 1, ProfileID: 1, Monitored: true,
	})
	if err != nil || season.RequiredCount.Int64 != 1 {
		t.Fatalf("season = %+v, %v; want only the aired first episode owed", season, err)
	}

	var owed []int64
	for _, after := range []time.Duration{time.Hour, 3 * time.Hour, 3*time.Hour + time.Minute, 9 * 24 * time.Hour} {
		fake.Set(airs.Add(after))
		changed, err := desired.RecountComingOut(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		item, err := queries.GetDesiredItem(t.Context(), season.ID)
		if err != nil {
			t.Fatal(err)
		}
		owed = append(owed, int64(changed), item.RequiredCount.Int64)
	}
	// Within the grace nothing changes; past it the second is owed once; a week on the third, never the unannounced.
	if want := []int64{0, 1, 1, 2, 0, 2, 1, 3}; !slices.Equal(owed, want) {
		t.Errorf("changed and owed after each step = %v, want %v", owed, want)
	}
}
