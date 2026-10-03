package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func monitoringFixture(t *testing.T) (library.Desired, library.SeriesResult, func(string) int64) {
	t.Helper()
	opened, _ := newLibrary(t)
	write := library.SeriesWrite{
		Work: library.WorkIdentity{MediaType: "tv", CanonicalTitle: "Monitoring", SortTitle: "monitoring"},
		IDs:  []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "77"}},
		Parts: []library.SeriesPartWrite{
			{MediaRef: "s01:e01", SortKey: "s0001e0001"}, {MediaRef: "s01:e02", SortKey: "s0001e0002"}, {MediaRef: "s02:e01", SortKey: "s0002e0001"},
		},
	}
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	desired := library.NewDesired(opened)
	for _, ref := range []string{"s01", "s02"} {
		if _, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: ref, CategoryID: 1, ProfileID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	monitoredItem := func(ref string) int64 {
		var monitored int64
		if err := opened.Read().QueryRowContext(t.Context(),
			`SELECT monitored FROM desired_item WHERE work_id = ? AND media_ref = ?`, result.WorkID, ref).Scan(&monitored); err != nil {
			t.Fatal(err)
		}
		return monitored
	}
	return desired, result, monitoredItem
}

func TestWantingOneEpisodeMonitorsItsSeasonAndDroppingTheLastUnmonitorsIt(t *testing.T) {
	desired, result, monitoredItem := monitoringFixture(t)
	if monitoredItem("s01") != 0 {
		t.Fatal("the season starts unmonitored")
	}
	if err := desired.SetPartMonitored(t.Context(), result.WorkID, result.PartIDs["s01:e02"], true); err != nil {
		t.Fatal(err)
	}
	if monitoredItem("s01") != 1 || monitoredItem("s02") != 0 {
		t.Errorf("s01 = %d, s02 = %d: wanting one episode must make its season visible to the planner and only that season", monitoredItem("s01"), monitoredItem("s02"))
	}
	if err := desired.SetPartMonitored(t.Context(), result.WorkID, result.PartIDs["s01:e02"], false); err != nil {
		t.Fatal(err)
	}
	if monitoredItem("s01") != 0 {
		t.Errorf("s01 = %d, want unmonitored once no episode of it is wanted", monitoredItem("s01"))
	}
}

func TestApplyPartMonitoringSetsPartsAndSeasonsTogether(t *testing.T) {
	desired, result, monitoredItem := monitoringFixture(t)
	wanted := map[int64]bool{result.PartIDs["s01:e01"]: true, result.PartIDs["s01:e02"]: false, result.PartIDs["s02:e01"]: false}
	if err := desired.ApplyPartMonitoring(t.Context(), result.WorkID, wanted); err != nil {
		t.Fatal(err)
	}
	if monitoredItem("s01") != 1 || monitoredItem("s02") != 0 {
		t.Errorf("s01 = %d, s02 = %d", monitoredItem("s01"), monitoredItem("s02"))
	}
	parts, err := desired.AllParts(t.Context(), result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, part := range parts {
		got[part.MediaRef] = part.Monitored
	}
	if got["s01:e01"] != 1 || got["s01:e02"] != 0 || got["s02:e01"] != 0 {
		t.Errorf("parts = %v", got)
	}
}
