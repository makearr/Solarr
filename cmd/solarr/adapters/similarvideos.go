package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// SimilarVideos satisfies apiserver.SimilarVideoReader.
type SimilarVideos struct{ Store library.SimilarVideos }

func (s SimilarVideos) SimilarVideos(ctx context.Context, rootFolderIDs []int64, level string, limit int) ([]apiserver.SimilarVideoGroupRow, error) {
	groups, err := s.Store.Groups(ctx, rootFolderIDs, library.SimilarityLevel(level), limit)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.SimilarVideoGroupRow, len(groups))
	for i, group := range groups {
		rows[i].MaxDistance = group.MaxDistance
		for _, video := range group.Videos {
			rows[i].Videos = append(rows[i].Videos, apiserver.SimilarVideoRow{
				MediaObjectID: video.MediaObjectID, SizeBytes: video.SizeBytes, DurationMs: video.DurationMs, Width: video.Width, Height: video.Height, RootFolderID: video.RootFolderID, RelPath: video.RelPath, PathCount: video.PathCount,
				Mean: video.Mean, File: partFileRow(&video.File), WorkID: video.WorkID, WorkTitle: video.WorkTitle,
			})
		}
	}
	return rows, nil
}
