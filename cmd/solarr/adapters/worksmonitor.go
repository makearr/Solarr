package adapters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// WorksMonitor satisfies apiserver.WorksMonitor over library.Desired,
// resolving category/profile the same way music.SubscriptionExpandJob
// already does (CategoryFinder.CategoryForMediaType) rather than needing a
// category the caller has no natural way to supply -- a work-detail page has
// a work, not a root folder, to derive one from.
type WorksMonitor struct {
	// RatingLimit answers a library's maximum rating, for a selection made under Above rating; nil filters nothing.
	RatingLimit func(categoryID int64) (library.RatingLimit, bool)
	Works       library.Works
	Identity    library.Identity
	Desired     library.Desired
	// Remover recycles the files of an album being deleted with them.
	Remover    FileRemover
	Categories music.CategoryFinder
	// Libraries gives a removal its library's recycling bin.
	Libraries        LibraryLookup
	DefaultProfileID int64
	// Monitoring answers a media type's monitor options; nil has none.
	Monitoring func(mediaType string) (media.Monitoring, bool)
	// GroupLabel names a group of a media type's work; nil names it by its ref.
	GroupLabel func(mediaType, ref string) string
	// Exclusions remember a work removed with "do not add it back".
	Exclusions library.Exclusions
	// ScanHonoursExclusions says a media type's scan leaves an excluded work out; nil says none does.
	ScanHonoursExclusions func(mediaType string) bool
	// Clock resolves the Fresh filter of a bulk selection.
	Clock  clock.Clock
	Fresh  FreshWindow
	Logger *slog.Logger
}

// FileRemover puts one file into the recycle bin. Narrow on purpose: deleting an
// album's files is recycle bin and nothing else -- no blocking, no blocklist
// (section 10.8, ADR-53's steps are separate choices).
type FileRemover interface {
	RecycleFile(ctx context.Context, mediaObjectID int64, recycling library.Recycling) error
}

