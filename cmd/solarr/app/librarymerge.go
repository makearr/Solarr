package app

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
)

// libraryMerger merges the content of one library into another and cancels the jobs still waiting under the old one.
type libraryMerger struct {
	merges library.CategoryMerges
	queue  *jobs.Queue
}

func (m libraryMerger) Merge(ctx context.Context, from, into int64) (int64, error) {
	moved, err := m.merges.Merge(ctx, from, into)
	if err != nil {
		return moved, err
	}
	if _, err := m.queue.CancelWaitingForCategory(ctx, from, fmt.Sprintf("library %d was merged into library %d", from, into)); err != nil {
		return moved, err
	}
	return moved, nil
}
