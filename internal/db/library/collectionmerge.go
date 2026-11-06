package library

import (
	"slices"
	"strings"
	"unicode"
)

// MergeCandidate is a collection as the duplicate rule sees it: its name and the works it holds.
type MergeCandidate struct {
	ID    int64
	Name  string
	Works []int64
}

// CollectionMerge says the Drop collections are the Keep collection said twice.
type CollectionMerge struct {
	Keep int64
	Drop []int64
}

// missingShare is how much of the larger collection's size the smaller may hold that the larger lacks.
const missingShare = 0.33

// PlanCollectionMerges folds collections that are one said twice into the larger: all works inside another, or two shared,
// a similar name and at most one (or a third of the larger) lacking. Rule in documentation/metadata-providers.md.
func PlanCollectionMerges(collections []MergeCandidate) []CollectionMerge {
	ordered := slices.Clone(collections)
	slices.SortStableFunc(ordered, func(a, b MergeCandidate) int {
		if len(a.Works) != len(b.Works) {
			return len(b.Works) - len(a.Works)
		}
		return int(a.ID - b.ID)
	})
	type keeper struct {
		candidate MergeCandidate
		held      map[int64]bool
		dropped   []int64
	}
	var keepers []*keeper
	for _, collection := range ordered {
		if len(collection.Works) == 0 {
			continue
		}
		var best *keeper
		bestShared := 0
		for _, large := range keepers {
			shared := 0
			for _, work := range collection.Works {
				if large.held[work] {
					shared++
				}
			}
			if !duplicates(collection, large.candidate.Name, len(large.held), shared) {
				continue
			}
			if best == nil || shared > bestShared {
				best, bestShared = large, shared
			}
		}
		if best == nil {
			held := make(map[int64]bool, len(collection.Works))
			for _, work := range collection.Works {
				held[work] = true
			}
			keepers = append(keepers, &keeper{candidate: collection, held: held})
			continue
		}
		best.dropped = append(best.dropped, collection.ID)
		for _, work := range collection.Works {
			best.held[work] = true
		}
	}
	var merges []CollectionMerge
	for _, one := range keepers {
		if len(one.dropped) > 0 {
			merges = append(merges, CollectionMerge{Keep: one.candidate.ID, Drop: one.dropped})
		}
	}
	return merges
}

// duplicates is the rule for one smaller collection against one larger, sharing that many works.
func duplicates(small MergeCandidate, largeName string, largeSize, shared int) bool {
	if shared == len(small.Works) {
		return true
	}
	lacking := len(small.Works) - shared
	allowed := max(1.0, missingShare*float64(largeSize))
	return shared >= 2 && float64(lacking) <= allowed && similarNames(small.Name, largeName)
}

// collectionNoise are the words that only say "this is a group", never which one.
var collectionNoise = map[string]bool{
	"the": true, "collection": true, "series": true, "saga": true, "franchise": true, "filmreihe": true, "anthology": true,
	"a": true, "of": true, "and": true, "film": true, "films": true, "movies": true, "trilogy": true,
}

// nameWords are a collection name's lower-cased letter and digit words without the noise.
func nameWords(name string) []string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	kept := words[:0]
	for _, word := range words {
		if !collectionNoise[word] {
			kept = append(kept, word)
		}
	}
	return kept
}

// similarNames holds when one name's words are all in the other's, or half of the shorter name's words are.
func similarNames(a, b string) bool {
	left, right := nameWords(a), nameWords(b)
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	if len(left) > len(right) {
		left, right = right, left
	}
	common := 0
	for _, word := range left {
		if slices.Contains(right, word) {
			common++
		}
	}
	return common == len(left) || float64(common) >= float64(len(left))/2 && common >= 1
}
