package book

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type fakeSeries struct{ asked [][]string }

func (f *fakeSeries) BookSeries(_ context.Context, ids []string) ([]wikidata.SeriesBook, error) {
	f.asked = append(f.asked, ids)
	return []wikidata.SeriesBook{
		{SeriesQID: "Q10", SeriesName: "Lantern Cycle", QID: "Q14", Title: "The Last Lantern", OpenLibrary: []string{"OL1004W"}, Ordinal: "4", Year: 1995},
		{SeriesQID: "Q10", SeriesName: "Lantern Cycle", QID: "Q11", Title: "The Lantern Keeper", OpenLibrary: []string{"OL1001W"}, Ordinal: "1", Year: 1987},
		{SeriesQID: "Q10", SeriesName: "Lantern Cycle", QID: "Q12", Title: "The Ember Gate", OpenLibrary: []string{"OL1002W", "OL1009W"}, Ordinal: "2", Year: 1989},
		{SeriesQID: "Q10", SeriesName: "Lantern Cycle", QID: "Q13", Title: "The Cinder Road", OpenLibrary: []string{"OL1003W"}, Ordinal: "3", Year: 1991},
		{SeriesQID: "Q10", SeriesName: "Lantern Cycle", QID: "Q15", Title: "Tales of the Cycle", Year: 1999},
		{SeriesQID: "Q20", SeriesName: "Collected Quell", QID: "Q11", Title: "The Lantern Keeper", OpenLibrary: []string{"OL1001W"}},
		{SeriesQID: "Q20", SeriesName: "Collected Quell", QID: "Q21", Title: "Another Book", OpenLibrary: []string{"OL1021W"}},
	}, nil
}

func TestASeriesMissingBooksAreThoseItHoldsUnderNoIDInSeriesOrder(t *testing.T) {
	opened := openFollowLibrary(t)
	now := clock.NewFakeAtEpoch().Now()
	series := library.NewSeries(opened)
	inSeries := func(title, openLibrary string) {
		t.Helper()
		if _, err := series.Upsert(t.Context(), now, library.SeriesWrite{
			Work:      library.WorkIdentity{MediaType: "book", CanonicalTitle: title, SortTitle: title},
			IDs:       []library.ExternalIDWrite{{Provider: ProviderOpenLibrary, ExternalID: openLibrary}},
			Franchise: &library.SeriesFranchiseWrite{Provider: ProviderSeries, ExternalID: PersonKey("Lantern Cycle"), Name: "Lantern Cycle"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	inSeries("The Lantern Keeper", "OL1001W")
	inSeries("The Ember Gate", "OL1009W")
	exclusions := library.NewExclusions(opened)
	if err := exclusions.Exclude(t.Context(), now, "book", "The Cinder Road", []library.ExternalIDWrite{{Provider: ProviderOpenLibrary, ExternalID: "OL1003W"}}); err != nil {
		t.Fatal(err)
	}
	summaries, err := library.NewFranchises(opened).Summaries(t.Context(), "book")
	if err != nil || len(summaries) != 1 {
		t.Fatalf("collections = %+v, %v; want the series", summaries, err)
	}
	source := &fakeSeries{}
	books := SeriesBooks{
		Franchises: library.NewFranchises(opened), Reader: library.NewSeriesReader(opened), Source: source,
		Catalog: Catalog{Service: Service{Format: Ebook, Series: series, Exclusions: exclusions}},
	}
	missing, err := books.MissingFranchiseMembers(t.Context(), 5, summaries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []media.FranchiseMember{{Title: "The Last Lantern", Year: 1995, Provider: ProviderOpenLibrary, ExternalID: "OL1004W"}}
	if !reflect.DeepEqual(missing, want) {
		t.Errorf("missing = %+v\nwant %+v: book 2 is held under its other id, 3 is excluded, the untitled-id one cannot be added, Q20 is another series", missing, want)
	}
	if len(source.asked) != 1 || len(source.asked[0]) != 2 {
		t.Errorf("asked = %v, want the two held books' ids", source.asked)
	}
}
