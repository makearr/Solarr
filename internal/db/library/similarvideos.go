package library

import (
	"context"
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
	// MaxDistance is the widest gap between two neighbours that joined the group, in bits.
	MaxDistance int
	Videos      []SimilarVideo
}

// similarMaxBits is the Hamming distance at or under which two videos count as one cut.
const similarMaxBits = 8

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
	var videos []SimilarVideo
	var hashes []uint64
	for _, row := range rows {
		if !inRoots[row.RootFolderID] || !row.Phash.Valid {
			continue
		}
		videos = append(videos, SimilarVideo{MediaObjectID: row.ID, SizeBytes: row.SizeBytes, DurationMs: row.DurationMs.Int64, RootFolderID: row.RootFolderID, RelPath: row.RelPath})
		hashes = append(hashes, uint64(row.Phash.Int64))
	}
	return groupSimilar(videos, hashes, limit), nil
}

// groupSimilar joins videos whose hashes are within similarMaxBits into groups of two or more.
func groupSimilar(videos []SimilarVideo, hashes []uint64, limit int) []SimilarGroup {
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
		for index, hash := range hashes {
			key := hash >> low & mask
			buckets[key] = append(buckets[key], index)
		}
		for _, members := range buckets {
			for i, first := range members {
				for _, second := range members[i+1:] {
					distance := bits.OnesCount64(hashes[first] ^ hashes[second])
					if distance > similarMaxBits || !sameLength(videos[first].DurationMs, videos[second].DurationMs) {
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
		byRoot[find(index)] = append(byRoot[find(index)], video)
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
