package match

import (
	"slices"
	"strings"
)

// EditPenalty is normalised Levenshtein distance (0 identical, 1 nothing shared) over normalized
// titles; unlike TitleSimilarity a shared prefix earns nothing, so "Voca Pebu a Maha" stays far from "Voca Pebu Tepa".
func EditPenalty(a, b string) float64 {
	// Equal titles are the common case, and normalising and comparing them would only confirm it.
	if a == b {
		return 0
	}
	left := []rune(strings.ReplaceAll(normalizeTitle(a), " ", ""))
	right := []rune(strings.ReplaceAll(normalizeTitle(b), " ", ""))
	switch {
	case len(left) == 0 && len(right) == 0 && symbolTitlesEqual(a, b):
		return 0
	case len(left) == 0 || len(right) == 0:
		return 1
	case slices.Equal(left, right):
		return 0
	}
	return float64(levenshtein(left, right)) / float64(max(len(left), len(right)))
}

func levenshtein(left, right []rune) int {
	rows := make([]int, 2*(len(right)+1))
	previous, current := rows[:len(right)+1], rows[len(right)+1:]
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(left); i++ {
		current[0] = i
		for j := 1; j <= len(right); j++ {
			substitution := previous[j-1]
			if left[i-1] != right[j-1] {
				substitution++
			}
			current[j] = min(previous[j]+1, current[j-1]+1, substitution)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

// symbolTitlesEqual compares titles that normalise to nothing as they are written, case and outer space aside.
func symbolTitlesEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
