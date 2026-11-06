package importjob

import "testing"

// A type's unit goes to its general library before one that rules or a rating limit narrow, then by priority, then id.
func TestChooseLibrary(t *testing.T) {
	for name, tc := range map[string]struct {
		libraries []Library
		want      int64
	}{
		"the only one":           {[]Library{{CategoryID: 9, MediaType: "tv"}, {CategoryID: 1, MediaType: "music", General: true}}, 9},
		"general before narrow":  {[]Library{{CategoryID: 1, MediaType: "tv", Rank: 0}, {CategoryID: 2, MediaType: "tv", General: true, Rank: 9}}, 2},
		"priority among general": {[]Library{{CategoryID: 1, MediaType: "tv", General: true, Rank: 5}, {CategoryID: 2, MediaType: "tv", General: true, Rank: 1}}, 2},
		"priority among narrow":  {[]Library{{CategoryID: 1, MediaType: "tv", Rank: 5}, {CategoryID: 2, MediaType: "tv", Rank: 1}}, 2},
		"lowest id on a tie":     {[]Library{{CategoryID: 8, MediaType: "tv", General: true}, {CategoryID: 3, MediaType: "tv", General: true}}, 3},
	} {
		if got := chooseLibrary(tc.libraries, "tv"); got != tc.want {
			t.Errorf("%s: chose %d, want %d", name, got, tc.want)
		}
	}
}
