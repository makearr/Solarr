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
	value := tierValueFor("video_codec", tiers, Facts{}, 0, false)
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
	value := tierValueFor("video_codec", tiers, facts, 0, false)
	if value.Rank != 2 {
		t.Errorf("Rank = %d, want 2", value.Rank)
	}
	if value.Label != "vc1" {
		t.Errorf("Label = %q, want %q (the actual unrecognized value)", value.Label, "vc1")
	}
}

func TestNumericTier_HigherSampleRateWins(t *testing.T) {
	tiers := map[string][][]string{}
	high := tierValueFor("sample_rate", tiers, Facts{File: FileFacts{SampleRate: 96000}}, 0, false)
	low := tierValueFor("sample_rate", tiers, Facts{File: FileFacts{SampleRate: 44100}}, 0, false)
	if high.Rank >= low.Rank {
		t.Errorf("high.Rank=%d low.Rank=%d, want high < low (lower rank wins, higher sample rate is better)", high.Rank, low.Rank)
	}
}

func TestNumericTier_Lossless(t *testing.T) {
	tiers := map[string][][]string{}
	lossless := tierValueFor("lossless", tiers, Facts{File: FileFacts{Lossless: true}}, 0, false)
	lossy := tierValueFor("lossless", tiers, Facts{File: FileFacts{Lossless: false}}, 0, false)
	if lossless.Rank >= lossy.Rank {
		t.Errorf("lossless.Rank=%d lossy.Rank=%d, want lossless < lossy", lossless.Rank, lossy.Rank)
	}
}

func TestBitrateDistanceTier_UnmeasuredFallsBackToWorstRank(t *testing.T) {
	value := bitrateDistanceTier(0, false)
	if value.Rank != math.MaxInt32 {
		t.Errorf("Rank = %d, want math.MaxInt32", value.Rank)
	}
}

func TestBitrateDistanceTier_SmallerDistanceRanksBetter(t *testing.T) {
	near := bitrateDistanceTier(0.1, true)
	far := bitrateDistanceTier(1.5, true)
	if near.Rank >= far.Rank {
		t.Errorf("near.Rank=%d far.Rank=%d, want near < far", near.Rank, far.Rank)
	}
}

func TestTierValueFor_ScoreDimensionAlwaysAgrees(t *testing.T) {
	value := tierValueFor("score", nil, Facts{}, 0, false)
	if value.Rank != 0 {
		t.Errorf("Rank = %d, want 0 (score never decides a tier)", value.Rank)
	}
}

func TestBuildQualityVector_OneEntryPerCompareDimension(t *testing.T) {
	compare := []string{"resolution", "video_codec", "score"}
	tiers := map[string][][]string{"resolution": {{"2160p"}}, "video_codec": {{"hevc"}}}
	vector := buildQualityVector(compare, tiers, Facts{File: FileFacts{Resolution: "2160p", VideoCodec: "hevc"}}, 0, false)
	if len(vector.Tiers) != len(compare) {
		t.Fatalf("len(Tiers) = %d, want %d", len(vector.Tiers), len(compare))
	}
	for index, dimension := range compare {
		if vector.Tiers[index].Dimension != dimension {
			t.Errorf("Tiers[%d].Dimension = %q, want %q", index, vector.Tiers[index].Dimension, dimension)
		}
	}
}
