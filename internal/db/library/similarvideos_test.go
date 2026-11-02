package library

import (
	"reflect"
	"testing"
)

func TestGroupSimilar_JoinsNearHashesAcrossBandsAndLeavesStrangersAlone(t *testing.T) {
	base := uint64(0x0123456789ABCDEF)
	hashes := []uint64{
		base,
		base ^ 0b1011,        // 3 bits off, low band
		base ^ (0b101 << 60), // 2 bits off, high band
		^base,                // far
		0xF0F0F0F0F0F0F0F0,   // far
	}
	videos := make([]SimilarVideo, len(hashes))
	for index := range videos {
		videos[index] = SimilarVideo{MediaObjectID: int64(index + 1), SizeBytes: int64(100 - index)}
	}
	groups := groupSimilar(videos, hashes, 10)
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want one", groups)
	}
	var ids []int64
	for _, video := range groups[0].Videos {
		ids = append(ids, video.MediaObjectID)
	}
	if want := []int64{1, 2, 3}; !reflect.DeepEqual(ids, want) {
		t.Errorf("group ids = %v, want %v", ids, want)
	}
	if groups[0].MaxDistance != 5 {
		t.Errorf("MaxDistance = %d, want 5", groups[0].MaxDistance)
	}
}

func TestGroupSimilar_NineBitsApartAreNotOneCut(t *testing.T) {
	hashes := []uint64{0, 0x1FF}
	videos := []SimilarVideo{{MediaObjectID: 1}, {MediaObjectID: 2}}
	if groups := groupSimilar(videos, hashes, 10); len(groups) != 0 {
		t.Errorf("groups = %+v, want none", groups)
	}
}

func TestGroupSimilar_EpisodesWithTheSameOpeningButOtherLengthsAreNotOneCut(t *testing.T) {
	hashes := []uint64{0, 0b11}
	videos := []SimilarVideo{{MediaObjectID: 1, DurationMs: 2_700_000}, {MediaObjectID: 2, DurationMs: 2_640_000}}
	if groups := groupSimilar(videos, hashes, 10); len(groups) != 0 {
		t.Errorf("groups = %+v, want none", groups)
	}
	videos[1].DurationMs = 2_690_000
	if groups := groupSimilar(videos, hashes, 10); len(groups) != 1 {
		t.Errorf("groups = %+v, want the re-encode joined", groups)
	}
}
