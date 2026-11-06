package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestPlanCollectionLinks_LinksDifferentMediaTypesThatShareAFranchiseKey(t *testing.T) {
	candidates := []library.LinkCandidate{
		{ID: 1, MediaType: "tv", Members: 2, Keys: []string{"wikidata:Q1", "auto:1"}},
		{ID: 2, MediaType: "movie", Members: 3, Keys: []string{"tmdb:collection:9"}},
		{ID: 3, MediaType: "movie", Members: 1, Keys: []string{"wikidata:Q1"}},
		{ID: 4, MediaType: "tv", Members: 4, Keys: []string{"wikidata:Q1"}},
		{ID: 5, MediaType: "book", Members: 0, Keys: []string{"wikidata:Q1"}},
		{ID: 6, MediaType: "music", Members: 2, Keys: []string{"auto:1"}},
	}
	got := library.PlanCollectionLinks(candidates, map[[2]int64]bool{}, map[[2]string]bool{})
	want := []library.PlannedLink{
		{A: 1, B: 3, Reason: library.LinkReasonProvider, Detail: "wikidata:Q1"},
		{A: 3, B: 4, Reason: library.LinkReasonProvider, Detail: "wikidata:Q1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("planned = %+v, want %+v", got, want)
	}
}

func TestPlanCollectionLinks_SkipsLinkedAndUnlinkedPairs(t *testing.T) {
	candidates := []library.LinkCandidate{
		{ID: 1, MediaType: "tv", Members: 2, Keys: []string{"wikidata:Q1"}, Identity: []string{"wikidata:Q1"}},
		{ID: 2, MediaType: "movie", Members: 3, Keys: []string{"wikidata:Q1", "tmdb:collection:9"}, Identity: []string{"tmdb:collection:9"}},
	}
	if got := library.PlanCollectionLinks(candidates, map[[2]int64]bool{{1, 2}: true}, nil); len(got) != 0 {
		t.Errorf("a linked pair planned again: %+v", got)
	}
	if got := library.PlanCollectionLinks(candidates, nil, map[[2]string]bool{{"tmdb:collection:9", "wikidata:Q1"}: true}); len(got) != 0 {
		t.Errorf("an unlinked pair planned again: %+v", got)
	}
}

func TestAWorkIsInSeveralCollectionsAndEachListsIt(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	desired := library.NewDesired(opened)
	franchises := library.NewFranchises(opened)
	result, err := series.Upsert(t.Context(), seriesAt, sampleSeries())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := desired.EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", SortKey: "s01", CategoryID: 1, ProfileID: 1, Monitored: true}); err != nil {
		t.Fatal(err)
	}
	first, err := franchises.Join(t.Context(), "tv", "wikidata", "Q1", "Iron Man", result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := franchises.Join(t.Context(), "tv", "wikidata", "Q2", "Marvel Cinematic Universe", result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := franchises.InCategory(t.Context(), "tv", 1)
	if err != nil || len(listed) != 2 || first == second {
		t.Fatalf("collections = %+v, %v", listed, err)
	}
	for _, row := range listed {
		if len(row.Members) != 1 || row.Members[0].WorkID != result.WorkID {
			t.Errorf("collection %q members = %+v, want the one work", row.Name, row.Members)
		}
	}
}

func TestAutoLinkRecordsWhyAndAnUnlinkSticks(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	franchises := library.NewFranchises(opened)
	result, err := series.Upsert(t.Context(), seriesAt, sampleSeries())
	if err != nil {
		t.Fatal(err)
	}
	tv, err := franchises.Join(t.Context(), "tv", "wikidata", "Q42", "MacGyver", result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	films, err := franchises.Join(t.Context(), "movie", "wikidata", "Q42", "MacGyver", result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if tv == films {
		t.Fatalf("a franchise id another media type holds must give this one its own collection, got %d twice", tv)
	}

	pass := func() []library.PlannedLink {
		candidates, err := franchises.LinkCandidates(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		linked, refused, err := franchises.LinkState(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return library.PlanCollectionLinks(candidates, linked, refused)
	}
	planned := pass()
	if len(planned) != 1 || planned[0].Detail != "wikidata:Q42" {
		t.Fatalf("planned = %+v, want one link on wikidata:Q42", planned)
	}
	if made, err := franchises.AutoLink(t.Context(), planned[0]); err != nil || !made {
		t.Fatalf("AutoLink = %v, %v", made, err)
	}
	links, err := franchises.Links(t.Context(), films)
	want := []library.CollectionLink{{OtherID: tv, Reason: library.LinkReasonProvider, Detail: "wikidata:Q42"}}
	if err != nil || !reflect.DeepEqual(links, want) {
		t.Fatalf("links = %+v, %v; want %+v", links, err, want)
	}
	if again := pass(); len(again) != 0 {
		t.Errorf("a linked pair planned again: %+v", again)
	}

	if err := franchises.UnlinkCollections(t.Context(), tv, films); err != nil {
		t.Fatal(err)
	}
	if links, _ := franchises.Links(t.Context(), films); len(links) != 0 {
		t.Errorf("links after unlink = %+v", links)
	}
	if again := pass(); len(again) != 0 {
		t.Fatalf("an undone link planned again: %+v", again)
	}
	if made, err := franchises.AutoLink(t.Context(), planned[0]); err != nil || made {
		t.Errorf("AutoLink after an unlink = %v, %v; want it left alone", made, err)
	}

	if err := franchises.LinkCollections(t.Context(), tv, films); err != nil {
		t.Fatal(err)
	}
	links, _ = franchises.Links(t.Context(), tv)
	if len(links) != 1 || links[0].Reason != library.LinkReasonHand {
		t.Errorf("links after a hand link = %+v, want one by hand", links)
	}
}

func TestDeclaredKeysLinkACollectionWithAnotherProvidersFranchise(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	franchises := library.NewFranchises(opened)
	result, err := series.Upsert(t.Context(), seriesAt, sampleSeries())
	if err != nil {
		t.Fatal(err)
	}
	films, err := franchises.Join(t.Context(), "movie", "tmdb:collection", "10", "Star Wars Collection", result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := franchises.Join(t.Context(), "tv", "wikidata", "Q462", "Star Wars", result.WorkID); err != nil {
		t.Fatal(err)
	}
	if err := franchises.DeclareKeys(t.Context(), films, "wikidata", []string{"Q462"}); err != nil {
		t.Fatal(err)
	}
	candidates, err := franchises.LinkCandidates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	planned := library.PlanCollectionLinks(candidates, map[[2]int64]bool{}, map[[2]string]bool{})
	if len(planned) != 1 || planned[0].Detail != "wikidata:Q462" {
		t.Errorf("planned = %+v, want the declared franchise to link the two", planned)
	}
}

func TestProviderItemsReadsSiblingNamespaces(t *testing.T) {
	got := library.ProviderItems([]string{"wikidata:Q1", "wikidata@movie:Q2", "tmdb:collection:5", "wikidataish:Q3"}, "wikidata")
	if want := []string{"Q1", "Q2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
}

func TestDismissingAWorkForgetsWhyItJoined(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	franchises := library.NewFranchises(opened)
	result, err := series.Upsert(t.Context(), seriesAt, sampleSeries())
	if err != nil {
		t.Fatal(err)
	}
	id, err := franchises.CreateAuto(t.Context(), "tv", "Family")
	if err != nil {
		t.Fatal(err)
	}
	if err := franchises.AddToFranchise(t.Context(), id, []int64{result.WorkID}); err != nil {
		t.Fatal(err)
	}
	if err := franchises.RecordMemberOrigin(t.Context(), id, result.WorkID, library.MemberReasonCrossover, "Two Hearts (Other)"); err != nil {
		t.Fatal(err)
	}
	origins, err := franchises.MemberOrigins(t.Context(), id)
	if err != nil || origins[result.WorkID] != (library.MemberOrigin{Reason: library.MemberReasonCrossover, Detail: "Two Hearts (Other)"}) {
		t.Fatalf("origins = %+v, %v", origins, err)
	}
	if err := franchises.Dismiss(t.Context(), id, result.WorkID); err != nil {
		t.Fatal(err)
	}
	if origins, _ := franchises.MemberOrigins(t.Context(), id); len(origins) != 0 {
		t.Errorf("origins after dismissing = %+v", origins)
	}
}

func TestLinkedCountsAddUpWhatTheOtherEndHoldsFromBothSides(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	franchises := library.NewFranchises(opened)
	result, err := series.Upsert(t.Context(), seriesAt, sampleSeries())
	if err != nil {
		t.Fatal(err)
	}
	tv, err := franchises.Join(t.Context(), "tv", "wikidata", "Q42", "MacGyver", result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	films, err := franchises.Join(t.Context(), "movie", "wikidata", "Q42", "MacGyver", result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if err := franchises.LinkCollections(t.Context(), tv, films); err != nil {
		t.Fatal(err)
	}
	got, err := franchises.LinkedCountsByCollection(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]library.LinkedCounts{tv: {Held: 1, Total: 1}, films: {Held: 1, Total: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("counts = %+v, want %+v", got, want)
	}
}
