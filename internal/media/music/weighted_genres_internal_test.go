package music

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

func TestWeightedGenresKeepsWhatTheVotesAgreeOn(t *testing.T) {
	cases := []struct {
		name string
		in   []musicbrainz.Genre
		want []string
	}{
		{"a stray vote is dropped", []musicbrainz.Genre{{Name: "jazz", Count: 1}, {Name: "pop", Count: 9}, {Name: "rock", Count: 12}}, []string{"rock", "pop"}},
		{"single votes all stay", []musicbrainz.Genre{{Name: "ambient", Count: 1}, {Name: "drone", Count: 1}}, []string{"ambient", "drone"}},
		{"no votes at all stay", []musicbrainz.Genre{{Name: "folk"}, {Name: "blues"}}, []string{"blues", "folk"}},
		{"ties go by name and six at most", []musicbrainz.Genre{
			{Name: "g", Count: 5}, {Name: "f", Count: 5}, {Name: "e", Count: 5}, {Name: "d", Count: 5}, {Name: "c", Count: 5}, {Name: "b", Count: 5}, {Name: "a", Count: 5},
		}, []string{"a", "b", "c", "d", "e", "f"}},
		{"none", nil, nil},
	}
	for _, c := range cases {
		if got := weightedGenres(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
