package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestSameContentSuggestionsNeedMoreThanOneTitleOnOneDay(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	desired := library.NewDesired(opened)
	minutes := func(m int64) *int64 { v := m * 60_000; return &v }
	add := func(title, id string, parts ...library.SeriesPartWrite) {
		write := sampleSeries()
		write.Work.CanonicalTitle, write.Work.SortTitle = title, title
		write.IDs = []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: id}}
		write.Parts = parts
		result, err := series.Upsert(t.Context(), seriesAt, write)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", SortKey: "s01", CategoryID: 1, ProfileID: 1, Monitored: true}); err != nil {
			t.Fatal(err)
		}
	}
	part := func(ref, title string, runtime *int64) library.SeriesPartWrite {
		return library.SeriesPartWrite{MediaRef: ref, SortKey: ref, Title: title, RuntimeMs: runtime, Date: "2016-03-31", MonitoredIfNew: true}
	}
	add("Show A", "1", part("s01:e05", "Episode 5", minutes(45)), part("s01:e06", "The Big Crossover", minutes(44)), part("s01:e07", "Long Special Night", minutes(40)))
	add("Show B", "2", part("s01:e05", "Episode 5", minutes(45)), part("s01:e06", "The Big Crossover", minutes(45)), part("s01:e07", "Long Special Night", minutes(95)))

	found, err := library.NewCrossovers(opened).Suggestions(t.Context(), "tv", 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Title != "The Big Crossover" {
		t.Errorf("suggestions = %+v; want only the specific title of about the same length (not \"Episode 5\", not a 40 and a 95 minute episode)", found)
	}
}
