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

type fakeLister []ListedBook

func (f fakeLister) ListSeries(context.Context, string) ([]ListedBook, error) { return f, nil }

// The provider's list completes Wikidata's: a book Wikidata gives no Open Library id is added by the provider's id, a
// book only the provider knows is listed too, and the count is the whole series.
func TestASeriesIsCompletedFromTheBookProvidersList(t *testing.T) {
	opened := openFollowLibrary(t)
	now := clock.NewFakeAtEpoch().Now()
	series := library.NewSeries(opened)
	if _, err := series.Upsert(t.Context(), now, library.SeriesWrite{
		Work:      library.WorkIdentity{MediaType: "book", CanonicalTitle: "The Lantern Keeper", SortTitle: "The Lantern Keeper"},
		IDs:       []library.ExternalIDWrite{{Provider: ProviderOpenLibrary, ExternalID: "OL1001W"}},
		Franchise: &library.SeriesFranchiseWrite{Provider: ProviderSeries, ExternalID: PersonKey("Lantern Cycle"), Name: "Lantern Cycle"},
	}); err != nil {
		t.Fatal(err)
	}
	summaries, err := library.NewFranchises(opened).Summaries(t.Context(), "book")
	if err != nil || len(summaries) != 1 {
		t.Fatalf("collections = %+v, %v", summaries, err)
	}
	books := SeriesBooks{
		Franchises: library.NewFranchises(opened), Reader: library.NewSeriesReader(opened), Source: &fakeSeries{},
		Lister: fakeLister{
			{ID: "1", Title: "The Lantern Keeper", Position: 1},
			{ID: "15", Title: "Tales of the Cycle", Year: 1999},
			{ID: "16", Title: "Book Five", Year: 2001, Position: 5},
		},
		Catalog: Catalog{Service: Service{Format: Ebook, Series: series, Exclusions: library.NewExclusions(opened)}},
	}
	missing, err := books.MissingFranchiseMembers(t.Context(), 5, summaries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, member := range missing {
		got = append(got, member.Provider+":"+member.ExternalID)
	}
	want := []string{ProviderOpenLibrary + ":OL1002W", ProviderOpenLibrary + ":OL1003W", ProviderOpenLibrary + ":OL1004W", ProviderHardcover + ":16", ProviderHardcover + ":15"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("missing = %v, want %v in series order", got, want)
	}
	facts := books.FranchiseFacts(t.Context(), []media.FranchiseRef{{ID: summaries[0].ID}})
	if facts[summaries[0].ID].Total != 6 {
		t.Errorf("series total = %+v, want 6 books in all", facts)
	}
}

// A collection lists the books in the languages its library prefers: a translation the provider lists is not part of the
// series there, a book that states no language is.
func TestASeriesLeavesOutBooksInLanguagesTheLibraryDoesNotPrefer(t *testing.T) {
	opened := openFollowLibrary(t)
	now := clock.NewFakeAtEpoch().Now()
	series := library.NewSeries(opened)
	if _, err := series.Upsert(t.Context(), now, library.SeriesWrite{
		Work:      library.WorkIdentity{MediaType: "book", CanonicalTitle: "The Lantern Keeper", SortTitle: "The Lantern Keeper"},
		IDs:       []library.ExternalIDWrite{{Provider: ProviderOpenLibrary, ExternalID: "OL1001W"}},
		Franchise: &library.SeriesFranchiseWrite{Provider: ProviderSeries, ExternalID: PersonKey("Lantern Cycle"), Name: "Lantern Cycle"},
	}); err != nil {
		t.Fatal(err)
	}
	summaries, err := library.NewFranchises(opened).Summaries(t.Context(), "book")
	if err != nil || len(summaries) != 1 {
		t.Fatalf("collections = %+v, %v", summaries, err)
	}
	books := SeriesBooks{
		Franchises: library.NewFranchises(opened), Reader: library.NewSeriesReader(opened), Source: &fakeSeries{},
		Lister: fakeLister{
			{ID: "20", Title: "Fuenf Buecher", Year: 2002, Position: 6, Language: "de"},
			{ID: "21", Title: "Book Seven", Year: 2003, Position: 7, Language: "en"},
			{ID: "22", Title: "Book Eight", Year: 2004, Position: 8},
		},
		Languages: func(int64) []string { return []string{"en"} },
		Catalog:   Catalog{Service: Service{Format: Ebook, Series: series, Exclusions: library.NewExclusions(opened)}},
	}
	missing, err := books.MissingFranchiseMembers(t.Context(), 5, summaries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, member := range missing {
		if member.Provider == ProviderHardcover {
			got = append(got, member.ExternalID)
		}
	}
	if want := []string{"21", "22"}; !reflect.DeepEqual(got, want) {
		t.Errorf("listed books = %v, want %v: the German one is another language", got, want)
	}
}