// MonitorWork monitors or unmonitors what the scope names, then answers the work's groups.
func (a WorksMonitor) MonitorWork(ctx context.Context, workID int64, scope apiserver.MonitorScope) ([]apiserver.GroupRow, error) {
	detail, err := a.Works.Detail(ctx, workID)
	if err != nil {
		return nil, err
	}
	switch {
	case scope.Option != "":
		err = a.applyOption(ctx, detail, scope)
	case len(scope.Parts) > 0:
		for _, partID := range scope.Parts {
			if err = a.Desired.SetPartMonitored(ctx, workID, partID, scope.Monitored); err != nil {
				break
			}
		}
	case scope.Group != nil:
		err = a.monitorGroup(ctx, detail, *scope.Group, scope.Monitored)
	case len(detail.Groups) == 0:
		_, err = a.ensureWorkItem(ctx, detail, scope.Monitored)
	default:
		for _, group := range detail.Groups {
			if err = a.monitorGroup(ctx, detail, group.Ref, scope.Monitored); err != nil {
				break
			}
		}
	}
	if errors.Is(err, library.ErrPartNotFound) {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	if err != nil {
		return nil, err
	}
	after, err := a.Works.Detail(ctx, workID)
	if err != nil {
		return nil, err
	}
	groups := make([]apiserver.GroupRow, len(after.Groups))
	for i, group := range after.Groups {
		label := group.Ref
		if a.GroupLabel != nil {
			label = a.GroupLabel(after.Work.MediaType, group.Ref)
		}
		groups[i] = apiserver.GroupRow{Ref: group.Ref, Label: label, Wanted: apiserver.WantedRow{
			ID: group.ID, Monitored: group.Monitored, RequiredCount: group.RequiredCount, PresentCount: group.PresentCount,
			State: group.State, LastSearchedAt: group.LastSearchedAt,
		}}
	}
	return groups, nil
}

// applyOption applies one of the type's monitor options in the work's library.
func (a WorksMonitor) applyOption(ctx context.Context, detail library.WorkDetail, scope apiserver.MonitorScope) error {
	var monitoring media.Monitoring
	ok := false
	if a.Monitoring != nil {
		monitoring, ok = a.Monitoring(detail.Work.MediaType)
	}
	if !ok {
		return fmt.Errorf("%w: a %s has no monitor options", apiserver.ErrInvalidChoice, detail.Work.MediaType)
	}
	if len(detail.CategoryIDs) == 0 {
		return fmt.Errorf("%w: the work is in no library", apiserver.ErrInvalidChoice)
	}
	err := monitoring.ApplyMonitorOption(ctx, detail.CategoryIDs[0], detail.Work.ID, scope.Option, scope.Special)
	if errors.Is(err, media.ErrUnknownMonitorOption) {
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	return err
}

// monitorGroup sets one group: a work's single group by its item alone, a group with a ref with the parts under it.
func (a WorksMonitor) monitorGroup(ctx context.Context, detail library.WorkDetail, ref string, monitored bool) error {
	if ref == "" {
		_, err := a.ensureWorkItem(ctx, detail, monitored)
		return err
	}
	found, err := a.Desired.SetSeasonMonitored(ctx, detail.Work.ID, ref, monitored)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: the work has no group %q", apiserver.ErrInvalidChoice, ref)
	}
	return nil
}

// workCategory is the work's own library, or the first of its media type for a work in none yet.
func (a WorksMonitor) workCategory(detail library.WorkDetail) (settings.Category, bool) {
	if len(detail.CategoryIDs) > 0 {
		if category, ok := a.Categories.Category(detail.CategoryIDs[0]); ok {
			return category, true
		}
	}
	return a.Categories.CategoryForMediaType(detail.Work.MediaType)
}

// ensureWorkItem always ensures the work's single SET item exists in its library, monitored or not: an explicit "not
// interested" is recorded the same way a category's monitor_discovered policy would be (Invariant 71).
func (a WorksMonitor) ensureWorkItem(ctx context.Context, detail library.WorkDetail, monitored bool) (apiserver.WantedRow, error) {
	category, ok := a.workCategory(detail)
	if !ok {
		return apiserver.WantedRow{}, fmt.Errorf("no category configured for %s", detail.Work.MediaType)
	}
	profileID := a.DefaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	item, err := a.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: detail.Work.ID, CategoryID: category.ID, ProfileID: profileID, Monitored: monitored,
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

	category, ok := a.workCategory(detail)
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
	ctx context.Context, selection apiserver.BulkSelection, monitored, preview bool,
) (int64, error) {
	matched, err := a.matchingWorkIDs(ctx, selection)
	if err != nil {
		return 0, err
	}
	if preview {
		return int64(len(matched)), nil
	}

	var affected int64
	for _, workID := range matched {
		if _, err := a.MonitorWork(ctx, workID, apiserver.MonitorScope{Monitored: monitored}); err != nil {
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
func (a WorksMonitor) matchingWorkIDs(ctx context.Context, selection apiserver.BulkSelection) ([]int64, error) {
	rootFolderIDs, categoryIDs := selection.RootFolderIDs, selection.CategoryIDs
	excluded := make(map[int64]bool, len(selection.ExcludedIDs))
	for _, id := range selection.ExcludedIDs {
		excluded[id] = true
	}

	if len(selection.IDs) > 0 {
		matched := make([]int64, 0, len(selection.IDs))
		for _, id := range selection.IDs {
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

	where, err := listfilter.Parse(selection.Where, library.WorkFields)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	filter := library.WorkFilter{
		RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs, WantedOnly: selection.Wanted, CutoffUnmetOnly: selection.CutoffUnmet,
		Where: where, TagIDs: selection.TagIDs, ExcludedTagIDs: selection.ExcludedTagIDs,
		AboveRating: aboveRating(selection.AboveRating, categoryIDs, a.RatingLimit), MediaType: selection.MediaType,
	}
	if selection.Fresh {
		filter.FreshSince, filter.FreshRSSSince = a.Fresh.Cutoffs(a.Clock)
	}
	var matched []int64
	cursor := library.WorkCursor{}
	for {
		rows, err := a.Works.List(ctx, filter, cursor, bulkPageSize)
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

// RemoveWork stops wanting a work and, with exclude, keeps follows, import lists and a type's scan that honours it from
// adding it back. A missing id answers sql.ErrNoRows rather than silently succeeding on nothing.
func (a WorksMonitor) RemoveWork(
	ctx context.Context, workID int64, deleteFiles, exclude bool,
) (apiserver.RemoveWorkResult, error) {
	detail, err := a.Works.Detail(ctx, workID)
	if err != nil {
		return apiserver.RemoveWorkResult{}, err
	}

	var recycled int64
	if deleteFiles {
		files, err := a.Identity.WorkFiles(ctx, workID)
		if err != nil {
			return apiserver.RemoveWorkResult{}, err
		}
		// Recycle bin, never delete: section 10.8 -- the bytes stay
		// recoverable for the retention window, and a mistaken delete of a
		// whole album is exactly what that window is for.
		recycling := RecyclingFor(a.Libraries, detail.CategoryIDs)
		for _, mediaObjectID := range files {
			if err := a.Remover.RecycleFile(ctx, mediaObjectID, recycling); err != nil {
				return apiserver.RemoveWorkResult{}, err
			}
			recycled++
		}
	}

	withFiles := false
	if exclude {
		ids := make([]library.ExternalIDWrite, len(detail.ExternalIDs))
		for i, external := range detail.ExternalIDs {
			ids[i] = library.ExternalIDWrite{Provider: external.Provider, ExternalID: external.ExternalID}
		}
		if err := a.Exclusions.Exclude(ctx, a.Clock.Now(), detail.Work.MediaType, detail.Work.CanonicalTitle, ids); err != nil {
			return apiserver.RemoveWorkResult{}, err
		}
		withFiles = a.ScanHonoursExclusions != nil && a.ScanHonoursExclusions(detail.Work.MediaType)
	}
	result, err := a.Identity.RemoveWork(ctx, workID, withFiles)
	if err != nil {
		return apiserver.RemoveWorkResult{}, err
	}
	return apiserver.RemoveWorkResult{
		WorkDeleted: result.WorkDeleted, FilesKept: result.FilesKept,
		FilesRecycled: recycled,
	}, nil
}

// RemovePart stops wanting one track: the file goes to the recycle bin first, so the
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
		recycling := RecyclingFor(a.Libraries, detail.CategoryIDs)
		if err := a.Remover.RecycleFile(ctx, part.File.MediaObjectID, recycling); err != nil {
			return apiserver.RemovePartResult{}, err
		}
		result.FilesRecycled = 1
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
