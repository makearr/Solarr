package indexer_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

func TestCategoriesIn(t *testing.T) {
	listed := []indexer.Category{
		{ID: 2000, Name: "Movies", Subcategories: []indexer.Category{{ID: 2040}}},
		{ID: 3000, Name: "Audio", Subcategories: []indexer.Category{{ID: 3010}, {ID: 3040}}},
		{ID: 5040}, {ID: 5030, Subcategories: []indexer.Category{{ID: 5030}}},
		{ID: 100123, Name: "custom"},
	}
	for name, test := range map[string]struct {
		root int
		want []int
	}{
		"the parent covers its children": {indexer.RootMusic, []int{3000}},
		"movies":                         {indexer.RootMovies, []int{2000}},
		"children without their parent":  {indexer.RootTV, []int{5030, 5040}},
		"nothing listed":                 {indexer.RootBooks, nil},
	} {
		if got := indexer.CategoriesIn(listed, test.root); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: got %v, want %v", name, got, test.want)
		}
	}
}
