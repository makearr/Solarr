package library

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/bits"
	"slices"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// SimilarVideos finds videos that look like one another by perceptual hash (ADR-35): the same cut in another
// encode, size or container. It only reads.
type SimilarVideos struct{ store *store.Store }

// NewSimilarVideos builds it over an open library store.
func NewSimilarVideos(opened *store.Store) SimilarVideos { return SimilarVideos{store: opened} }

// SimilarVideo is one hashed file.
type SimilarVideo struct {
	MediaObjectID int64
	SizeBytes     int64
	// DurationMs is zero when unprobed; the duration gate then lets the pair through.
	DurationMs   int64
	RootFolderID int64
	RelPath      string
}

// SimilarGroup is files whose hashes chain together within the distance; the largest comes first.
type SimilarGroup struct {
	// MaxDistance is the widest gap between the mean-frame hashes of two neighbours that joined the group, in bits.
	MaxDistance int
	Videos      []SimilarVideo
}

// similarMaxBits is the Hamming distance between mean-frame hashes at or under which two videos are compared frame by frame.
const similarMaxBits = 8

// FrameHash is one sampled frame's perceptual hash; Slot is the moment of the video it was taken at, in order.
type FrameHash struct {
	Slot int
	Hash uint64
}

// frameMaxBits is the Hamming distance at or under which two frames taken at one slot show the same picture.
const frameMaxBits = 10

// minSharedFrames is how many slots two videos must both have a frame for before they can be called alike.
const minSharedFrames = 4

// framesAlike is whether two videos show the same pictures at the same moments: at least two thirds of the slots
// both sampled match. One show's episodes share a look, so the mean frame alone joins them (found 2026-11-02).
func framesAlike(first, second []FrameHash) bool {
	shared, matched := 0, 0
	for i, j := 0, 0; i < len(first) && j < len(second); {
		switch {
		case first[i].Slot < second[j].Slot:
			i++
		case first[i].Slot > second[j].Slot:
			j++
		default:
			shared++
			if bits.OnesCount64(first[i].Hash^second[j].Hash) <= frameMaxBits {
				matched++
			}
			i++
			j++
		}
	}
	return shared >= minSharedFrames && matched*3 >= shared*2
}

// frameRecordBytes is one stored frame: its slot, then the hash big-endian.
const frameRecordBytes = 9

func encodeFrameHashes(frames []FrameHash) []byte {
	encoded := make([]byte, 0, len(frames)*frameRecordBytes)
	for _, frame := range frames {
		encoded = append(encoded, byte(frame.Slot))
		encoded = binary.BigEndian.AppendUint64(encoded, frame.Hash)
	}
	return encoded
}

func decodeFrameHashes(encoded []byte) ([]FrameHash, error) {
	if len(encoded)%frameRecordBytes != 0 {
		return nil, fmt.Errorf("frame hashes of %d bytes", len(encoded))
	}
	frames := make([]FrameHash, 0, len(encoded)/frameRecordBytes)
	for offset := 0; offset < len(encoded); offset += frameRecordBytes {
		frames = append(frames, FrameHash{Slot: int(encoded[offset]), Hash: binary.BigEndian.Uint64(encoded[offset+1 : offset+frameRecordBytes])})
	}
	return frames, nil
}

// hashedVideo is one video with what it is compared by.
type hashedVideo struct {
	video  SimilarVideo
	mean   uint64
	frames []FrameHash
}

// sameLength is whether two durations can be one cut: episodes sharing an opening differ by far more than an encode does.
func sameLength(first, second int64) bool {
	if first == 0 || second == 0 {
		return true
	}
	return max(first, second)-min(first, second) <= max(5_000, max(first, second)/50)
}

// similarBands splits a hash for the pigeonhole index: two hashes within similarMaxBits bits agree on at least one of
// similarMaxBits+1 bands, so only files sharing a band are compared.
const similarBands = similarMaxBits + 1

// Groups answers the groups of similar videos among those with a path in rootFolderIDs, most files first.
func (s SimilarVideos) Groups(ctx context.Context, rootFolderIDs []int64, limit int) ([]SimilarGroup, error) {
	rows, err := librarydb.New(s.store.Read()).ListHashedVideos(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list hashed videos: %w", err)
	}
	inRoots := make(map[int64]bool, len(rootFolderIDs))
	for _, id := range rootFolderIDs {
		inRoots[id] = true
	}
	var videos []hashedVideo
	for _, row := range rows {
		if !inRoots[row.RootFolderID] || !row.Phash.Valid {
			continue
		}
		frames, err := decodeFrameHashes(row.Frames)
		if err != nil {
			return nil, fmt.Errorf("library: video %d: %w", row.ID, err)
		}
		videos = append(videos, hashedVideo{
			video: SimilarVideo{MediaObjectID: row.ID, SizeBytes: row.SizeBytes, DurationMs: row.DurationMs.Int64, RootFolderID: row.RootFolderID, RelPath: row.RelPath},
			mean:  uint64(row.Phash.Int64), frames: frames,
		})
	}
	return groupSimilar(videos, limit), nil
}

// groupSimilar joins videos whose mean hashes are within similarMaxBits, lengths agree and frames match into groups of
// two or more.
func groupSimilar(videos []hashedVideo, limit int) []SimilarGroup {
	parent := make([]int, len(videos))
	for index := range parent {
		parent[index] = index
	}
	find := func(index int) int {
		for parent[index] != index {
			parent[index] = parent[parent[index]]
			index = parent[index]
		}
		return index
	}
	widest := make(map[int]int)
	for band := range similarBands {
		low, high := band*64/similarBands, (band+1)*64/similarBands
		mask := uint64(1)<<(high-low) - 1
		buckets := make(map[uint64][]int)
		for index, video := range videos {
			key := video.mean >> low & mask
			buckets[key] = append(buckets[key], index)
		}
		for _, members := range buckets {
			for i, first := range members {
				for _, second := range members[i+1:] {
					distance := bits.OnesCount64(videos[first].mean ^ videos[second].mean)
					if distance > similarMaxBits || !sameLength(videos[first].video.DurationMs, videos[second].video.DurationMs) ||
						!framesAlike(videos[first].frames, videos[second].frames) {
						continue
					}
					rootFirst, rootSecond := find(first), find(second)
					if rootFirst != rootSecond {
						parent[rootSecond] = rootFirst
						widest[rootFirst] = max(widest[rootFirst], widest[rootSecond])
					}
					widest[rootFirst] = max(widest[rootFirst], distance)
				}
			}
		}
	}
	byRoot := make(map[int][]SimilarVideo)
	for index, video := range videos {
		byRoot[find(index)] = append(byRoot[find(index)], video.video)
	}
	var groups []SimilarGroup
	for root, members := range byRoot {
		if len(members) > 1 {
			slices.SortFunc(members, func(a, b SimilarVideo) int { return int(b.SizeBytes - a.SizeBytes) })
			groups = append(groups, SimilarGroup{MaxDistance: widest[root], Videos: members})
		}
	}
	slices.SortFunc(groups, func(a, b SimilarGroup) int {
		if len(a.Videos) != len(b.Videos) {
			return len(b.Videos) - len(a.Videos)
		}
		return int(b.Videos[0].SizeBytes - a.Videos[0].SizeBytes)
	})
	if len(groups) > limit {
		groups = groups[:limit]
	}
	return groups
}
