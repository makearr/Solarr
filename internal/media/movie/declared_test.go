package movie

import (
	"slices"
	"testing"
)

func TestDeclaredFranchises_NamesTheFranchisesMostOfACollectionIsInAndTheirParents(t *testing.T) {
	direct := map[string][]int64{"QIronMan": {1, 2, 3}, "QMCU": {1, 2, 3, 9}, "QOther": {3}}
	parents := map[string][]string{"QIronMan": {"QMCU"}}
	got := declaredFranchises([]int64{1, 2, 3}, direct, parents)
	if want := []string{"QIronMan", "QMCU"}; !slices.Equal(got, want) {
		t.Errorf("declared = %v, want %v", got, want)
	}
	if got := declaredFranchises([]int64{3, 4, 5}, direct, parents); len(got) != 0 {
		t.Errorf("declared for a collection with one film in a franchise = %v, want none", got)
	}
}
