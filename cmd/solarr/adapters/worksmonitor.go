package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/music"
)

// WorksMonitor satisfies apiserver.WorksMonitor over library.Desired,
// resolving category/profile the same way music.SubscriptionExpandJob
// already does (CategoryFinder.CategoryForMediaType) rather than needing a
// category the caller has no natural way to supply -- a work-detail page has
// a work, not a root folder, to derive one from.
type WorksMonitor struct {
	Works    library.Works
	Identity library.Identity
	Desired  library.Desired
	// Remover quarantines the files of an album being deleted with them.
	Remover          FileRemover
	Categories       music.CategoryFinder
	DefaultProfileID int64
	Logger           *slog.Logger
}

// FileRemover puts one file into quarantine. Narrow on purpose: deleting an
// album's files is quarantine and nothing else -- no blocking, no blocklist
// (section 10.8, ADR-53's steps are separate choices).
type FileRemover interface {
	QuarantineFile(ctx context.Context, mediaObjectID int64) error
}

// SetWorkMonitored always ensures the album item exists, monitored or not:
// an explicit "not interested" from a user is worth recording the same way
// as a category's monitor_discovered policy would (Invariant 71 -- once it
// exists, a later scan or subscription pass leaves it alone either way).
func (a WorksMonitor) SetWorkMonitored(
	ctx context.Context, workID int64, monitored bool,
) (apiserver.WantedRow, error) {
	detail, err := a.Works.Detail(ctx, workID)
	if err != nil {
		return apiserver.WantedRow{}, err
	}

	category, ok := a.Categories.CategoryForMediaType(detail.Work.MediaType)
	if !ok {
		return apiserver.WantedRow{}, fmt.Errorf("no category configured for %s", detail.Work.MediaType)
	}
	profileID := a.DefaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}

	item, err := a.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workID, CategoryID: category.ID, ProfileID: profileID, Monitored: monitored,
	})
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	if (item.Monitored == 1) != monitored {
		if err := a.Desired.SetItemMonitored(ctx, item.ID, monitored); err != nil {
			return apiserver.WantedRow{}, err
		}
		item.Monitored = boolToInt64(monitored)
	}

	row := apiserver.WantedRow{ID: item.ID, Monitored: monitored, PresentCount: item.PresentCount, State: item.State}
	if item.RequiredCount.Valid {
		row.RequiredCount = &item.RequiredCount.Int64
	}
	return row, nil
}

func (a WorksMonitor) SetPartMonitored(ctx context.Context, workID, partID int64, monitored bool) error {
	return a.Desired.SetPartMonitored(ctx, workID, partID, monitored)
}

// SetTrackWanted ensures the track's own SINGLE item exists, wanted or not,
// mirroring SetWorkMonitored's own shape one level down (Invariant 71 --
// an explicit "not interested" is worth recording the same as wanted).
// Distinct from SetPartMonitored: that only resizes the album's own
// required_count, this creates a standalone acquisition target for the
// track by itself.
func (a WorksMonitor) SetTrackWanted(
	ctx context.Context, workID, partID int64, wanted bool,
) (apiserver.WantedRow, error) {
	detail, err := a.Works.Detail(ctx, workID)
	if err != nil {
		return apiserver.WantedRow{}, err
	}

	category, ok := a.Categories.CategoryForMediaType(detail.Work.MediaType)
	if !ok {
		return apiserver.WantedRow{}, fmt.Errorf("no category configured for %s", detail.Work.MediaType)
	}
	profileID := a.DefaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}

	item, err := a.Desired.EnsureTrackItemByPart(ctx, workID, partID, category.ID, profileID, nil)
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	if (item.Monitored == 1) != wanted {
		if err := a.Desired.SetItemMonitored(ctx, item.ID, wanted); err != nil {
			return apiserver.WantedRow{}, err
		}
		item.Monitored = boolToInt64(wanted)
	}

	row := apiserver.WantedRow{ID: item.ID, Monitored: wanted, PresentCount: item.PresentCount, State: item.State}
	if item.RequiredCount.Valid {
		row.RequiredCount = &item.RequiredCount.Int64
	}
	return row, nil
}

// bulkPageSize bounds one library.Works.List call while enumerating a
// selection's matching ids -- ADR-3's "bulk work chunks to 200 rows",
// applied to a read pass here rather than a write transaction.
const bulkPageSize = 200

// BulkSetMonitored resolves the selection to concrete work ids (explicit
// ids filtered to what's visible, or every visible work not excluded) and,
// unless preview, calls SetWorkMonitored for each -- reusing the exact same
// per-work logic and its Invariant 71 guarantee rather than a bulk SQL
// statement that would have to reimplement it. One work failing (a media
// type with no category configured, a since-deleted work) is logged and
// skipped rather than aborting the rest of the selection.
func (a WorksMonitor) BulkSetMonitored(
	ctx context.Context, rootFolderIDs, categoryIDs, ids, excludedIDs []int64, monitored, preview bool,
) (int64, error) {
	matched, err := a.matchingWorkIDs(ctx, rootFolderIDs, categoryIDs, ids, excludedIDs)
	if err != nil {
		return 0, err
	}
	if preview {
		return int64(len(matched)), nil
	}

	var affected int64
	for _, workID := range matched {
		if _, err := a.SetWorkMonitored(ctx, workID, monitored); err != nil {
			a.Logger.Warn("bulk monitor: one work failed, continuing",
				slog.Int64("work_id", workID), slog.Any("error", err))
			continue
		}
		affected++
	}
	return affected, nil
}

