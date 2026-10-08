package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

var tvFacets = []library.WorkFacet{
	{Key: "year", Source: library.FacetColumn, Name: "year", Kind: listfilter.Number},
	{Key: "status", Source: library.FacetField, Name: "status", Kind: listfilter.Enum, Choices: []string{"continuing", "ended", "upcoming"}},
	{Key: "network", Source: library.FacetOrganisation, Name: "network", Kind: listfilter.Text},
	{Key: "genre", Source: library.FacetClassification, Name: "genre", Kind: listfilter.Text},
	{Key: "runtime", Source: library.FacetColumn, Name: "runtime_ms", Kind: listfilter.Number},
}

// A series is listed by the works list like an album: its facets read from where its type declares them, a custom
// filter on them.
func TestTheWorksListServesASeriesWithItsFacets(t *testing.T) {
	opened, _ := newLibrary(t)
	write := creditedSeries()
	write.Fields = []library.SeriesFieldWrite{{Field: "status", Provider: "skyhook", Value: "ended"}, {Field: "status", Provider: "__manual__", Value: "continuing"}}
	result, err := library.NewSeries(opened).Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	works := library.NewWorks(opened)
	list := func(where string) []library.WorkListRow {
		t.Helper()
		conditions, err := listfilter.Parse(where, library.FacetFields(library.WorkFields, tvFacets))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := works.List(t.Context(), library.WorkFilter{MediaType: "tv", CategoryIDs: []int64{4}, Where: conditions, Facets: tvFacets}, library.WorkCursor{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rows := list("")
	if len(rows) != 1 || rows[0].ID != result.WorkID || !rows[0].Monitored {
		t.Fatalf("rows = %+v; want the monitored series", rows)
	}
	values, err := works.FacetValues(t.Context(), tvFacets, []int64{result.WorkID})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"status": {"continuing"}, "network": {"Example Net", "Other Net"}, "genre": {"Drama"}, "runtime": {"2400000"},
	}
	if got := values[result.WorkID]; !reflect.DeepEqual(got, want) {
		t.Errorf("facets = %v, want %v (the user's own status over the provider's)", got, want)
	}
	for where, count := range map[string]int{
		`[{"field":"network","op":"contains","value":"other"}]`:     1,
		`[{"field":"network","op":"is_not","value":"Example Net"}]`: 0,
		`[{"field":"status","op":"is","value":"continuing"}]`:       1,
		`[{"field":"status","op":"is","value":"ended"}]`:            0,
		`[{"field":"genre","op":"is","value":"drama"}]`:             1,
		`[{"field":"runtime","op":"ge","value":"3000000"}]`:         0,
	} {
		if got := len(list(where)); got != count {
			t.Errorf("%s: %d works, want %d", where, got, count)
		}
	}
}
