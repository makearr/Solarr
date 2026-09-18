package adapters

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// healthSampleSize bounds every drill-down list LibraryHealth.Report
// carries -- a compact summary, not a second paginated browser for each
// figure (todo 4.8: "every figure a link", not a full list of its own).
const healthSampleSize = 20

// LibraryHealth satisfies apiserver.LibraryHealth over library.db's own
// readers. Root folder capacity is not composed here: the handler already
// holds settings.Cache directly and reads it itself, the same shape
// GetNetworkSettings already uses.
type LibraryHealth struct {
	Physical       library.Physical
	Unidentified   library.Unidentified
	Desired        library.Desired
	BlockedContent library.BlockedContentStore
	Counters       library.Counters
}

func (a LibraryHealth) Report(ctx context.Context) (apiserver.LibraryHealthReport, error) {
	counts, err := a.Counters.Counters(ctx, "library")
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: counters: %w", err)
	}
	unprobedCount := int(counts["total_files"] - counts["probed"])

	unprobed, err := a.Physical.ListUnmeasured(ctx, 0, healthSampleSize)
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: unprobed sample: %w", err)
	}
	unprobedFiles := make([]apiserver.UnprobedFile, len(unprobed))
	for i, f := range unprobed {
		unprobedFiles[i] = apiserver.UnprobedFile{RootFolderID: f.RootFolderID, RelPath: f.RelPath}
	}

	unmatchedCount, err := a.Unidentified.Count(ctx)
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: unmatched count: %w", err)
	}

	incompleteCount, err := a.Desired.CountIncomplete(ctx, 0)
	if err != nil {
		return apiserver.LibraryHealthReport{}, fmt.Errorf("library health: incomplete count: %w", err)
	}
	incomplete, err := a.Desired.IncompleteItems(ctx, 0, 0, healthSampleSize)
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
