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
