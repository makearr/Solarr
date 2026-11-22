package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// healthSampleSize bounds every drill-down list LibraryHealth.Report
// carries -- a compact summary, not a second paginated browser for each
// figure (todo 4.8: "every figure a link", not a full list of its own).
const healthSampleSize = 20

// LibraryHealth satisfies apiserver.LibraryHealth over library.db readers; root folder capacity is read by the handler from settings.Cache, not composed here.
type LibraryHealth struct {
	Physical       library.Physical
	Unidentified   library.Unidentified
	Desired        library.Desired
	BlockedContent library.BlockedContentStore
	Counters       library.Counters
}

// unprobedSample lists files with no measured attributes, each with what ffprobe said when it last failed on it.
func (a LibraryHealth) unprobedSample(ctx context.Context) ([]apiserver.UnprobedFile, error) {
	unprobed, err := a.Physical.ListUnmeasured(ctx, 0, healthSampleSize, library.IncludeProbeFailures)
	if err != nil {
		return nil, fmt.Errorf("library health: unprobed sample: %w", err)
	}
	files := make([]apiserver.UnprobedFile, len(unprobed))
	for i, f := range unprobed {
		raw, err := a.Physical.ProbeFailureRaw(ctx, f.MediaObjectID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("library health: probe output: %w", err)
		}
		files[i] = apiserver.UnprobedFile{RootFolderID: f.RootFolderID, RelPath: f.RelPath, FFprobeOutput: raw}
	}
	return files, nil
}

func (a LibraryHealth) Report(ctx context.Context) (apiserver.LibraryHealthReport, error) {
	counts, err := a.Counters.Counters(ctx, "library")
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: counters: %w", err)
	}
	unprobedCount := int(counts["total_files"] - counts["probed"])

	unprobedFiles, err := a.unprobedSample(ctx)
	if err != nil {
		return apiserver.LibraryHealthReport{}, err
	}

	unmatchedCount, err := a.Unidentified.Count(ctx)
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: unmatched count: %w", err)
	}

	incompleteCount, err := a.Desired.CountIncomplete(ctx, 0)
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: incomplete count: %w", err)
	}
	incomplete, err := a.Desired.IncompleteItems(ctx, 0, 0, 0, healthSampleSize)
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: incomplete sample: %w", err)
	}
	incompleteWorks := make([]apiserver.IncompleteWork, len(incomplete))
	for i, item := range incomplete {
		incompleteWorks[i] = apiserver.IncompleteWork{
			WorkID: item.WorkID, Title: item.Title, CategoryID: item.CategoryID, State: item.State,
			RequiredCount: item.RequiredCount, PresentCount: item.PresentCount,
		}
	}

	blocked, err := a.BlockedContent.ListPresentInLibrary(ctx, 0, healthSampleSize)
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: blocked present: %w", err)
	}
	blockedPresent := make([]apiserver.BlockedPresent, len(blocked))
	for i, b := range blocked {
		blockedPresent[i] = apiserver.BlockedPresent{
			BlockedContentID: b.BlockedContentID, Reason: string(b.Reason), Title: b.Title,
			MediaObjectID: b.MediaObjectID, RootFolderID: b.RootFolderID, RelPath: b.RelPath,
		}
	}

	return apiserver.LibraryHealthReport{
		UnprobedCount: unprobedCount, UnprobedFiles: unprobedFiles,
		UnmatchedCount:  int(unmatchedCount),
		IncompleteCount: int(incompleteCount), IncompleteWorks: incompleteWorks,
		BlockedPresent: blockedPresent,
	}, nil
}
