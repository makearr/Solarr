package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/music"
)

// worksMonitorAdapter satisfies apiserver.WorksMonitor over library.Desired,
// resolving category/profile the same way music.SubscriptionExpandJob
// already does (CategoryFinder.CategoryForMediaType) rather than needing a
// category the caller has no natural way to supply -- a work-detail page has
// a work, not a root folder, to derive one from.
type worksMonitorAdapter struct {
	works            library.Works
	desired          library.Desired
	categories       music.CategoryFinder
	defaultProfileID int64
	logger           *slog.Logger
}

// SetWorkMonitored always ensures the album item exists, monitored or not:
// an explicit "not interested" from a user is worth recording the same way
// as a category's monitor_discovered policy would (Invariant 71 -- once it
// exists, a later scan or subscription pass leaves it alone either way).
func (a worksMonitorAdapter) SetWorkMonitored(
	ctx context.Context, workID int64, monitored bool,
) (apiserver.WantedRow, error) {
	detail, err := a.works.Detail(ctx, workID)
	if err != nil {
		return apiserver.WantedRow{}, err
	}

	category, ok := a.categories.CategoryForMediaType(detail.Work.MediaType)
	if !ok {
		return apiserver.WantedRow{}, fmt.Errorf("no category configured for %s", detail.Work.MediaType)
	}
	profileID := a.defaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}

	item, err := a.desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workID, CategoryID: category.ID, ProfileID: profileID, Monitored: monitored,
	})
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	if (item.Monitored == 1) != monitored {
		if err := a.desired.SetItemMonitored(ctx, item.ID, monitored); err != nil {
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

func (a worksMonitorAdapter) SetPartMonitored(ctx context.Context, workID, partID int64, monitored bool) error {
	return a.desired.SetPartMonitored(ctx, workID, partID, monitored)
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
func (a worksMonitorAdapter) BulkSetMonitored(
	ctx context.Context, rootFolderIDs, ids, excludedIDs []int64, monitored, preview bool,
) (int64, error) {
	matched, err := a.matchingWorkIDs(ctx, rootFolderIDs, ids, excludedIDs)
	if err != nil {
		return 0, err
	}
	if preview {
		return int64(len(matched)), nil
	}

	var affected int64
	for _, workID := range matched {
		if _, err := a.SetWorkMonitored(ctx, workID, monitored); err != nil {
			a.logger.Warn("bulk monitor: one work failed, continuing",
				slog.Int64("work_id", workID), slog.Any("error", err))
			continue
		}
		affected++
	}
	return affected, nil
}

// matchingWorkIDs turns a selection into concrete ids. Explicit ids (an
// "include" selection) are filtered to ones visible under rootFolderIDs and
// not excluded; otherwise every visible work not excluded is enumerated
// (an "all matching" selection), paged so no single read pulls the whole
// library into memory at once.
func (a worksMonitorAdapter) matchingWorkIDs(
	ctx context.Context, rootFolderIDs, ids, excludedIDs []int64,
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
			detail, err := a.works.Detail(ctx, id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if rootFolderVisible(rootFolderIDs, detail.RootFolderIDs) {
				matched = append(matched, id)
			}
		}
		return matched, nil
	}

	var matched []int64
	cursor := library.WorkCursor{}
	for {
		rows, err := a.works.List(ctx, library.WorkFilter{RootFolderIDs: rootFolderIDs}, cursor, bulkPageSize)
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

// rootFolderVisible reports whether any of a work's own root folders is
// among the caller's visible ones -- ADR-27, the same check
// apiserver.sharesAnID makes, duplicated here rather than exported since
// apiserver's helpers are unexported by design (this package translates,
// it does not reach into apiserver's internals).
func rootFolderVisible(visible, workRootFolderIDs []int64) bool {
	seen := make(map[int64]struct{}, len(visible))
	for _, id := range visible {
		seen[id] = struct{}{}
	}
	for _, id := range workRootFolderIDs {
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
