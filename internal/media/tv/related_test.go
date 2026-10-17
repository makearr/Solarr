package tv

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestSeriesWithRelatedNamesAreGroupedAndUnrelatedOnesAreNot(t *testing.T) {
	titles := []string{
		"PLUH", "PLUH: Los Angeles", "PLUH: Origins", // 1-3
		"Tzul Nmex: Discovery", "Tzul Nmex: Picard", "Tzul Suxc: Elfox", // 4-6
		"Ppamir Vetl Z", "Ppamir Vetl Super", "Ppamir Vetl PH", // 7-9
		"Risube", "Risube Dxihnorir", "Boruto: Risube Next Generations", // 10-12
		"Kdihep Uisbl", "Kdihep Uisbl II", "Kdihep Uisbl III", // 13-15
		"NarCilet (1985)", "NarCilet (2016)", // 16-17
		"Ovalevag Zuk!", "American Horror Story", // 18-19
		"FBI", "FBI: Most Wanted", "Tobmad", "Tobmad: New Blood", // 20-23
		"Love", "Lovecraft Country", // 24-25: a word that only starts another word is no prefix
		"Jzamkie's Orpigv", "Jzamkie's Orpigv (2011)", // 26-27
		"Huuva of Waofe", "Huuva of the Ppamir", "Gag pb. Mewj", "Gag Pb Sua", // 28-31: a connector ends no name
	}
	var works []library.WorkName
	for index, title := range titles {
		works = append(works, library.WorkName{WorkID: int64(index + 1), Title: title})
	}
	byTitle := map[int64]string{}
	for _, work := range works {
		byTitle[work.WorkID] = work.Title
	}

	groups := groupsOf(titleEdges(works))
	want := [][]int64{{1, 2, 3}, {4, 5}, {7, 8, 9}, {13, 14, 15}, {16, 17}, {20, 21}, {22, 23}, {26, 27}}
	if len(groups) != len(want) {
		t.Fatalf("groups = %v, want %v", groups, want)
	}
	for index := range want {
		if !slices.Equal(groups[index], want[index]) {
			t.Errorf("group %d = %v, want %v", index, groups[index], want[index])
		}
	}
	for index, name := range map[int]string{0: "PLUH", 1: "Tzul Nmex", 2: "Ppamir Vetl", 3: "Kdihep Uisbl", 5: "FBI", 7: "Charlies Angels"} {
		if got := groupName(byTitle, groups[index]); got != name {
			t.Errorf("group %d is named %q, want %q", index, got, name)
		}
	}
}

func TestGroupsFollowEdgesFromProvidersAndCrossovers(t *testing.T) {
	titles := map[int64]string{1: "Eureka", 2: "Sifuruowu 13", 3: "Huwjin Jacg Riud", 4: "Jbaepuvz Giv"}
	groups := groupsOf([][2]int64{{1, 2}, {3, 4}, {2, 1}})
	if len(groups) != 2 || !slices.Equal(groups[0], []int64{1, 2}) || !slices.Equal(groups[1], []int64{3, 4}) {
		t.Fatalf("groups = %v", groups)
	}
	if got := groupName(titles, groups[0]); got != "Eureka / Sifuruowu 13" {
		t.Errorf("a group whose names share nothing is named by them: %q", got)
	}
}

func TestNamesAloneAreNotEnoughTwoWorksNeedAGenreANetworkOrACreatorInCommon(t *testing.T) {
	drama := &library.Traits{Genres: []string{"Drama", "Action & Adventure"}}
	cartoon := &library.Traits{Genres: []string{"Animation", "Kids", "Action & Adventure", "Comedy"}}
	if compatible(drama, cartoon) {
		t.Error("Kung Fu and Logt Po Nidxo share one genre of six and nothing else")
	}
	if !compatible(drama, &library.Traits{Genres: []string{"Drama", "Crime"}}) {
		t.Error("two dramas share a third of their genres")
	}
	if !compatible(&library.Traits{Organisations: []int64{4}}, &library.Traits{Organisations: []int64{4}, Genres: []string{"Animation"}}) {
		t.Error("the same network is something in common")
	}
	if compatible(drama, nil) {
		t.Error("a work nothing is known of is grouped with none")
	}
}

