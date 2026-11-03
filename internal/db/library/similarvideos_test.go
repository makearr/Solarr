package library

import (
	"database/sql"
	"math/bits"
	"math/rand"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// sameFrames is a full set of frames that match whatever else they are compared with, for tests about the mean hash.
// mediumBits is the default level's bound, the one the cases below were written against.
var mediumBits = SimilarityMedium.MaxBits()

var sameFrames = []FrameHash{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 6}, {6, 7}}

func hashed(video SimilarVideo, mean uint64) hashedVideo {
	return hashedVideo{video: video, mean: mean, frames: sameFrames}
}

func TestGroupSimilar_JoinsNearHashesAcrossBandsAndLeavesStrangersAlone(t *testing.T) {
	base := uint64(0x0123456789ABCDEF)
	hashes := []uint64{
		base,
		base ^ 0b1011,        // 3 bits off, low band
		base ^ (0b101 << 60), // 2 bits off, high band
		^base,                // far
		0xF0F0F0F0F0F0F0F0,   // far
	}
	videos := make([]hashedVideo, len(hashes))
	for index := range videos {
		videos[index] = hashed(SimilarVideo{MediaObjectID: int64(index + 1), SizeBytes: int64(100 - index)}, hashes[index])
	}
	groups := groupSimilar(videos, mediumBits, 10)
	want := []SimilarGroup{{MaxDistance: 5, Videos: []SimilarVideo{
		{MediaObjectID: 1, SizeBytes: 100}, {MediaObjectID: 2, SizeBytes: 99}, {MediaObjectID: 3, SizeBytes: 98},
	}}}
	if !reflect.DeepEqual(groups, want) {
		t.Errorf("groups = %+v, want %+v", groups, want)
	}
}

func TestGroupSimilar_NineBitsApartAreNotOneCut(t *testing.T) {
	videos := []hashedVideo{hashed(SimilarVideo{MediaObjectID: 1}, 0), hashed(SimilarVideo{MediaObjectID: 2}, 0x1FF)}
	if groups := groupSimilar(videos, mediumBits, 10); groups != nil {
		t.Errorf("groups = %+v, want none", groups)
	}
}

func TestGroupSimilar_EpisodesWithTheSameOpeningButOtherLengthsAreNotOneCut(t *testing.T) {
	videos := []hashedVideo{
		hashed(SimilarVideo{MediaObjectID: 1, DurationMs: 2_700_000}, 0),
		hashed(SimilarVideo{MediaObjectID: 2, DurationMs: 2_640_000}, 0b11),
	}
	if groups := groupSimilar(videos, mediumBits, 10); groups != nil {
		t.Errorf("groups = %+v, want none", groups)
	}
	videos[1].video.DurationMs = 2_690_000
	want := []SimilarGroup{{MaxDistance: 2, Videos: []SimilarVideo{videos[0].video, videos[1].video}}}
	if groups := groupSimilar(videos, mediumBits, 10); !reflect.DeepEqual(groups, want) {
		t.Errorf("groups = %+v, want %+v", groups, want)
	}
}

// scene is a grey 32x32 frame: one shared backdrop (the set, the studio) with the shot's own shapes in front of it.
func scene(shot int64) []byte {
	frame := make([]byte, 32*32)
	paint := func(seed int64, blobs int) {
		random := rand.New(rand.NewSource(seed))
		for range blobs {
			cx, cy, radius, level := random.Intn(32), random.Intn(32), 3+random.Intn(8), byte(random.Intn(255))
			for y := range 32 {
				for x := range 32 {
					if (x-cx)*(x-cx)+(y-cy)*(y-cy) < radius*radius {
						frame[y*32+x] = level
					}
				}
			}
		}
	}
	paint(1000, 12)
	paint(shot, 4)
	return frame
}

func regrade(frame []byte, seed int64) []byte {
	random := rand.New(rand.NewSource(seed))
	out := make([]byte, len(frame))
	for index, value := range frame {
		out[index] = byte(max(0, min(255, int(value)*9/10+12+random.Intn(7)-3)))
	}
	return out
}

func videoOf(t *testing.T, id, durationMs int64, frames [][]byte) hashedVideo {
	t.Helper()
	hash, err := mediatool.HashOfFrames(frames)
	if err != nil {
		t.Fatal(err)
	}
	video := hashedVideo{video: SimilarVideo{MediaObjectID: id, SizeBytes: 1000 - id, DurationMs: durationMs}, mean: hash.Mean}
	for _, frame := range hash.Frames {
		video.frames = append(video.frames, FrameHash{Slot: frame.Slot, Hash: frame.Hash})
	}
	return video
}

// Two episodes of one show: the same set, the same kinds of shots, so the same mean frame, but other shots at each moment.
func TestGroupSimilar_EpisodesSharingABackdropAreNotOneCutAndAReencodeIs(t *testing.T) {
	var episode, next, reencode [][]byte
	for shot := range int64(7) {
		episode = append(episode, scene(shot))
		next = append(next, scene((shot+3)%7))
		reencode = append(reencode, regrade(scene(shot), shot))
	}
	reencode[5] = nil
	first := videoOf(t, 1, 2_700_000, episode)
	second := videoOf(t, 2, 2_700_000, next)
	again := videoOf(t, 3, 2_699_000, reencode)
	if distance := bits.OnesCount64(first.mean ^ second.mean); distance > mediumBits {
		t.Fatalf("the episodes' mean frames are %d bits apart; the case needs them within %d", distance, mediumBits)
	}
	if groups := groupSimilar([]hashedVideo{first, second}, mediumBits, 10); groups != nil {
		t.Errorf("two episodes: groups = %+v, want none", groups)
	}
	want := []SimilarGroup{{MaxDistance: bits.OnesCount64(first.mean ^ again.mean), Videos: []SimilarVideo{first.video, again.video}}}
	if groups := groupSimilar([]hashedVideo{first, second, again}, mediumBits, 10); !reflect.DeepEqual(groups, want) {
		t.Errorf("with a re-encode: groups = %+v, want %+v", groups, want)
	}
}

