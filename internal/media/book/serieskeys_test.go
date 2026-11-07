package book

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type fakeSeriesKeys struct {
	books []wikidata.SeriesBook
	parts map[string][]string
	err   error
}

func (f fakeSeriesKeys) BookSeries(_ context.Context, ids []string) ([]wikidata.SeriesBook, error) {
	var out []wikidata.SeriesBook
	for _, book := range f.books {
		if slices.ContainsFunc(book.OpenLibrary, func(id string) bool { return slices.Contains(ids, id) }) {
			out = append(out, book)
		}
	}
	return out, f.err
}

func (f fakeSeriesKeys) ItemFranchises(context.Context, []string) (map[string][]string, error) {
	return f.parts, nil
}

func TestSeriesKeysPassDeclaresTheSeriesItemAndItsFranchisesOnlyWhereTheSeriesIsTheCollections(t *testing.T) {
	opened := openFollowLibrary(t)
	franchises := library.NewFranchises(opened)
	works := map[string]int64{}
	for _, id := range []string{"OL1W", "OL2W", "OL3W", "OL4W", "OL5W"} {
		stored, err := library.NewSeries(opened).Upsert(t.Context(), clock.NewFakeAtEpoch().Now(), library.SeriesWrite{
			Work: library.WorkIdentity{MediaType: Ebook.MediaType, CanonicalTitle: id, SortTitle: id},
			IDs:  []library.ExternalIDWrite{{Provider: ProviderOpenLibrary, ExternalID: id}},
		})
		if err != nil {
			t.Fatal(err)
		}
		works[id] = stored.WorkID
	}
	join := func(name string, ids ...string) int64 {
		var collection int64
		for _, id := range ids {
			joined, err := franchises.Join(t.Context(), Ebook.MediaType, ProviderSeries, PersonKey(name), name, works[id])
			if err != nil {
				t.Fatal(err)
			}
			collection = joined
		}
		return collection
	}
	// Named like its Wikidata series; most books in a series named otherwise; one book of three in a franchise's series.
	lantern := join("Lantern Cycle", "OL1W")
	ember := join("The Ember Books", "OL2W", "OL3W")
	mixed := join("Assorted Tales", "OL4W", "OL5W")
	source := fakeSeriesKeys{
		books: []wikidata.SeriesBook{
			{SeriesQID: "Q10", SeriesName: "Lantern Cycle", QID: "Q11", OpenLibrary: []string{"OL1W"}},
			{SeriesQID: "Q20", SeriesName: "Ember Gate series", QID: "Q21", OpenLibrary: []string{"OL2W"}},
			{SeriesQID: "Q20", SeriesName: "Ember Gate series", QID: "Q22", OpenLibrary: []string{"OL3W"}},
			{SeriesQID: "Q30", SeriesName: "Vexmoor novels", QID: "Q31", OpenLibrary: []string{"OL4W"}},
		},
		parts: map[string][]string{"Q10": {"Q90"}, "Q30": {"Q91"}},
	}
	pass := SeriesKeysPass{Formats: []Format{Ebook}, Source: source, Franchises: franchises, Logger: slog.New(slog.DiscardHandler)}
	if err := pass.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatal(err)
	}
	candidates, err := franchises.LinkCandidates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64][]string{}
	for _, candidate := range candidates {
		got[candidate.ID] = wikidataKeys(candidate.Keys)
	}
	want := map[int64][]string{lantern: {"Q10", "Q90"}, ember: {"Q20"}, mixed: nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("declared wikidata keys = %v, want %v", got, want)
	}

	// A busy Wikidata is retried and declares nothing.
	pass.Source = fakeSeriesKeys{err: errors.New("busy")}
	if err := pass.Run(t.Context(), jobs.Job{}); err == nil {
		t.Error("a failing Wikidata is an error to retry")
	}
}

// wikidataKeys are the Wikidata items among a collection's keys.
func wikidataKeys(keys []string) []string {
	items := library.ProviderItems(keys, ProviderWikidata)
	slices.Sort(items)
	return items
}
