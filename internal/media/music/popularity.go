package music

import "math"

// PopularityScore puts a count of listeners or fans on the library's 0..10 rating scale: two points a power of ten, so a
// thousand people is 6 and a hundred thousand is 10 (the scale is ours; the providers publish counts, not scores).
func PopularityScore(people int64) float64 {
	if people <= 0 {
		return 0
	}
	return math.Min(10, 2*math.Log10(1+float64(people)))
}
