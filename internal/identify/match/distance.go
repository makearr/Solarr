package match

import "fmt"

// Dimension names, shared between DistancePart.Dimension and Weights keys.
const (
	DimensionExternalID = "external_id"
	DimensionTitle      = "title"
	DimensionYear       = "year"
	// DimensionTrackCount is the one "item count" dimension for every media type:
	// tracks for an album, episodes for a season. Splitting it per media type
	// would duplicate identical distance code for no behavioural difference.
	DimensionTrackCount = "track_count"
	DimensionNumbering  = "numbering"
	DimensionAlias      = "alias"
	DimensionPath       = "path"
)

// Weights lets a caller override section 10.2's default per-dimension weight --
// a dimension not present here falls back to DefaultWeights. Anime needs its own
// numbers; a movie profile does not.
type Weights map[string]float64

// DefaultWeights is section 10.2's table, verbatim.
func DefaultWeights() Weights {
	return Weights{
		DimensionExternalID: 5.0,
		DimensionTitle:      3.0,
		DimensionYear:       2.0,
		DimensionTrackCount: 2.0,
		DimensionNumbering:  2.0,
		DimensionAlias:      1.0,
		DimensionPath:       1.5,
	}
}

func resolveWeights(weights Weights) Weights {
	resolved := DefaultWeights()
	for dimension, weight := range weights {
		resolved[dimension] = weight
	}
	return resolved
}

// Distance is Compute's answer for one candidate: every dimension that had data
// on both sides, and the weighted total (ADR-10 house style -- data, not a
// formatted verdict).
type Distance struct {
	Parts []DistancePart
	Total float64 // sum(distance*weight) / sum(weight) over Parts; lower is better
}

// DistancePart is one dimension's contribution. A dimension with no data on
// either side has no DistancePart at all -- it never appears with a zero weight,
// so Parts always shows exactly what evidence existed.
type DistancePart struct {
	Dimension string
	Distance  float64 // 0 = perfect, 1 = no agreement
	Weight    float64
	Detail    string
}

// Compute measures the Distance between a claimed identity and one candidate.
// Each dimension is skipped entirely, weight and all, when neither side has data
// for it; a dimension with data on both sides that simply disagrees still counts
// against the total. When nothing was comparable at all, Total is 1 -- an empty
// comparison carries no evidence either way and must never look like a match.
func Compute(claimed Claimed, candidate Candidate, weights Weights) Distance {
	resolved := resolveWeights(weights)
	var parts []DistancePart

	add := func(part DistancePart, ok bool) {
		if !ok {
			return
		}
		part.Weight = resolved[part.Dimension]
		parts = append(parts, part)
	}

	add(externalIDDistance(claimed.ExternalIDs, candidate.ExternalIDs))
	add(titleDistance(claimed.Title, candidate.Title))
	add(yearDistance(claimed.Year, candidate.Year))
	add(itemCountDistance(claimed.ItemCount, candidate.ItemCount))
	add(setDistance(DimensionNumbering, claimed.Numbering, candidate.Numbering))
	add(aliasDistance(claimed, candidate))
	add(setDistance(DimensionPath, claimed.PathSegments, candidate.PathSegments))

	var weightedSum, weightSum float64
	for _, part := range parts {
		weightedSum += part.Distance * part.Weight
		weightSum += part.Weight
	}
	total := 1.0
	if weightSum > 0 {
		total = weightedSum / weightSum
	}
	return Distance{Parts: parts, Total: total}
}

// externalIDDistance only applies when both sides carry at least one external id
// -- a candidate simply lacking any external id is not evidence against it, only
// a disagreeing id is.
func externalIDDistance(claimed, candidate []ExternalID) (DistancePart, bool) {
	if len(claimed) == 0 || len(candidate) == 0 {
		return DistancePart{}, false
	}
	for _, c := range claimed {
		for _, k := range candidate {
			if c.Provider == k.Provider && c.ID == k.ID {
				return DistancePart{Dimension: DimensionExternalID, Distance: 0,
					Detail: fmt.Sprintf("%s %s matched", c.Provider, c.ID)}, true
			}
		}
	}
	return DistancePart{Dimension: DimensionExternalID, Distance: 1,
		Detail: "external id present on both sides but none matched"}, true
}

// titleDistance takes the better (lower-distance) of token-set overlap and
// Jaro-Winkler similarity, per section 10.2.
func titleDistance(claimed, candidate string) (DistancePart, bool) {
	if claimed == "" || candidate == "" {
		return DistancePart{}, false
	}
	similarity, method := bestTitleSimilarity(claimed, candidate)
	return DistancePart{Dimension: DimensionTitle, Distance: 1 - similarity,
		Detail: fmt.Sprintf("title %.2f %s", similarity, method)}, true
}