func TestFramesAlike_TwoThirdsOfTheSharedSlotsInOrder(t *testing.T) {
	frames := func(slots ...int) []FrameHash {
		var out []FrameHash
		for _, slot := range slots {
			out = append(out, FrameHash{Slot: slot, Hash: uint64(slot+1) * 0x9E3779B97F4A7C15})
		}
		return out
	}
	far := func(list []FrameHash, slots ...int) []FrameHash {
		out := append([]FrameHash(nil), list...)
		for index := range out {
			for _, slot := range slots {
				if out[index].Slot == slot {
					out[index].Hash = ^out[index].Hash
				}
			}
		}
		return out
	}
	all := frames(0, 1, 2, 3, 4, 5, 6)
	for _, test := range []struct {
		name          string
		first, second []FrameHash
		want          bool
	}{
		{"identical", all, all, true},
		{"five of seven", all, far(all, 1, 4), true},
		{"four of seven", all, far(all, 1, 4, 6), false},
		{"three of five shared", frames(0, 1, 2, 3, 4, 5), far(frames(1, 2, 3, 4, 5, 6), 2, 5), false},
		{"four of five shared", frames(0, 1, 2, 3, 4), far(frames(0, 1, 2, 3, 4, 6), 2), true},
		{"three shared slots", frames(0, 1, 2), frames(0, 1, 2), false},
		{"other slots", frames(0, 2, 4, 6), frames(1, 3, 5), false},
		{"one shifted along", all, []FrameHash{{1, all[0].Hash}, {2, all[1].Hash}, {3, all[2].Hash}, {4, all[3].Hash}, {5, all[4].Hash}, {6, all[5].Hash}}, false},
		{"nothing", nil, nil, false},
	} {
		if got := framesAlike(test.first, test.second, 10); got != test.want {
			t.Errorf("%s: framesAlike = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestFrameHashes_RoundTripAndRefuseATornRecord(t *testing.T) {
	frames := []FrameHash{{0, 0xFEDCBA9876543210}, {6, 1}}
	encoded := encodeFrameHashes(frames)
	if want := []byte{0, 0xFE, 0xDC, 0xBA, 0x98, 0x76, 0x54, 0x32, 0x10, 6, 0, 0, 0, 0, 0, 0, 0, 1}; !reflect.DeepEqual(encoded, want) {
		t.Errorf("encoded = %v, want %v", encoded, want)
	}
	decoded, err := decodeFrameHashes(encoded)
	if err != nil || !reflect.DeepEqual(decoded, frames) {
		t.Errorf("decoded = %+v, %v; want %+v", decoded, err, frames)
	}
	if decoded, err := decodeFrameHashes(nil); err != nil || !reflect.DeepEqual(decoded, []FrameHash{}) {
		t.Errorf("empty = %+v, %v", decoded, err)
	}
	if _, err := decodeFrameHashes(encoded[:10]); err == nil {
		t.Error("a torn record was accepted")
	}
}

func TestGroupSimilar_TheLevelSetsHowFarApartTwoHashesMayBe(t *testing.T) {
	videos := []hashedVideo{hashed(SimilarVideo{MediaObjectID: 1}, 0), hashed(SimilarVideo{MediaObjectID: 2}, 0b1111111111)}
	for _, test := range []struct {
		level SimilarityLevel
		bits  int
		want  bool
	}{
		{SimilarityExact, 0, false}, {SimilarityHigh, 4, false}, {SimilarityMedium, 8, false}, {SimilarityLow, 10, true},
	} {
		if got := test.level.MaxBits(); got != test.bits {
			t.Errorf("%s: %d bits, want %d", test.level, got, test.bits)
		}
		if got := groupSimilar(videos, test.level.MaxBits(), 10) != nil; got != test.want {
			t.Errorf("%s: ten bits apart grouped = %v, want %v", test.level, got, test.want)
		}
	}
	same := []hashedVideo{hashed(SimilarVideo{MediaObjectID: 1}, 7), hashed(SimilarVideo{MediaObjectID: 2}, 7)}
	if groups := groupSimilar(same, SimilarityExact.MaxBits(), 10); len(groups) != 1 || groups[0].MaxDistance != 0 {
		t.Errorf("exact: groups = %+v, want the one identical pair", groups)
	}
}

func TestCollectVideosCountsAFileLinkedAtManyPathsOnce(t *testing.T) {
	row := func(id int64, rel string, root int64) librarydb.ListHashedVideosRow {
		return librarydb.ListHashedVideosRow{ID: id, Phash: sql.NullInt64{Int64: 5, Valid: true}, RootFolderID: root, RelPath: rel}
	}
	rows := []librarydb.ListHashedVideosRow{row(1, "a/one.mkv", 1), row(1, "b/one.mkv", 1), row(2, "c/two.mkv", 1), row(3, "d/other.mkv", 9)}
	got, err := collectVideos(rows, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	var counts []int
	for _, video := range got {
		paths, counts = append(paths, video.video.RelPath), append(counts, video.video.PathCount)
	}
	if !reflect.DeepEqual(paths, []string{"a/one.mkv", "c/two.mkv"}) || !reflect.DeepEqual(counts, []int{2, 1}) {
		t.Fatalf("videos = %v with path counts %v, want [a/one.mkv c/two.mkv] with [2 1]", paths, counts)
	}
}
