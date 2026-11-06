package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func cand(id int64, name string, works []int64) library.MergeCandidate {
	return library.MergeCandidate{ID: id, Name: name, Works: works}
}

func TestPlanCollectionMerges(t *testing.T) {
	cases := []struct {
		name string
		in   []library.MergeCandidate
		want []library.CollectionMerge
	}{
		{"none", nil, nil},
		{"a subset merges into the larger whatever its name",
			[]library.MergeCandidate{cand(1, "Aladdin Collection", []int64{1, 2}), cand(2, "Disney Classics", []int64{1, 2, 3})},
			[]library.CollectionMerge{{Keep: 2, Drop: []int64{1}}}},
		{"one shared work is not enough for the loose rule",
			[]library.MergeCandidate{cand(1, "Home Alone", []int64{1, 2, 3}), cand(2, "Home Alone Collection", []int64{3, 4})},
			nil},
		{"six sharing four with eight and a similar name merge",
			[]library.MergeCandidate{cand(1, "Kung Fu Panda Collection", []int64{1, 2, 3, 4, 5, 6, 7, 8}), cand(2, "Kung Fu Panda", []int64{1, 2, 3, 4, 9, 10})},
			[]library.CollectionMerge{{Keep: 1, Drop: []int64{2}}}},
		{"the same overlap under an unrelated name stays apart",
			[]library.MergeCandidate{cand(1, "Kung Fu Panda Collection", []int64{1, 2, 3, 4, 5, 6, 7, 8}), cand(2, "Spy Kids", []int64{1, 2, 3, 4, 9, 10})},
			nil},
		{"too many works the larger lacks stays apart",
			[]library.MergeCandidate{cand(1, "G.I. Joe Collection", []int64{1, 2, 3, 4, 5, 6, 7, 8}), cand(2, "G.I. Joe", []int64{1, 2, 9, 10, 11})},
			nil},
		{"one missing work is always allowed",
			[]library.MergeCandidate{cand(1, "Beauty and the Beast", []int64{1, 2, 3}), cand(2, "Beauty and the Beast Collection", []int64{1, 2, 9})},
			[]library.CollectionMerge{{Keep: 1, Drop: []int64{2}}}},
		{"several fold into the largest at once, an equal size keeps the lower id",
			[]library.MergeCandidate{
				cand(5, "Toy Story Collection", []int64{1, 2, 3, 4}),
				cand(3, "Toy Story", []int64{1, 2}),
				cand(4, "Toy Story Saga", []int64{3, 4}),
				cand(6, "Other", []int64{50}),
			},
			[]library.CollectionMerge{{Keep: 5, Drop: []int64{3, 4}}}},
		{"empty collections are left alone",
			[]library.MergeCandidate{cand(1, "A", nil), cand(2, "A", nil)},
			nil},
	}
	for _, c := range cases {
		if got := library.PlanCollectionMerges(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
