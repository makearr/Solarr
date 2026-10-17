package tv

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestSeriesWithRelatedNamesAreGroupedAndUnrelatedOnesAreNot(t *testing.T) {
	titles := []string{
		"Quill", "Quill: North", "Quill: Origins", // 1-3
		"Void Rangers: Alpha", "Void Rangers: Beta", "Void Wars: Delta", // 4-6
		"Ember Orb Z", "Ember Orb Super", "Ember Orb GT", // 7-9
		"Kitsu", "Kitsu Storm", "Zoru: Kitsu Next", // 10-12
		"Blue Globe", "Blue Globe II", "Blue Globe III", // 13-15
		"Fixit (1985)", "Fixit (2016)", // 16-17
		"Midland Dad!", "Midland Horror Story", // 18-19
		"BRX", "BRX: Most Sought", "Corvin", "Corvin: Reborn", // 20-23
		"Love", "Lovecraft Country", // 24-25: a word that only starts another word is no prefix
		"Mabel's Angels", "Mabel's Angels (2011)", // 26-27
		"House of Mice", "House of the Vxixa", "Man vs. Beast", "Man Vs Bug", // 28-31: a connector ends no name
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
	for index, name := range map[int]string{0: "Quill", 1: "Void Rangers", 3: "Blue Globe", 5: "BRX", 7: "Mabel's Angels"} {
		var members []library.WorkName
		for _, id := range groups[index] {
			members = append(members, library.WorkName{WorkID: id, Title: byTitle[id]})
		}
		if got := familyName(members); got != name {
			t.Errorf("group %d is named %q, want %q", index, got, name)
		}
	}
}

func TestGroupsFollowEdgesFromProvidersAndCrossovers(t *testing.T) {
	groups := groupsOf([][2]int64{{1, 2}, {3, 4}, {2, 1}})
	if len(groups) != 2 || !slices.Equal(groups[0], []int64{1, 2}) || !slices.Equal(groups[1], []int64{3, 4}) {
		t.Fatalf("groups = %v", groups)
	}
}

func TestNamesAloneAreNotEnoughTwoWorksNeedAGenreANetworkOrACreatorInCommon(t *testing.T) {
	drama := &library.Traits{Genres: []string{"Drama", "Action & Adventure"}}
	cartoon := &library.Traits{Genres: []string{"Animation", "Kids", "Action & Adventure", "Comedy"}}
	if compatible(drama, cartoon) {
		t.Error("Kung Fu and Kung Fu Pup share one genre of six and nothing else")
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
		{WorkID: 1, Title: "Zed 10", Year: 2005}, {WorkID: 2, Title: "Zed 10: Force", Year: 2008}, {WorkID: 3, Title: "Zed 10: Multi", Year: 2012},
		{WorkID: 4, Title: "Star Cruiser (2003)", Year: 2005}, {WorkID: 5, Title: "Tiber", Year: 2010},
		{WorkID: 6, Title: "Quill: North", Year: 2009}, {WorkID: 7, Title: "Quill: East", Year: 2021},
		{WorkID: 8, Title: "Void Rangers: Gamma", Year: 1995}, {WorkID: 9, Title: "Void Rangers: Alpha", Year: 2017},
		{WorkID: 10, Title: "Mabel's Angels", Year: 1976}, {WorkID: 11, Title: "Mabel's Angels (2011)", Year: 2011},
		{WorkID: 12, Title: "Vulcano Park: Camp Lava", Year: 2020}, {WorkID: 13, Title: "Vulcano Park: Ember Theory", Year: 2024},
		{WorkID: 14, Title: "Mirrorcase", Year: 2001}, {WorkID: 15, Title: "Driftwater", Year: 2004},
	}
	collections := []library.FranchiseSummary{
		{ID: 584, Name: "Q5594233", Source: "wikidata"}, {ID: 585, Name: "Zed 10: Force", Source: "wikidata"},
		{ID: 643, Name: "Star Cruiser", Source: "wikidata"}, {ID: 650, Name: "Star Cruiser", Source: "wikidata"},
		{ID: 700, Name: "Quill", Source: "auto"}, {ID: 701, Name: "Quill", Source: "auto"},
		{ID: 628, Name: "Void Rangers", Source: "wikidata"},
		{ID: 590, Name: "Charlies Angels", Source: "auto"},
		{ID: 710, Name: "Vulcano Land", Source: "wikidata"},
		{ID: 724, Name: "Mirrorcase", Source: "auto"},
	}
	memberships := []library.Membership{
		{CollectionID: 584, WorkID: 1}, {CollectionID: 584, WorkID: 2}, {CollectionID: 584, WorkID: 3}, {CollectionID: 585, WorkID: 1}, {CollectionID: 585, WorkID: 2},
		{CollectionID: 643, WorkID: 4}, {CollectionID: 650, WorkID: 4}, {CollectionID: 650, WorkID: 5},
		{CollectionID: 700, WorkID: 6}, {CollectionID: 701, WorkID: 6}, {CollectionID: 701, WorkID: 7}, {CollectionID: 701, WorkID: 9},
		{CollectionID: 628, WorkID: 8}, {CollectionID: 628, WorkID: 9},
		{CollectionID: 590, WorkID: 10}, {CollectionID: 590, WorkID: 11},
		{CollectionID: 710, WorkID: 12}, {CollectionID: 710, WorkID: 13},
		{CollectionID: 724, WorkID: 14}, {CollectionID: 724, WorkID: 15},
	}
	// A person said the two Mabel's Angels do not belong together; nothing says Mirrorcase and Driftwater do any more.
	unrelated := map[[2]int64]bool{{10, 11}: true, {11, 10}: true}
	plans, drop := planFranchises(works, collections, memberships, nil, titleEdges(works), unrelated)

	byMember := map[int64]franchisePlan{}
	for _, plan := range plans {
		for _, id := range plan.Members {
			byMember[id] = plan
		}
	}
	if plan := byMember[1]; plan.Keep != 584 || !slices.Equal(plan.Merge, []int64{585}) || plan.Name != "Zed 10" || !plan.Rename {
		t.Errorf("Zed 10: %+v; want the larger kept, the other merged, an item id replaced by the name its series share", plan)
	}
	if plan := byMember[4]; plan.Keep != 650 || !slices.Equal(plan.Merge, []int64{643}) || plan.Name != "Star Cruiser" {
		t.Errorf("Star Cruiser appeared twice: %+v", plan)
	}
	if plan := byMember[6]; plan.Keep != 701 || plan.Merge != nil || !slices.Equal(plan.Remove, []int64{9}) || plan.Name != "Quill" {
		t.Errorf("Quill: %+v; want Solarr's fuller one kept and rebuilt from the evidence", plan)
	}
	if plan := byMember[8]; plan.Keep != 628 || plan.Rename || plan.Name != "Void Rangers" {
		t.Errorf("a provider's name that is no series' title stays: %+v", plan)
	}
	if plan := byMember[12]; plan.Keep != 710 || plan.Name != "Vulcano Park" || !plan.Rename {
		t.Errorf("Vulcano Park: %+v; want the name its series share over the provider's", plan)
	}
	if _, grouped := byMember[10]; grouped {
		t.Errorf("a pair a person ruled out is no family: %+v", byMember[10])
	}
	if _, grouped := byMember[14]; grouped {
		t.Errorf("Solarr's own collection is no evidence for itself: %+v", byMember[14])
	}
	slices.Sort(drop)
	if !slices.Equal(drop, []int64{590, 700, 724}) {
		t.Errorf("drop = %v; want Solarr's collections no family keeps", drop)
	}
}

func TestSeriesThatShareTheirRegularsOrAreNamedAfterACharacterAreOneFamily(t *testing.T) {
	works := []library.WorkName{
		{WorkID: 1, Title: "Kitsuro", Year: 2002}, {WorkID: 2, Title: "Kitsuro Senpu", Year: 2007},
		{WorkID: 3, Title: "The Lattice Hour", Year: 2007}, {WorkID: 4, Title: "Young Brannock", Year: 2017},
		{WorkID: 5, Title: "The Pemberlys", Year: 1989}, {WorkID: 6, Title: "Harbour Folk", Year: 1999},
		{WorkID: 7, Title: "Quill and Pebble", Year: 2013}, {WorkID: 8, Title: "Governor Tandry", Year: 2026},
		{WorkID: 13, Title: "Mopp and Dibble", Year: 2007}, {WorkID: 14, Title: "Gravel and the Agents of B.O.U.L.D.E.R.", Year: 2013},
		{WorkID: 9, Title: "Tinyverse", Year: 2022}, {WorkID: 10, Title: "Marshlands", Year: 2019}, {WorkID: 11, Title: "Goose Tales", Year: 2017}, {WorkID: 12, Title: "Small Town Reds", Year: 2018},
	}
	regular := func(work, person int64, name, character string) library.CharacterCredit {
		return library.CharacterCredit{WorkID: work, PersonID: person, Person: name, Character: character, Regular: true, Billing: 1}
	}
	guest := func(work, person int64, name, character string, episodes int64) library.CharacterCredit {
		return library.CharacterCredit{WorkID: work, PersonID: person, Person: name, Character: character, Episodes: episodes, Billing: -1}
	}
	credits := []library.CharacterCredit{
		regular(1, 1, "Aiko Morane", "Kitsuro Hayame (voice)"), regular(2, 1, "Aiko Morane", "Kitsuro Hayame"),
		regular(1, 2, "Ren Usagawa", "Rin Tokara"), regular(2, 2, "Ren Usagawa", "Rin Tokara"),
		regular(1, 3, "Kei Tomura", "Jiro Hasaki"), regular(2, 3, "Kei Tomura", "Jiro Hasaki"),
		// Young Brannock is named after a regular of the older series, played by the same actor.
		regular(3, 4, "Tobin Ashe", "Brannock Vale"), regular(4, 4, "Tobin Ashe", "Brannock Vale / Narrator"),
		// A crossover episode and a cameo as oneself prove no family.
		regular(5, 5, "Dale Corvin", "Bert Pemberly"), guest(6, 5, "Dale Corvin", "Bert Pemberly", 1),
		regular(5, 6, "Silas Marrow", "Dougal Harrow"), guest(6, 6, "Silas Marrow", "Dougal Harrow", 1),
		guest(5, 7, "Orla Fenwick", "Orla Fenwick", 2), guest(3, 7, "Orla Fenwick", "Orla Fenwick", 2),
		regular(5, 8, "Some Actor", "Narrator"), regular(6, 8, "Some Actor", "Narrator"), regular(3, 8, "Some Actor", "Narrator"),
		// Governor Tandry recurs on Quill and Pebble, under more than one name, before getting his own series.
		guest(7, 9, "Basil Okoro", "Governor Tandry / Mayor Tandry (voice)", 1), guest(7, 9, "Basil Okoro", "Governor/Senator Ayo Tandry (voice)", 4),
		regular(8, 9, "Basil Okoro", "Governor Ayo Tandry (voice)"),
		// Gravel, billed far down Mopp and Dibble for one episode, names no spin-off of it.
		{WorkID: 13, PersonID: 10, Person: "Ferris Tate", Character: "Gravel (voice)", Regular: true, Episodes: 1, Billing: 649},
		regular(14, 10, "Ferris Tate", "Gravel"),
	}
	// Tinyverse has three regulars of each of three series that share nothing else.
	for index, show := range []int64{10, 11, 12} {
		for n := range 3 {
			person := int64(100 + index*10 + n)
			name := "Character " + string(rune('A'+index)) + string(rune('a'+n))
			credits = append(credits, regular(9, person, "Actor", name), regular(show, person, "Actor", name))
		}
	}
	edges := characterEdges(credits, works)
	want := [][2]int64{{1, 2}, {3, 4}, {7, 8}}
	if !slices.Equal(edges, want) {
		t.Errorf("edges = %v, want %v", edges, want)
	}
}

func TestWikidataCollectionsHoldWhatWikidataListsAndGoWhenItNoLongerNamesThem(t *testing.T) {
	works := []library.WorkName{
		{WorkID: 1, Title: "Harbourlight", Year: 2001}, {WorkID: 2, Title: "Harbourlight: Tides", Year: 2008},
		{WorkID: 3, Title: "Talking Harbourlight", Year: 2010}, {WorkID: 4, Title: "Orchard Lane", Year: 1999},
	}
	collections := []library.FranchiseSummary{
		{ID: 900, Name: "Harbourlight", Source: "wikidata"},
		{ID: 901, Name: "Old Strand", Source: "wikidata"}, {ID: 902, Name: "Followed Strand", Source: "wikidata", Monitored: true},
		{ID: 903, Name: "Orchard Lane", Source: "wikidata"},
	}
	memberships := []library.Membership{
		{CollectionID: 900, WorkID: 1}, {CollectionID: 900, WorkID: 2}, {CollectionID: 900, WorkID: 3},
		{CollectionID: 901, WorkID: 4}, {CollectionID: 902, WorkID: 4}, {CollectionID: 903, WorkID: 4}, {CollectionID: 903, WorkID: 3},
	}
	listed := map[int64][]int64{900: {1, 2}, 903: {4}}
	plans, drop := planFranchises(works, collections, memberships, listed, nil, nil)

	byKeep := map[int64]franchisePlan{}
	for _, plan := range plans {
		byKeep[plan.Keep] = plan
	}
	if plan := byKeep[900]; !slices.Equal(plan.Members, []int64{1, 2}) || !slices.Equal(plan.Remove, []int64{3}) {
		t.Errorf("Harbourlight: %+v; want what Wikidata lists, a series added by an earlier pass taken out", plan)
	}
	if plan := byKeep[903]; !slices.Equal(plan.Remove, []int64{3}) {
		t.Errorf("a collection of one series keeps only Wikidata's: %+v", plan)
	}
	if !slices.Equal(drop, []int64{901}) {
		t.Errorf("drop = %v; want the collection Wikidata no longer names, not the followed one", drop)
	}
}
