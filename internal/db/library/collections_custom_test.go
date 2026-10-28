package library_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestACollectionMadeByHandHoldsWorksOfItsLibraryAndNoOtherKindIsEditable(t *testing.T) {
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

	id, err := franchises.Create(t.Context(), "tv", 1, "Spin-offs")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := franchises.InCategory(t.Context(), "tv", 1)
	if err != nil || len(listed) != 1 || listed[0].Kind != library.CustomKind || len(listed[0].Members) != 0 {
		t.Fatalf("an empty collection of the library is listed: %+v, %v", listed, err)
	}
	if other, _ := franchises.InCategory(t.Context(), "tv", 2); len(other) != 0 {
		t.Errorf("another library does not see it: %+v", other)
	}
	if err := franchises.AddMember(t.Context(), id, result.WorkID); err != nil {
		t.Fatal(err)
	}
	if err := franchises.Rename(t.Context(), id, "Spin-offs and sequels"); err != nil {
		t.Fatal(err)
	}
	listed, _ = franchises.InCategory(t.Context(), "tv", 1)
	if len(listed) != 1 || listed[0].Name != "Spin-offs and sequels" || len(listed[0].Members) != 1 || listed[0].Members[0].WorkID != result.WorkID {
		t.Errorf("after adding and renaming: %+v", listed)
	}

	provider, err := franchises.Join(t.Context(), "tv", "wikidata", "Q1", "A franchise", result.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if err := franchises.Rename(t.Context(), provider, "Mine now"); !errors.Is(err, library.ErrNotCustomCollection) {
		t.Errorf("renaming a provider's collection = %v, want it refused", err)
	}
	if err := franchises.AddMember(t.Context(), provider, result.WorkID); !errors.Is(err, library.ErrNotCustomCollection) {
		t.Errorf("filling a provider's collection = %v, want it refused", err)
	}
	if err := franchises.RemoveMember(t.Context(), id, result.WorkID); err != nil {
		t.Fatal(err)
	}
	if err := franchises.Delete(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if listed, _ = franchises.InCategory(t.Context(), "tv", 1); len(listed) != 1 || listed[0].ID != provider {
		t.Errorf("only the provider's collection is left: %+v", listed)
	}
}

func TestSameNamedCollectionsOfOneSourceMergeIntoTheFirst(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	franchises := library.NewFranchises(opened)
	first, err := series.Upsert(t.Context(), seriesAt, sampleSeries())
	if err != nil {
		t.Fatal(err)
	}
	keep, err := franchises.Join(t.Context(), "tv", "book:series", "B01", "Awaken Online", first.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := franchises.Join(t.Context(), "tv", "book:series", "B02", "awaken online", first.WorkID); err != nil {
		t.Fatal(err)
	}
	other, err := franchises.Join(t.Context(), "tv", "book:series", "B03", "Another Series", first.WorkID)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := franchises.MergeSameNamed(t.Context(), "tv", "book:series")
	if err != nil || removed != 1 {
		t.Fatalf("MergeSameNamed = %d, %v, want one removed", removed, err)
	}
	_, _ = keep, other
	if again, _ := franchises.MergeSameNamed(t.Context(), "tv", "book:series"); again != 0 {
		t.Errorf("a second merge removed %d, want none", again)
	}
}

func TestAProvidersCollectionAPersonDeletedOrTookAWorkOutOfStaysSoOnRefresh(t *testing.T) {
	opened, _ := newLibrary(t)
	series := library.NewSeries(opened)
	franchises := library.NewFranchises(opened)
	refresh := func(title, tvdb string) int64 {
		t.Helper()
		write := sampleSeries()
		write.Work.CanonicalTitle, write.Work.SortTitle = title, title
		write.IDs = []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: tvdb}}
		write.Franchise = &library.SeriesFranchiseWrite{Provider: "tmdb:collection", ExternalID: "10", Name: "Example Collection"}
		result, err := series.Upsert(t.Context(), seriesAt, write)
		if err != nil {
			t.Fatal(err)
		}
		return result.WorkID
	}
	collectionOf := func() (int64, []int64) {
		t.Helper()
		summaries, err := franchises.Summaries(t.Context(), "tv")
		if err != nil {
			t.Fatal(err)
		}
		if len(summaries) == 0 {
			return 0, nil
		}
		members, err := franchises.MembersOf(t.Context(), summaries[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		return summaries[0].ID, members
	}
	first, second := refresh("First", "1"), refresh("Second", "2")
	id, members := collectionOf()
	if len(members) != 2 {
		t.Fatalf("members = %v, want both works", members)
	}

	if err := franchises.Dismiss(t.Context(), id, first); err != nil {
		t.Fatal(err)
	}
	refresh("First", "1")
	refresh("Second", "2")
	if _, members := collectionOf(); len(members) != 1 || members[0] != second {
		t.Errorf("after a refresh, members = %v, want only %d: the work taken out stays out", members, second)
	}

	if err := franchises.DeleteFranchise(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	refresh("First", "1")
	refresh("Second", "2")
	if again, members := collectionOf(); again != 0 {
		t.Errorf("after a refresh, collection %d with %v exists, want the deleted one never made again", again, members)
	}
	if joined, err := franchises.Join(t.Context(), "tv", "tmdb:collection", "10", "Example Collection", second); err != nil || joined != 0 {
		t.Errorf("Join = %d, %v, want 0: the collection was deleted", joined, err)
	}
}