// matchingWorkIDs turns a selection into concrete ids. Explicit ids (an
// "include" selection) are filtered to ones visible under rootFolderIDs or
// categoryIDs (ADR-27's two halves) and not excluded; otherwise every visible
// work not excluded is enumerated (an "all matching" selection), paged so no
// single read pulls the whole library into memory at once.
func (a WorksMonitor) matchingWorkIDs(
	ctx context.Context, rootFolderIDs, categoryIDs, ids, excludedIDs []int64,
) ([]int64, error) {
	excluded := make(map[int64]bool, len(excludedIDs))
	for _, id := range excludedIDs {
		excluded[id] = true
	}

	if len(ids) > 0 {
		matched := make([]int64, 0, len(ids))
		for _, id := range ids {
			if excluded[id] {
				continue
			}
			detail, err := a.Works.Detail(ctx, id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if idVisible(rootFolderIDs, detail.RootFolderIDs) || idVisible(categoryIDs, detail.CategoryIDs) {
				matched = append(matched, id)
			}
		}
		return matched, nil
	}

	var matched []int64
	cursor := library.WorkCursor{}
	for {
		rows, err := a.Works.List(ctx,
			library.WorkFilter{RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs}, cursor, bulkPageSize)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if !excluded[row.ID] {
				matched = append(matched, row.ID)
			}
		}
		if len(rows) < bulkPageSize {
			return matched, nil
		}
		last := rows[len(rows)-1]
		cursor = library.WorkCursor{SortTitle: last.SortTitle, ID: last.ID}
	}
}

// idVisible reports whether any of a work's own ids (root folder or category)
// is among the caller's visible ones -- ADR-27, the same check
// apiserver.sharesAnID makes, duplicated here rather than exported since
// apiserver's helpers are unexported by design (this package translates,
// it does not reach into apiserver's internals).
func idVisible(visible, workIDs []int64) bool {
	seen := make(map[int64]struct{}, len(visible))
	for _, id := range visible {
		seen[id] = struct{}{}
	}
	for _, id := range workIDs {
		if _, ok := seen[id]; ok {
			return true
		}
	}
	return false
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// RemoveWork stops wanting an album. The work is looked up first so a missing
// id answers sql.ErrNoRows rather than silently succeeding on nothing.
func (a WorksMonitor) RemoveWork(
	ctx context.Context, workID int64, deleteFiles bool,
) (apiserver.RemoveWorkResult, error) {
	if _, err := a.Works.Detail(ctx, workID); err != nil {
		return apiserver.RemoveWorkResult{}, err
	}

	var quarantined int64
	if deleteFiles {
		files, err := a.Identity.WorkFiles(ctx, workID)
		if err != nil {
			return apiserver.RemoveWorkResult{}, err
		}
		// Quarantine, never delete: section 10.8 -- the bytes stay
		// recoverable for the retention window, and a mistaken delete of a
		// whole album is exactly what that window is for.
		for _, mediaObjectID := range files {
			if err := a.Remover.QuarantineFile(ctx, mediaObjectID); err != nil {
				return apiserver.RemoveWorkResult{}, err
			}
			quarantined++
		}
	}

	result, err := a.Identity.RemoveWork(ctx, workID)
	if err != nil {
		return apiserver.RemoveWorkResult{}, err
	}
	return apiserver.RemoveWorkResult{
		WorkDeleted: result.WorkDeleted, FilesKept: result.FilesKept,
		FilesQuarantined: quarantined,
	}, nil
}

// RemovePart stops wanting one track: the file goes to quarantine first, so the
// album is recomputed without it, then the part is unmonitored and unwanted.
func (a WorksMonitor) RemovePart(
	ctx context.Context, workID, partID int64, deleteFiles bool,
) (apiserver.RemovePartResult, error) {
	detail, err := a.Works.Detail(ctx, workID)
	if err != nil {
		return apiserver.RemovePartResult{}, err
	}
	var part *library.PartDetail
	for i := range detail.Parts {
		if detail.Parts[i].Part.ID == partID {
			part = &detail.Parts[i]
			break
		}
	}
	if part == nil {
		return apiserver.RemovePartResult{}, fmt.Errorf("part %d of work %d: %w", partID, workID, sql.ErrNoRows)
	}

	var result apiserver.RemovePartResult
	if deleteFiles && part.File != nil {
		if err := a.Remover.QuarantineFile(ctx, part.File.MediaObjectID); err != nil {
			return apiserver.RemovePartResult{}, err
		}
		result.FilesQuarantined = 1
	}
	if err := a.Desired.SetPartMonitored(ctx, workID, partID, false); err != nil {
		return apiserver.RemovePartResult{}, err
	}
	if part.Wanted != nil && part.Wanted.Monitored {
		if err := a.Desired.SetItemMonitored(ctx, part.Wanted.ID, false); err != nil {
			return apiserver.RemovePartResult{}, err
		}
	}
	return result, nil
}
