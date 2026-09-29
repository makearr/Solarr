package match

import (
	"math"
	"testing"
)

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func findPart(t *testing.T, distance Distance, dimension string) DistancePart {
	t.Helper()
	for _, part := range distance.Parts {
		if part.Dimension == dimension {
			return part
		}
	}
	t.Fatalf("no %q part in %+v", dimension, distance.Parts)
	return DistancePart{}
}

func hasPart(distance Distance, dimension string) bool {
	for _, part := range distance.Parts {
		if part.Dimension == dimension {
			return true
		}
	}
	return false
}

// A dimension with no data on either side must be entirely absent from Parts,
// not present with distance 0 or weight 0 -- those would look like agreement or
// like a deliberately zeroed-out dimension instead of "no evidence."
func TestDistance_InapplicableDimensionsAreSkippedEntirely(t *testing.T) {
	claimed := Claimed{Identity{Title: "Heroes", Year: 1977}}
	candidate := Candidate{ID: "w1", Identity: Identity{Title: "Heroes", Year: 1977}}

	distance := Compute(claimed, candidate, nil)

	for _, dimension := range []string{
		DimensionExternalID, DimensionTrackCount, DimensionNumbering, DimensionAlias, DimensionPath,
	} {
		if hasPart(distance, dimension) {
			t.Errorf("dimension %q present with no data on either side: %+v", dimension, findPart(t, distance, dimension))
		}
	}
	if !hasPart(distance, DimensionTitle) || !hasPart(distance, DimensionYear) {
		t.Errorf("expected title and year parts, got %+v", distance.Parts)
	}
}

// An exact external-id match must win overwhelmingly even against a badly
// mismatched title -- proving the weight table's dominance ordering. Both
// candidates carry an external id, so the dimension is applicable to both and
// the comparison isolates what a right-id/wrong-title candidate does against a
// wrong-id/right-title one (a candidate with no id at all would dodge the
// dimension entirely -- see TestDistance_InapplicableDimensionsAreSkippedEntirely
// for that case).
func TestDistance_ExternalIDDominatesOverBadTitleMismatch(t *testing.T) {
	claimed := Claimed{Identity{
		Title:       "Definitely The Wrong Title Entirely",
		ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-1"}},
	}}

	rightID := Candidate{ID: "right-id", Identity: Identity{
		Title:       "Some Other Totally Unrelated Name",
		ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-1"}},
	}}
	rightTitle := Candidate{ID: "right-title", Identity: Identity{
		Title:       "Definitely The Wrong Title Entirely",
		ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-2"}},
	}}

	distanceRightID := Compute(claimed, rightID, nil)
	distanceRightTitle := Compute(claimed, rightTitle, nil)

	if distanceRightID.Total >= distanceRightTitle.Total {
		t.Fatalf("matched-id/wrong-title total = %v, wrong-id/matched-title total = %v; want the id match to win",
			distanceRightID.Total, distanceRightTitle.Total)
	}

	idPart := findPart(t, distanceRightID, DimensionExternalID)
	if idPart.Distance != 0 || idPart.Weight != 5.0 {
		t.Errorf("external id part = %+v, want distance 0 weight 5.0", idPart)
	}
}

// "The Tonhut" vs "Tonhut, The" must normalize to a near-zero title distance,
// not merely a "close enough" one -- assert the actual number.
func TestDistance_TitleNormalizesArticleVariants(t *testing.T) {
	claimed := Claimed{Identity{Title: "The Tonhut"}}
	candidate := Candidate{ID: "w1", Identity: Identity{Title: "Tonhut, The"}}

	distance := Compute(claimed, candidate, nil)
	titlePart := findPart(t, distance, DimensionTitle)
	if titlePart.Distance != 0 {
		t.Errorf("title distance = %v, want 0 (identical after normalisation)", titlePart.Distance)
	}
}

