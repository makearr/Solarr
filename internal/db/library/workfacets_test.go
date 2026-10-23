package library_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

var tvFacets = []library.WorkFacet{
	{Key: "year", Source: library.FacetColumn, Name: "year", Kind: listfilter.Number},
	{Key: "status", Source: library.FacetField, Name: "status", Kind: listfilter.Enum, Choices: []string{"continuing", "ended", "upcoming"}},
	{Key: "network", Source: library.FacetOrganisation, Name: "network", Kind: listfilter.Text},
	{Key: "genre", Source: library.FacetTaxonomy, Name: "screen", Kind: listfilter.Text},
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

// A facet orders the list both ways, page by page: works without a value come first ascending, as "" sorts.
func TestTheWorksListOrdersByAFacet(t *testing.T) {
	opened, _ := newLibrary(t)
	works := library.NewWorks(opened)
	statuses := map[string]string{"Alpha": "upcoming", "Bravo": "continuing", "Charlie": "ended", "Delta": ""}
	for title, status := range statuses {
		work := createWork(t, opened, "tv", title)
		createDesiredInCategory(t, opened, work.ID, 4)
		if status != "" {
			if err := works.SetManualField(t.Context(), seriesAt, work.ID, "status", status); err != nil {
				t.Fatal(err)
			}
		}
	}
	order := func(direction library.SortDirection) []string {
		t.Helper()
		var titles []string
		var after library.WorkCursor
		for range 6 {
			rows, err := works.List(t.Context(), library.WorkFilter{
				CategoryIDs: []int64{4}, Sort: library.WorkSortFacet, SortFacet: tvFacets[1], Direction: direction,
			}, after, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				break
			}
			titles = append(titles, rows[0].CanonicalTitle)
			after = library.WorkCursor{SortText: rows[0].SortText, ID: rows[0].ID}
		}
		return titles
	}
	if got, want := order(library.SortAscending), []string{"Delta", "Bravo", "Charlie", "Alpha"}; !reflect.DeepEqual(got, want) {
		t.Errorf("by status ascending = %v, want %v", got, want)
	}
	if got, want := order(library.SortDescending), []string{"Alpha", "Charlie", "Bravo", "Delta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("by status descending = %v, want %v", got, want)
	}
}

// The list's SQL and the reader's Go pick a field's value by the one rule: the newest provider value, not the first.
func TestFieldFacetsPickTheNewestValueAsTheReaderDoes(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	write := creditedSeries()
	write.Fields = []library.SeriesFieldWrite{{Field: "status", Provider: "skyhook", Value: "ended"}}
	result, err := series.Upsert(t.Context(), seriesAt, write)
	if err != nil {
		t.Fatal(err)
	}
	write.Fields = []library.SeriesFieldWrite{{Field: "status", Provider: "tmdb", Value: "continuing"}}
	if _, err := series.Upsert(t.Context(), seriesAt.Add(time.Hour), write); err != nil {
		t.Fatal(err)
	}
	if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: 4, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	values, err := library.NewWorks(opened).FacetValues(t.Context(), tvFacets, []int64{result.WorkID})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := library.NewSeriesReader(opened).Get(t.Context(), 4, result.WorkID, seriesAt)
	if err != nil {
		t.Fatal(err)
	}
	if got, read := values[result.WorkID]["status"], detail.Summary.Fields["status"]; !reflect.DeepEqual(got, []string{"continuing"}) || read != "continuing" {
		t.Errorf("status: list %v, reader %q; want the newer continuing in both", got, read)
	}
}
