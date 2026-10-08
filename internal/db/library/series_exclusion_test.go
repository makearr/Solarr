package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestADeletedSeriesIsGoneWithItsPartsAndCanBeExcluded(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	exclusions := library.NewExclusions(opened)
	result, err := series.Upsert(t.Context(), seriesAt, creditedSeries())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1}); err != nil {
		t.Fatal(err)
	}
	ids := []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: "1234"}, {Provider: "tvmaze", ExternalID: "42"}}
	if excluded, err := exclusions.Excluded(t.Context(), ids); err != nil || excluded {
		t.Fatalf("excluded before = %v, err = %v", excluded, err)
	}
	if err := exclusions.Exclude(t.Context(), seriesAt, "tv", "Example Show", ids); err != nil {
		t.Fatal(err)
	}
	if err := series.DeleteSeries(t.Context(), result.WorkID); err != nil {
		t.Fatal(err)
	}

	var works, parts, items int
	row := opened.Read().QueryRowContext(t.Context(),
		`SELECT (SELECT COUNT(*) FROM work WHERE id = ?), (SELECT COUNT(*) FROM work_part WHERE work_id = ?), (SELECT COUNT(*) FROM desired_item WHERE work_id = ?)`,
		result.WorkID, result.WorkID, result.WorkID)
	if err := row.Scan(&works, &parts, &items); err != nil || works != 0 || parts != 0 || items != 0 {
		t.Errorf("works = %d, parts = %d, items = %d, err = %v; want nothing left", works, parts, items, err)
	}
	if excluded, _ := exclusions.Excluded(t.Context(), []library.ExternalIDWrite{{Provider: "tvmaze", ExternalID: "42"}}); !excluded {
		t.Error("the series is excluded by any one of its ids")
	}
	list, err := exclusions.List(t.Context(), "tv")
	if err != nil || len(list) != 2 || list[0].Title != "Example Show" {
		t.Errorf("exclusions = %+v, err = %v", list, err)
	}
	if err := exclusions.Unexclude(t.Context(), ids); err != nil {
		t.Fatal(err)
	}
	if excluded, _ := exclusions.Excluded(t.Context(), ids); excluded {
		t.Error("still excluded after unexclude")
	}
}
