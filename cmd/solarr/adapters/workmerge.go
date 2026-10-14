package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/media"
)

// WorkMerger implements apiserver.WorkMerger: library.db in one transaction, then the history in statistics.db and
// the media type's own placing of the files (no join across the two databases, section 4.3).
type WorkMerger struct {
	Identity library.Identity
	History  statistics.History
	Registry *media.Registry
	Logger   *slog.Logger
}

// Suggestion implements apiserver.WorkMerger.
func (m WorkMerger) Suggestion(ctx context.Context, id int64) (gen.WorkMergeSuggestion, bool, error) {
	suggestion, found, err := m.Identity.MergeSuggestionFor(ctx, id)
	if err != nil || !found {
		return gen.WorkMergeSuggestion{}, false, err
	}
	answer := gen.WorkMergeSuggestion{IntoWorkId: suggestion.IntoWorkID, Title: suggestion.Title}
	if suggestion.LeadName != "" {
		answer.LeadName = &suggestion.LeadName
	}
	return answer, true, nil
}

// Merge implements apiserver.WorkMerger. What follows the library's own transaction is best effort: the merge has
// happened, so a failure there is logged, never undone.
func (m WorkMerger) Merge(ctx context.Context, dropID, keepID int64, preview bool) (gen.WorkMerge, error) {
	var merged library.WorkMerge
	var err error
	if preview {
		merged, err = m.Identity.PreviewWorkMerge(ctx, dropID, keepID)
	} else {
		merged, err = m.Identity.MergeWork(ctx, dropID, keepID)
	}
	if errors.Is(err, library.ErrMergeRefused) {
		return gen.WorkMerge{}, fmt.Errorf("%w: %w", apiserver.ErrMergeRefused, err)
	}
	if err != nil {
		return gen.WorkMerge{}, err
	}
	if !preview {
		if err := m.History.MoveWork(ctx, dropID, keepID); err != nil {
			m.Logger.Warn("merged work's history not moved", slog.Int64("work_id", dropID), slog.Any("error", err))
		}
		m.afterMerge(ctx, keepID)
	}
	return gen.WorkMerge{
		DropId: merged.DropID, KeepId: merged.KeepID, DropTitle: merged.DropTitle, KeepTitle: merged.KeepTitle,
		Files: merged.Files, WantedItems: merged.WantedItems, Acquisitions: merged.Acquisitions, ImportUnits: merged.ImportUnits,
	}, nil
}

func (m WorkMerger) afterMerge(ctx context.Context, keepID int64) {
	mediaType, err := m.Identity.WorkMediaType(ctx, keepID)
	if err != nil {
		m.Logger.Warn("merged work's files not placed", slog.Int64("work_id", keepID), slog.Any("error", err))
		return
	}
	module, ok := media.Capability[media.Merging](m.Registry, media.Key(mediaType))
	if !ok {
		return
	}
	if err := module.AfterMerge(ctx, keepID); err != nil {
		m.Logger.Warn("merged work's files not placed", slog.Int64("work_id", keepID), slog.Any("error", err))
	}
}
