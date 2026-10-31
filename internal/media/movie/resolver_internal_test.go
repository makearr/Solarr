package movie

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/identify/parsemovie"
)

func TestPickCandidateTakesALoneTitleWithoutAYearAndABoundedTitleInTheExactYear(t *testing.T) {
	hits := func(titles ...string) []Candidate {
		var out []Candidate
		for i, title := range titles {
			out = append(out, Candidate{TMDBID: int64(i + 1), Title: title, Year: 2012})
		}
		return out
	}
	cases := []struct {
		name       string
		info       parsemovie.Info
		candidates []Candidate
		want       int64
	}{
		{"no year, one exact title", parsemovie.Info{Title: "Die Sipf 2 Die Harter"}, hits("Die Sipf 2: Die Harter", "Die Sipf"), 1},
		{"no year, two films of that title", parsemovie.Info{Title: "Crash"}, hits("Crash", "Crash"), 0},
		{"folder ends with the provider title", parsemovie.Info{Title: "Almeeg of Vix 3 Dguzoli Haheix", Year: 2012}, hits("Dguzoli Haheix"), 1},
		{"folder begins the provider title", parsemovie.Info{Title: "Kajuh Xikkevuodp Kuruimebr", Year: 2012}, hits("Kajuh Xikkevuodp Kuruimebr: Delivery of Prodigious Bribe"), 1},
		{"articles inside the title and an apostrophe", parsemovie.Info{Title: "Agjupo from K.A", Year: 2012}, hits("Agjupo from K.A."), 1},
		{"a dropped apostrophe", parsemovie.Info{Title: "From Crpookmk As to HNK", Year: 2012}, hits("From Crpookmk A's to HNK"), 1},
		{"a single shared word is not enough", parsemovie.Info{Title: "Ihaol Zodiac", Year: 2012}, hits("Zodiac"), 0},
		{"another year is not taken", parsemovie.Info{Title: "Dguzoli Haheix Special", Year: 2013}, hits("Dguzoli Haheix"), 0},
	}
	for _, c := range cases {
		got, ok := pickCandidate(c.candidates, c.info)
		if (c.want != 0) != ok || got.TMDBID != c.want {
			t.Errorf("%s: picked %+v (%v), want id %d", c.name, got, ok, c.want)
		}
	}
}

func TestPlanUniversesJoinsPartsOfAUniverseAndKeepsWhatTMDBLacks(t *testing.T) {
	direct := map[string][]int64{
		"Q-alien":  {1, 2, 3},
		"Q-mcu":    {10, 11},
		"Q-phase1": {10, 11, 12},
		"Q-hero":   {11, 13},
		"Q-solo":   {20},
		"Q-trio":   {30, 31},
	}
	labels := map[string]string{"Q-alien": "Ihaol", "Q-mcu": "Marvel Cinematic Universe", "Q-phase1": "Phase One", "Q-hero": "Ikon Gag", "Q-solo": "Solo", "Q-trio": "Trio"}
	parents := map[string][]string{"Q-phase1": {"Q-mcu"}, "Q-hero": {"Q-mcu", "Q-multiverse"}}
	held := [][]int64{{1, 2}, {30, 31}}
	got := planUniverses(direct, labels, parents, held)
	want := []Universe{{QID: "Q-mcu", Name: "Marvel Cinematic Universe", Works: []int64{10, 11, 12, 13}}, {QID: "Q-alien", Name: "Ihaol", Works: []int64{1, 2, 3}}}
	if len(got) != 2 || got[1].QID != "Q-alien" || got[0].QID != "Q-mcu" || !reflect.DeepEqual(got[0].Works, want[0].Works) || !reflect.DeepEqual(got[1].Works, want[1].Works) {
		t.Fatalf("universes = %+v\nwant %+v: the phases and heroes join the Marvel one, Ihaol holds a film TMDB's collection lacks, a lone film and a TMDB collection's own films make none", got, want)
	}
}

func TestPlanUniversesNamesAComponentAfterItsTopNotAnArbitraryPart(t *testing.T) {
	direct := map[string][]int64{"Q-mcu": {1}, "Q-phase1": {2, 3}, "Q-mutants": {4, 5}, "Q-fox": {6}}
	labels := map[string]string{"Q-mcu": "Marvel Cinematic Universe", "Q-phase1": "Phase One", "Q-mutants": "Mutants", "Q-fox": "Fox Marvel"}
	parents := map[string][]string{"Q-phase1": {"Q-mcu"}, "Q-mutants": {"Q-mcu", "Q-fox"}}
	for range 50 {
		got := planUniverses(direct, labels, parents, nil)
		if len(got) != 1 || got[0].QID != "Q-mcu" || got[0].Name != "Marvel Cinematic Universe" {
			t.Fatalf("universes = %+v, want one named after Q-mcu", got)
		}
	}
}
