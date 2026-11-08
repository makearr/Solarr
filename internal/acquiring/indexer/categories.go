package indexer

import "slices"

// The roots of Newznab's media categories; each covers the thousand ids above it.
const (
	RootMovies = 2000
	RootMusic  = 3000
	RootTV     = 5000
	RootAdult  = 6000
	RootBooks  = 7000
)

// CategoriesIn answers the ids to ask an indexer in for the media type rooted at root, from the categories its own caps
// list: the root alone when the indexer has it (a parent search takes its sub-categories), else every id it lists
// inside the range. Nil means the indexer lists nothing for the type, so it cannot find any.
func CategoriesIn(listed []Category, root int) []int {
	var found []int
	var walk func(categories []Category)
	walk = func(categories []Category) {
		for _, category := range categories {
			if category.ID >= root && category.ID < root+1000 {
				found = append(found, category.ID)
			}
			walk(category.Subcategories)
		}
	}
	walk(listed)
	if slices.Contains(found, root) {
		return []int{root}
	}
	slices.Sort(found)
	return slices.Compact(found)
}
