package works_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/works"
)

func TestCompareIDs(t *testing.T) {
	wanted := map[string]string{"tvdb": "123", "imdb": "tt555"}
	for _, test := range []struct {
		release map[string]string
		want    works.IDVerdict
	}{
		{nil, works.IDsUnknown},
		{map[string]string{"tmdb": "9"}, works.IDsUnknown},
		{map[string]string{"tvdb": "123"}, works.IDsSame},
		{map[string]string{"tvdb": "999"}, works.IDsDiffer},
		{map[string]string{"imdb": "tt555"}, works.IDsSame},
		{map[string]string{"imdb": "555"}, works.IDsSame},
		{map[string]string{"tvdb": "123", "imdb": "tt1"}, works.IDsSame},
	} {
		if got := works.CompareIDs(test.release, wanted); got != test.want {
			t.Errorf("CompareIDs(%v) = %v, want %v", test.release, got, test.want)
		}
	}
}
