package movie

import (
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