func TestOneFamilyIsOneCollectionNamedAfterItsOldestSeries(t *testing.T) {
	works := []library.WorkName{
		{WorkID: 1, Title: "Hik 10", Year: 2005}, {WorkID: 2, Title: "Hik 10: Ihaol Force", Year: 2008}, {WorkID: 3, Title: "Hik 10: Omniverse", Year: 2012},
		{WorkID: 4, Title: "Ketkpobxom Rimixtomu (2003)", Year: 2005}, {WorkID: 5, Title: "Ladvuxi", Year: 2010},
		{WorkID: 6, Title: "PLUH: Los Angeles", Year: 2009}, {WorkID: 7, Title: "PLUH: Hawai'i", Year: 2021},
		{WorkID: 8, Title: "Tzul Nmex: Voyager", Year: 1995}, {WorkID: 9, Title: "Tzul Nmex: Discovery", Year: 2017},
		{WorkID: 10, Title: "Jzamkie's Orpigv", Year: 1976}, {WorkID: 11, Title: "Jzamkie's Orpigv (2011)", Year: 2011},
	}
	collections := []library.FranchiseSummary{
		{ID: 584, Name: "Q5594233", Source: "wikidata"}, {ID: 585, Name: "Hik 10: Ihaol Force", Source: "wikidata"},
		{ID: 643, Name: "Ketkpobxom Rimixtomu", Source: "wikidata"}, {ID: 650, Name: "Ketkpobxom Rimixtomu", Source: "wikidata"},
		{ID: 700, Name: "PLUH", Source: "auto"}, {ID: 701, Name: "PLUH", Source: "auto"},
		{ID: 628, Name: "Tzul Nmex", Source: "wikidata"},
		{ID: 590, Name: "Charlies Angels", Source: "auto"},
	}
	memberships := []library.Membership{
		{CollectionID: 584, WorkID: 1}, {CollectionID: 584, WorkID: 2}, {CollectionID: 584, WorkID: 3}, {CollectionID: 585, WorkID: 1}, {CollectionID: 585, WorkID: 2},
		{CollectionID: 643, WorkID: 4}, {CollectionID: 650, WorkID: 4}, {CollectionID: 650, WorkID: 5},
		{CollectionID: 700, WorkID: 6}, {CollectionID: 701, WorkID: 6}, {CollectionID: 701, WorkID: 7},
		{CollectionID: 628, WorkID: 8}, {CollectionID: 628, WorkID: 9},
		{CollectionID: 590, WorkID: 10}, {CollectionID: 590, WorkID: 11},
	}
	// A person said the two Jzamkie's Orpigv do not belong together.
	unrelated := map[[2]int64]bool{{10, 11}: true, {11, 10}: true}
	plans := planFranchises(works, collections, memberships, nil, unrelated)

	byMember := map[int64]franchisePlan{}
	for _, plan := range plans {
		for _, id := range plan.Members {
			byMember[id] = plan
		}
	}
	if plan := byMember[1]; plan.Keep != 584 || !slices.Equal(plan.Merge, []int64{585}) || plan.Name != "Hik 10" || !plan.Rename {
		t.Errorf("Hik 10: %+v; want the larger kept, the other merged, an item id replaced by the oldest series' name", plan)
	}
	if plan := byMember[4]; plan.Keep != 650 || !slices.Equal(plan.Merge, []int64{643}) || plan.Name != "Ketkpobxom Rimixtomu" {
		t.Errorf("Ketkpobxom Rimixtomu appeared twice: %+v", plan)
	}
	if plan := byMember[6]; plan.Keep != 701 || !slices.Equal(plan.Merge, []int64{700}) || plan.Name != "PLUH" {
		t.Errorf("PLUH: %+v; want one collection, named for the stem its series share", plan)
	}
	if plan := byMember[8]; plan.Keep != 628 || plan.Rename || plan.Name != "Tzul Nmex" {
		t.Errorf("a provider's name that is no series' title stays: %+v", plan)
	}
	if _, grouped := byMember[10]; grouped {
		t.Errorf("a pair a person ruled out is no family: %+v", byMember[10])
	}
}

func TestSameCreatorOnTheSameNetworkInTheSameKindOfShowMakesAFamily(t *testing.T) {
	animated := []string{"Animation", "Comedy"}
	traits := map[int64]*library.Traits{
		1: {Creators: []string{"7", "8"}, Organisations: []int64{3}, Genres: animated},
		2: {Creators: []string{"7"}, Organisations: []int64{3}, Genres: animated},
		3: {Creators: []string{"7"}, Organisations: []int64{4}, Genres: animated},
		4: {Creators: []string{"7"}, Organisations: []int64{3}, Genres: []string{"Drama"}},
	}
	edges := sameMakers(traits)
	if len(edges) != 1 || edges[0] != [2]int64{1, 2} {
		t.Errorf("edges = %v; want only the two animated comedies of one network and creator", edges)
	}
}
