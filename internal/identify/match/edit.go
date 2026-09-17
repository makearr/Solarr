package match

import "strings"

// EditPenalty is normalised Levenshtein distance (0 identical, 1 nothing shared) over normalized
// titles; unlike TitleSimilarity a shared prefix earns nothing, so "Voca Pebu a Maha" stays far from "Just Like Tepa".
func EditPenalty(a, b string) float64 {
	left := []rune(strings.ReplaceAll(normalizeTitle(a), " ", ""))
	right := []rune(strings.ReplaceAll(normalizeTitle(b), " ", ""))
	switch {
	case len(left) == 0 && len(right) == 0:
		return 0
	case len(left) == 0 || len(right) == 0:
		return 1
	}
	return float64(levenshtein(left, right)) / float64(max(len(left), len(right)))
}

func levenshtein(left, right []rune) int {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
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
