package apiserver

import (
	"slices"
	"testing"
)

func TestWithinAge_KeepsWhatTheLimitAdmitsAndTheUnratedOnlyInAKidsLibrary(t *testing.T) {
	items := []DiscoverItem{
		{ID: 1, CategoryID: 10, Age: 6}, {ID: 2, CategoryID: 10, Age: 16}, {ID: 3, CategoryID: 10, Age: -1}, {ID: 4, CategoryID: 20, Age: -1}, {ID: 5, CategoryID: 10, Age: 12},
	}
	ids := func(kept []DiscoverItem) []int64 {
		var out []int64
		for _, item := range kept {
			out = append(out, item.ID)
		}
		return out
	}
	if got, want := ids(withinAge(nil, items)), []int64{1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("no limit kept %v, want all %v", got, want)
	}
	if got, want := ids(withinAge(&ViewerAge{MaxAge: 12, KidsCategoryIDs: []int64{20}}, items)), []int64{1, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("limit 12 kept %v, want %v (6, 12, and the unrated one of the kids library)", got, want)
	}
}
