package comic

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/gcd"
)

type fakeGCD struct {
	series gcd.Series
	issues map[int]gcd.Issue
	asked  *[]int
}

func (fakeGCD) SearchSeries(context.Context, string, int) ([]gcd.Series, bool, error) {
	return nil, false, nil
}
func (f fakeGCD) Series(context.Context, int) (gcd.Series, error) { return f.series, nil }
func (f fakeGCD) Issue(_ context.Context, id int) (gcd.Issue, error) {
	*f.asked = append(*f.asked, id)
	return f.issues[id], nil
}

func TestAGCDOnlySeriesGetsItsIssuesWithoutVariantsAndWithTidyDates(t *testing.T) {
	var asked []int
	source := Sources{GCD: fakeGCD{
		series: gcd.Series{ID: 7, Name: "Lantern Wardens", YearBegan: 1963, IssueIDs: []int{1, 2, 3}},
		issues: map[int]gcd.Issue{
			1: {ID: 1, Number: "1", Title: "Pilot", KeyDate: "1963-03-00", OnSaleDate: "1963-02-14", Pages: 32},
			2: {ID: 2, Number: "1", VariantOfID: 1},
			3: {ID: 3, Number: "2 Annual", KeyDate: "1964-00-00"},
		},
		asked: &asked,
	}}
	series := &Series{IDs: map[string]string{ProviderGCD: "7"}}
	source.askGCD(t.Context(), series, Hint{})
	want := []Part{
		{Kind: PartIssue, Number: "1", Title: "Pilot", CoverDate: "1963-03", Store: "1963-02-14", Pages: 32, IDs: map[string]string{ProviderGCD: "1"}},
		{Kind: PartAnnual, Number: "2 Annual", CoverDate: "1964", IDs: map[string]string{ProviderGCD: "3"}},
	}
	if !reflect.DeepEqual(series.Parts, want) {
		t.Fatalf("parts = %+v\nwant %+v", series.Parts, want)
	}
	// Another source's list is never replaced by GCD's.
	other := &Series{IDs: map[string]string{ProviderGCD: "7"}, Parts: []Part{{Kind: PartIssue, Number: "9"}}}
	asked = nil
	source.askGCD(t.Context(), other, Hint{})
	if len(asked) != 0 {
		t.Fatalf("issues read although a list was held: %v", asked)
	}
}

// pagedGCD lists a name search over several pages and records which pages were read.
type pagedGCD struct {
	pages [][]gcd.Series
	read  *[]int
}

func (p pagedGCD) SearchSeries(_ context.Context, _ string, page int) ([]gcd.Series, bool, error) {
	*p.read = append(*p.read, page)
	if page > len(p.pages) {
		return nil, false, nil
	}
	return p.pages[page-1], page < len(p.pages), nil
}
func (p pagedGCD) Series(_ context.Context, id int) (gcd.Series, error) {
	return gcd.Series{ID: id, Name: "Wanted Tales", YearBegan: 1990}, nil
}
func (p pagedGCD) Issue(context.Context, int) (gcd.Issue, error) { return gcd.Issue{}, nil }

func TestAGCDNameSearchReadsLaterPagesUntilTheSeriesIsFound(t *testing.T) {
	var read []int
	source := Sources{GCD: pagedGCD{read: &read, pages: [][]gcd.Series{
		{{ID: 1, Name: "Wanted Tales Annual", YearBegan: 1990}},
		{{ID: 2, Name: "Wanted Tales", YearBegan: 1990}},
		{{ID: 3, Name: "Wanted Tales", YearBegan: 1990}},
	}}}
	series := &Series{IDs: map[string]string{}}
	source.askGCD(t.Context(), series, Hint{Title: "Wanted Tales", Year: 1990})
	if series.IDs[ProviderGCD] != "2" || !slices.Equal(read, []int{1, 2}) {
		t.Errorf("id %q after pages %v; want series 2 found on page 2 and page 3 never read", series.IDs[ProviderGCD], read)
	}
}

func TestAGCDNameSearchStopsAtThePageBound(t *testing.T) {
	var read []int
	pages := make([][]gcd.Series, 6)
	for i := range pages {
		pages[i] = []gcd.Series{{ID: i + 1, Name: "Other", YearBegan: 1990}}
	}
	found, err := (Sources{GCD: pagedGCD{read: &read, pages: pages}}).searchGCD(t.Context(), "x", nil)
	if err != nil || len(found) != 3 || !slices.Equal(read, []int{1, 2, 3}) {
		t.Errorf("found %d, pages %v, error %v; want 3 results from pages 1 to 3", len(found), read, err)
	}
}
