package library

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// StoredGroupLimit is how many groups a stored report keeps, more than one screen shows so a single library's share of it is not cut short.
const StoredGroupLimit = 1000

// SimilarityLevels are the levels the screen offers, each stored by the report job.
var SimilarityLevels = []SimilarityLevel{SimilarityExact, SimilarityHigh, SimilarityMedium, SimilarityLow, SimilarityLowest}

// SimilarReport is groups found at one time, shown with the age they have.
type SimilarReport struct {
	// ComputedAt is Unix seconds; zero when the report has not been computed yet.
	ComputedAt int64
	Groups     []SimilarGroup
}

// storedVideo is what the comparison found of a video; its facts are read again when the report is shown.
type storedVideo struct {
	ID, SizeBytes, DurationMs, Width, Height, RootFolderID int64
	RelPath                                                string
	PathCount                                              int
	Mean                                                   uint64
}

type storedGroup struct {
	MaxDistance int
	Videos      []storedVideo
}

// Refresh compares every hashed video on the roots at level and stores the groups under it, replacing the last report.
func (s SimilarVideos) Refresh(ctx context.Context, rootFolderIDs []int64, level SimilarityLevel, now int64) error {
	groups, err := s.compare(ctx, rootFolderIDs, level, StoredGroupLimit)
	if err != nil {
		return err
	}
	stored := make([]storedGroup, len(groups))
	for index, group := range groups {
		stored[index].MaxDistance = group.MaxDistance
		for _, video := range group.Videos {
			stored[index].Videos = append(stored[index].Videos, storedVideo{
				ID: video.MediaObjectID, SizeBytes: video.SizeBytes, DurationMs: video.DurationMs, Width: video.Width, Height: video.Height,
				RootFolderID: video.RootFolderID, RelPath: video.RelPath, PathCount: video.PathCount, Mean: video.Mean,
			})
		}
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("library: encode similar report: %w", err)
	}
	err = s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).UpsertSimilarReport(ctx, librarydb.UpsertSimilarReportParams{Level: string(level), ComputedAt: now, GroupsJson: string(encoded)})
	})
	if err != nil {
		return fmt.Errorf("library: store similar report: %w", err)
	}
	return nil
}

// Stored answers the report kept for level, limited to videos on the roots that still have a live path, with their facts read now.
// A group left with one video is dropped.
func (s SimilarVideos) Stored(ctx context.Context, rootFolderIDs []int64, level SimilarityLevel, limit int) (SimilarReport, error) {
	queries := librarydb.New(s.store.Read())
	row, err := queries.GetSimilarReport(ctx, string(level))
	if errors.Is(err, sql.ErrNoRows) {
		return SimilarReport{}, nil
	}
	if err != nil {
		return SimilarReport{}, fmt.Errorf("library: read similar report: %w", err)
	}
	var stored []storedGroup
	if err := json.Unmarshal([]byte(row.GroupsJson), &stored); err != nil {
		return SimilarReport{}, fmt.Errorf("library: decode similar report: %w", err)
	}
	live, err := s.liveVideos(ctx, stored)
	if err != nil {
		return SimilarReport{}, err
	}
	inRoots := make(map[int64]bool, len(rootFolderIDs))
	for _, id := range rootFolderIDs {
		inRoots[id] = true
	}
	var groups []SimilarGroup
	for _, group := range stored {
		shown := SimilarGroup{MaxDistance: group.MaxDistance}
		for _, video := range group.Videos {
			if !inRoots[video.RootFolderID] || !live[video.ID] {
				continue
			}
			shown.Videos = append(shown.Videos, SimilarVideo{
				MediaObjectID: video.ID, SizeBytes: video.SizeBytes, DurationMs: video.DurationMs, Width: video.Width, Height: video.Height,
				RootFolderID: video.RootFolderID, RelPath: video.RelPath, PathCount: video.PathCount, Mean: video.Mean,
			})
		}
		if len(shown.Videos) >= 2 {
			groups = append(groups, shown)
		}
	}
	slices.SortStableFunc(groups, func(first, second SimilarGroup) int { return cmp.Compare(len(second.Videos), len(first.Videos)) })
	if len(groups) > limit {
		groups = groups[:limit]
	}
	if err := s.addFacts(ctx, groups); err != nil {
		return SimilarReport{}, err
	}
	return SimilarReport{ComputedAt: row.ComputedAt, Groups: groups}, nil
}

// liveVideos is the stored videos that still have a live path, asked a chunk of ids at a time.
func (s SimilarVideos) liveVideos(ctx context.Context, stored []storedGroup) (map[int64]bool, error) {
	var ids []int64
	for _, group := range stored {
		for _, video := range group.Videos {
			ids = append(ids, video.ID)
		}
	}
	live := make(map[int64]bool, len(ids))
	queries := librarydb.New(s.store.Read())
	for start := 0; start < len(ids); start += factsChunk {
		encoded, err := json.Marshal(ids[start:min(start+factsChunk, len(ids))])
		if err != nil {
			return nil, fmt.Errorf("library: encode video ids: %w", err)
		}
		found, err := queries.ListVideoIDsWithLivePath(ctx, string(encoded))
		if err != nil {
			return nil, fmt.Errorf("library: live videos: %w", err)
		}
		for _, id := range found {
			live[id] = true
		}
	}
	return live, nil
}