// An external id present on both sides that simply disagrees is applicable
// evidence against the candidate, distance 1 -- not skipped like a genuinely
// absent id would be.
func TestDistance_ExternalIDDisagreementIsMaximalNotSkipped(t *testing.T) {
	claimed := Claimed{Identity{ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-1"}}}}
	candidate := Candidate{ID: "w1", Identity: Identity{ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-2"}}}}

	distance := Compute(claimed, candidate, nil)
	part := findPart(t, distance, DimensionExternalID)
	if part.Distance != 1 {
		t.Errorf("external id distance = %v, want 1 (disagreement, not absence)", part.Distance)
	}
}

func TestDistance_YearProximity(t *testing.T) {
	tests := []struct {
		name          string
		claimed, want int
		wantDistance  float64
	}{
		{"same year", 1977, 1977, 0},
		{"one year off", 1977, 1978, 0.2},
		{"far off saturates at one", 1977, 2020, 1},
		// 2026-09-29: a reissue's later year weighs a fifth, however late; an earlier one fully.
		{"a reissue five years later", 1982, 1977, 0.2},
		{"a reissue decades later", 2020, 1977, 0.2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			distance := Compute(
				Claimed{Identity{Year: test.claimed}},
				Candidate{ID: "w1", Identity: Identity{Year: test.want}},
				nil,
			)
			part := findPart(t, distance, DimensionYear)
			if part.Distance != test.wantDistance {
				t.Errorf("year distance = %v, want %v", part.Distance, test.wantDistance)
			}
		})
	}
}

func TestDistance_TrackCountIsRelativeNotAbsolute(t *testing.T) {
	// One track off a 3-track EP (distance 1/3) must weigh more than one track
	// off a 20-track compilation (distance 1/20).
	epDistance := Compute(Claimed{Identity{ItemCount: 3}}, Candidate{ID: "ep", Identity: Identity{ItemCount: 4}}, nil)
	compilationDistance := Compute(Claimed{Identity{ItemCount: 20}}, Candidate{ID: "comp", Identity: Identity{ItemCount: 21}}, nil)

	epPart := findPart(t, epDistance, DimensionTrackCount)
	compilationPart := findPart(t, compilationDistance, DimensionTrackCount)
	if epPart.Distance <= compilationPart.Distance {
		t.Errorf("EP off-by-one distance = %v, compilation off-by-one distance = %v, want EP strictly larger",
			epPart.Distance, compilationPart.Distance)
	}
}

func TestDistance_NumberingSetOverlap(t *testing.T) {
	claimed := Claimed{Identity{Numbering: []string{"S03E10", "#062"}}}
	candidate := Candidate{ID: "w1", Identity: Identity{Numbering: []string{"S03E10"}}}

	distance := Compute(claimed, candidate, nil)
	part := findPart(t, distance, DimensionNumbering)
	// 1 of 2 elements agree: intersect 1, union 2, distance 1 - 1/2 = 0.5.
	if part.Distance != 0.5 {
		t.Errorf("numbering distance = %v, want 0.5", part.Distance)
	}
}

func TestDistance_AliasHit(t *testing.T) {
	claimed := Claimed{Identity{Title: "Napak"}}
	candidate := Candidate{ID: "w1", Identity: Identity{
		Title:           "Pedaf Napak",
		AlternateTitles: []string{"Napak", "The Thin White Duke"},
	}}

	distance := Compute(claimed, candidate, nil)
	part := findPart(t, distance, DimensionAlias)
	if part.Distance != 0 {
		t.Errorf("alias distance = %v, want 0 (claimed title matched a known alias)", part.Distance)
	}
}

func TestDistance_AliasNoHit(t *testing.T) {
	claimed := Claimed{Identity{Title: "Someone Else Entirely"}}
	candidate := Candidate{ID: "w1", Identity: Identity{
		Title:           "Pedaf Napak",
		AlternateTitles: []string{"Napak", "The Thin White Duke"},
	}}

	distance := Compute(claimed, candidate, nil)
	part := findPart(t, distance, DimensionAlias)
	if part.Distance != 1 {
		t.Errorf("alias distance = %v, want 1 (no alias matched)", part.Distance)
	}
}

func TestDistance_PathSegmentOverlap(t *testing.T) {
	claimed := Claimed{Identity{PathSegments: []string{"Music", "Pedaf Napak", "Heroes"}}}
	candidate := Candidate{ID: "w1", Identity: Identity{PathSegments: []string{"Pedaf Napak", "Heroes"}}}

	distance := Compute(claimed, candidate, nil)
	part := findPart(t, distance, DimensionPath)
	// "music" only on the claimed side: intersect 2 ("pedaf napak", "heroes"), union 3.
	if !approxEqual(part.Distance, 1.0/3.0) {
		t.Errorf("path distance = %v, want %v", part.Distance, 1.0/3.0)
	}
}

// Weight overrides must actually change which candidate wins, not merely change
// the numbers -- construct a case where title similarity and track count
// disagree, then prove swapping which one has the higher weight flips the
// winner.
func TestDistance_WeightOverrideFlipsWinner(t *testing.T) {
	claimed := Claimed{Identity{Title: "Greatest Hits", ItemCount: 12}}

	titleMatch := Candidate{ID: "title-match", Identity: Identity{Title: "Greatest Hits", ItemCount: 30}}
	countMatch := Candidate{ID: "count-match", Identity: Identity{Title: "Rarities And B-Sides", ItemCount: 12}}

	titleHeavy := Weights{DimensionTitle: 10.0, DimensionTrackCount: 0.1}
	countHeavy := Weights{DimensionTitle: 0.1, DimensionTrackCount: 10.0}

	titleHeavyResult := Compute(claimed, titleMatch, titleHeavy)
	titleHeavyOther := Compute(claimed, countMatch, titleHeavy)
	if titleHeavyResult.Total >= titleHeavyOther.Total {
		t.Fatalf("with title weighted heavily, title-match total = %v, count-match total = %v; want title-match to win",
			titleHeavyResult.Total, titleHeavyOther.Total)
	}

	countHeavyResult := Compute(claimed, countMatch, countHeavy)
	countHeavyOther := Compute(claimed, titleMatch, countHeavy)
	if countHeavyResult.Total >= countHeavyOther.Total {
		t.Fatalf("with track count weighted heavily, count-match total = %v, title-match total = %v; want count-match to win",
			countHeavyResult.Total, countHeavyOther.Total)
	}
}

// A dimension not mentioned in the override map must still fall back to its
// section 10.2 default rather than dropping to zero.
func TestDistance_UnspecifiedWeightsFallBackToDefault(t *testing.T) {
	claimed := Claimed{Identity{
		ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-1"}},
	}}
	candidate := Candidate{ID: "w1", Identity: Identity{
		ExternalIDs: []ExternalID{{Provider: "musicbrainz", ID: "rg-2"}},
	}}

	distance := Compute(claimed, candidate, Weights{DimensionTitle: 9})
	part := findPart(t, distance, DimensionExternalID)
	if part.Weight != DefaultWeights()[DimensionExternalID] {
		t.Errorf("external id weight = %v, want the default %v", part.Weight, DefaultWeights()[DimensionExternalID])
	}
}

// No applicable dimension at all must not look like a perfect match -- an empty
// comparison carries no evidence either way.
func TestDistance_NoApplicableDimensionsGivesMaximalDistance(t *testing.T) {
	distance := Compute(Claimed{}, Candidate{ID: "w1"}, nil)
	if len(distance.Parts) != 0 {
		t.Fatalf("Parts = %+v, want none", distance.Parts)
	}
	if distance.Total != 1 {
		t.Errorf("Total = %v, want 1 (no evidence, never a free match)", distance.Total)
	}
}
