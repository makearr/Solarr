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
	want := [][]int64{{1, 2, 3}, {4, 5}, {7, 8, 9}, {10, 11}, {13, 14, 15}, {16, 17}, {20, 21}, {22, 23}}
	if len(groups) != len(want) {
		t.Fatalf("groups = %v, want %v", groups, want)
	}
	for index := range want {
		if !slices.Equal(groups[index], want[index]) {
			t.Errorf("group %d = %v, want %v", index, groups[index], want[index])
		}
	}
	for index, name := range map[int]string{0: "PLUH", 1: "Tzul Nmex", 2: "Ppamir Vetl", 4: "Kdihep Uisbl", 6: "FBI"} {
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
