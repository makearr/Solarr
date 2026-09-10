package score

import (
	"math"
	"testing"
)

func TestRankInGroups(t *testing.T) {
	groups := [][]string{{"av1", "hevc"}, {"h264"}, {"vp9"}, {"mpeg4", "xvid"}}

	if rank, found := rankInGroups(groups, "hevc"); !found || rank != 0 {
		t.Errorf("rankInGroups(hevc) = (%d, %v), want (0, true)", rank, found)
	}
	if rank, found := rankInGroups(groups, "H264"); !found || rank != 1 {
		t.Errorf("rankInGroups(H264) = (%d, %v), want (1, true) (case-insensitive)", rank, found)
	}
	if rank, found := rankInGroups(groups, "vc1"); found || rank != len(groups) {
		t.Errorf("rankInGroups(vc1) = (%d, %v), want (%d, false)", rank, found, len(groups))
	}
}

func TestListTier_UnmeasuredFieldFallsBackToWorstRank(t *testing.T) {
	tiers := map[string][][]string{"video_codec": {{"hevc"}, {"h264"}}}
	value := tierValueFor("video_codec", tiers, Facts{}, nil)
	if value.Rank != 2 {
		t.Errorf("Rank = %d, want 2 (len(groups), worse than every configured tier)", value.Rank)
	}
	if value.Label != "unmeasured" {
		t.Errorf("Label = %q, want %q", value.Label, "unmeasured")
	}
}

func TestListTier_UnlistedValueFallsBackToWorstRank(t *testing.T) {
	tiers := map[string][][]string{"video_codec": {{"hevc"}, {"h264"}}}
	facts := Facts{File: FileFacts{VideoCodec: "vc1"}}
	value := tierValueFor("video_codec", tiers, facts, nil)
	if value.Rank != 2 {
		t.Errorf("Rank = %d, want 2", value.Rank)
	}
	if value.Label != "vc1" {
		t.Errorf("Label = %q, want %q (the actual unrecognized value)", value.Label, "vc1")
	}
}

func TestNumericTier_HigherSampleRateWins(t *testing.T) {
	tiers := map[string][][]string{}
	high := tierValueFor("sample_rate", tiers, Facts{File: FileFacts{SampleRate: 96000}}, nil)
	low := tierValueFor("sample_rate", tiers, Facts{File: FileFacts{SampleRate: 44100}}, nil)
	if high.Rank >= low.Rank {
		t.Errorf("high.Rank=%d low.Rank=%d, want high < low (lower rank wins, higher sample rate is better)", high.Rank, low.Rank)
	}
}

func TestNumericTier_Lossless(t *testing.T) {
	tiers := map[string][][]string{}
	lossless := tierValueFor("lossless", tiers, Facts{File: FileFacts{Lossless: true}}, nil)
	lossy := tierValueFor("lossless", tiers, Facts{File: FileFacts{Lossless: false}}, nil)
	if lossless.Rank >= lossy.Rank {
		t.Errorf("lossless.Rank=%d lossy.Rank=%d, want lossless < lossy", lossless.Rank, lossy.Rank)
	}
}

func TestDistanceTier_UnmeasuredFallsBackToWorstRank(t *testing.T) {
	value := distanceTier("bitrate_distance", targetResult{})
	if value.Rank != math.MaxInt32 {
		t.Errorf("Rank = %d, want math.MaxInt32", value.Rank)
	}
}

func TestDistanceTier_SmallerDistanceRanksBetter(t *testing.T) {
	near := distanceTier("bitrate_distance", targetResult{distance: 0.1, distanceOK: true})
	far := distanceTier("bitrate_distance", targetResult{distance: 1.5, distanceOK: true})
	if near.Rank >= far.Rank {
		t.Errorf("near.Rank=%d far.Rank=%d, want near < far", near.Rank, far.Rank)
	}
}

// TestTierValueFor_DistanceSuffixIsALookupNotABranch proves ADR-40's own claim:
// any "<dimension>_distance" compare entry resolves through the same code path,
// keyed only by the profile's own "targets" data.
func TestTierValueFor_DistanceSuffixIsALookupNotABranch(t *testing.T) {
	distances := map[string]targetResult{
		"bitrate":     {distance: 0.2, distanceOK: true},
		"sample_rate": {distance: 0.5, distanceOK: true},
	}
	bitrate := tierValueFor("bitrate_distance", nil, Facts{}, distances)
	sampleRate := tierValueFor("sample_rate_distance", nil, Facts{}, distances)
	if bitrate.Rank >= sampleRate.Rank {
		t.Errorf("bitrate.Rank=%d sampleRate.Rank=%d, want bitrate < sampleRate (smaller distance)", bitrate.Rank, sampleRate.Rank)
	}
}

func TestTierValueFor_ScoreDimensionAlwaysAgrees(t *testing.T) {
	value := tierValueFor("score", nil, Facts{}, nil)
	if value.Rank != 0 {
		t.Errorf("Rank = %d, want 0 (score never decides a tier)", value.Rank)
	}
}

func TestBuildQualityVector_OneEntryPerCompareDimension(t *testing.T) {
	compare := []string{"resolution", "video_codec", "score"}
	tiers := map[string][][]string{"resolution": {{"2160p"}}, "video_codec": {{"hevc"}}}
	vector := buildQualityVector(compare, tiers, Facts{File: FileFacts{Resolution: "2160p", VideoCodec: "hevc"}}, nil)
	if len(vector.Tiers) != len(compare) {
		t.Fatalf("len(Tiers) = %d, want %d", len(vector.Tiers), len(compare))
	}
	for index, dimension := range compare {
		if vector.Tiers[index].Dimension != dimension {
			t.Errorf("Tiers[%d].Dimension = %q, want %q", index, vector.Tiers[index].Dimension, dimension)
		}
	}
}
