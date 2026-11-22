package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// SimilarVideos satisfies apiserver.SimilarVideoReader.
type SimilarVideos struct {
	Store library.SimilarVideos
	// Queue asks for the comparison job of every level.
	Queue func(ctx context.Context) error
}

func (s SimilarVideos) RefreshSimilarVideos(ctx context.Context) error { return s.Queue(ctx) }

func (s SimilarVideos) SimilarVideos(ctx context.Context, rootFolderIDs []int64, level string, limit int) (apiserver.SimilarVideoReportRow, error) {
	report, err := s.Store.Stored(ctx, rootFolderIDs, library.SimilarityLevel(level), limit)
	if err != nil {
		return apiserver.SimilarVideoReportRow{}, err
	}
	rows := make([]apiserver.SimilarVideoGroupRow, len(report.Groups))
	for i, group := range report.Groups {
		rows[i].MaxDistance = group.MaxDistance
		for _, video := range group.Videos {
			rows[i].Videos = append(rows[i].Videos, apiserver.SimilarVideoRow{
				MediaObjectID: video.MediaObjectID, SizeBytes: video.SizeBytes, DurationMs: video.DurationMs, Width: video.Width, Height: video.Height, RootFolderID: video.RootFolderID, RelPath: video.RelPath, PathCount: video.PathCount,
				Mean: video.Mean, File: partFileRow(&video.File), WorkID: video.WorkID, WorkTitle: video.WorkTitle,
			})
		}
	}
	return apiserver.SimilarVideoReportRow{ComputedAt: report.ComputedAt, Groups: rows}, nil
}