// bestTitleSimilarity is the shared "best of token-set overlap and Jaro-Winkler"
// comparison behind both titleDistance and TitleSimilarity.
func bestTitleSimilarity(a, b string) (score float64, method string) {
	normalizedA, normalizedB := normalizeTitle(a), normalizeTitle(b)
	tokenSimilarity := jaccardWords(normalizedA, normalizedB)
	jaroWinklerSimilarity := jaroWinkler(normalizedA, normalizedB)

	if jaroWinklerSimilarity > tokenSimilarity {
		return jaroWinklerSimilarity, "jaro-winkler"
	}
	return tokenSimilarity, "token-set"
}

// TitleSimilarity is section 10.2's title comparison (the better of token-set
// overlap and Jaro-Winkler similarity, over case/accent/punctuation-normalised
// titles), exported for a caller matching two titles directly rather than through
// Compute's full Distance -- internal/media/music's tracklist alignment (section 19.6)
// is the first of these.
func TitleSimilarity(a, b string) float64 {
	score, _ := bestTitleSimilarity(a, b)
	return score
}

// yearDistanceScale is how many years apart before the year dimension saturates
// at maximum distance.
const yearDistanceScale = 5.0

// laterYearFactor is how much a claimed year after the candidate's counts: a reissue carries its
// own later year, as Lidarr accepts (2026-09-29). An earlier year counts fully -- nothing predates
// its own work.
const laterYearFactor = 0.2

func yearDistance(claimed, candidate int) (DistancePart, bool) {
	if claimed == 0 || candidate == 0 {
		return DistancePart{}, false
	}
	diff := claimed - candidate
	factor := laterYearFactor
	if diff < 0 {
		diff, factor = -diff, 1
	}
	distance := min(float64(diff)/yearDistanceScale, 1) * factor
	return DistancePart{Dimension: DimensionYear, Distance: distance,
		Detail: fmt.Sprintf("year %d vs %d", claimed, candidate)}, true
}

// itemCountDistance is relative, not absolute: a difference of one track matters
// far more on a 3-track EP than a 20-track compilation.
func itemCountDistance(claimed, candidate int) (DistancePart, bool) {
	if claimed <= 0 || candidate <= 0 {
		return DistancePart{}, false
	}
	diff := claimed - candidate
	if diff < 0 {
		diff = -diff
	}
	denominator := max(claimed, candidate)
	distance := min(float64(diff)/float64(denominator), 1)
	return DistancePart{Dimension: DimensionTrackCount, Distance: distance,
		Detail: fmt.Sprintf("count %d vs %d", claimed, candidate)}, true
}

// setDistance is a Jaccard distance over whole normalized elements -- shared by
// numbering (every known scheme at once) and path (folder segments), both
// "does this set of strings overlap" comparisons where the element, not the
// word, is the atomic unit.
func setDistance(dimension string, claimed, candidate []string) (DistancePart, bool) {
	if len(claimed) == 0 || len(candidate) == 0 {
		return DistancePart{}, false
	}
	claimedSet := normalizeElements(claimed)
	candidateSet := normalizeElements(candidate)

	var intersect int
	for element := range claimedSet {
		if _, ok := candidateSet[element]; ok {
			intersect++
		}
	}
	union := len(claimedSet) + len(candidateSet) - intersect

	var similarity float64
	if union > 0 {
		similarity = float64(intersect) / float64(union)
	}
	return DistancePart{Dimension: dimension, Distance: 1 - similarity,
		Detail: fmt.Sprintf("%d/%d elements agree", intersect, union)}, true
}

func normalizeElements(elements []string) map[string]struct{} {
	set := make(map[string]struct{}, len(elements))
	for _, element := range elements {
		normalized := normalizeTitle(element)
		if normalized == "" {
			continue
		}
		set[normalized] = struct{}{}
	}
	return set
}

// aliasDistance applies only when the candidate carries known aliases at all --
// a work with no recorded alternate titles offers no evidence either way. It
// checks claimed's title and its own alternates against every candidate alias.
func aliasDistance(claimed Claimed, candidate Candidate) (DistancePart, bool) {
	if len(candidate.AlternateTitles) == 0 {
		return DistancePart{}, false
	}

	claimedForms := claimed.AlternateTitles
	if claimed.Title != "" {
		claimedForms = append([]string{claimed.Title}, claimedForms...)
	}

	for _, form := range claimedForms {
		normalized := normalizeTitle(form)
		if normalized == "" {
			continue
		}
		for _, alias := range candidate.AlternateTitles {
			if normalizeTitle(alias) == normalized {
				return DistancePart{Dimension: DimensionAlias, Distance: 0,
					Detail: fmt.Sprintf("alias %q matched", alias)}, true
			}
		}
	}
	return DistancePart{Dimension: DimensionAlias, Distance: 1, Detail: "no alias in common"}, true
}
