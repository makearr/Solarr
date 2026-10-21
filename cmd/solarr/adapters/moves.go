package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/moving/drainjob"
	"github.com/makearr/Solarr/internal/moving/mover"
)

// Drain satisfies apiserver.DrainStarter over drainjob, mapping its sentinels to ones writeError knows (404, 409).
type Drain struct {
	Config drainjob.Config
	Cache  *settings.Cache
}

func (a Drain) StartDrain(ctx context.Context, rootFolderID int64, preview bool) (apiserver.DrainPlanResult, error) {
	result, err := drainjob.Start(ctx, a.Config, rootFolderID, preview)
	switch {
	case errors.Is(err, drainjob.ErrRootFolderNotFound):
		return apiserver.DrainPlanResult{}, settings.ErrNotFound
	case errors.Is(err, drainjob.ErrNoFiles):
		return apiserver.DrainPlanResult{}, apiserver.ErrNothingToDrain
	case err != nil:
		return apiserver.DrainPlanResult{}, err
	}
	return drainPlanResult(result), nil
}

// WorkMove satisfies apiserver.WorkMover over drainjob.StartWorkMove.
type WorkMove struct {
	Config drainjob.Config
	Files  drainjob.WorkFiles
	Works  library.Works
	Cache  *settings.Cache
}

func (a WorkMove) StartWorkMove(ctx context.Context, workID, rootFolderID int64, preview bool) (apiserver.DrainPlanResult, error) {
	mediaType, err := a.Works.MediaTypeOf(ctx, workID)
	if err != nil {
		return apiserver.DrainPlanResult{}, err
	}
	snapshot := a.Cache.Snapshot()
	inLibrary := false
	for _, attachment := range snapshot.CategoryRoots() {
		category, found := snapshot.Category(attachment.CategoryID)
		inLibrary = inLibrary || (attachment.RootFolderID == rootFolderID && found && category.MediaType == mediaType)
	}
	if !inLibrary {
		return apiserver.DrainPlanResult{}, apiserver.ErrRootNotInWorkLibrary
	}
	result, err := drainjob.StartWorkMove(ctx, a.Config, a.Files, workID, rootFolderID, preview)
	switch {
	case errors.Is(err, drainjob.ErrRootFolderNotFound):
		return apiserver.DrainPlanResult{}, settings.ErrNotFound
	case errors.Is(err, drainjob.ErrNothingToMove):
		return apiserver.DrainPlanResult{}, apiserver.ErrNothingToMove
	case err != nil:
		return apiserver.DrainPlanResult{}, err
	}
	return drainPlanResult(result), nil
}

// StopDrain clears drain_state and restores accepts_new -- the settings-only
// half of ADR-51's own "stop draining", not a cancellation of any move job
// already running (DELETE /moves/{id} is that, once it exists).
func (a Drain) StopDrain(ctx context.Context, rootFolderID int64) error {
	if _, found := a.Cache.Snapshot().RootFolder(rootFolderID); !found {
		return settings.ErrNotFound
	}
	if err := a.Cache.SetAcceptsNewForRootFolder(ctx, rootFolderID, true); err != nil {
		return err
	}
	return a.Cache.SetRootFolderDrainState(ctx, rootFolderID, settings.DrainStateNone, nil)
}

func drainPlanResult(result drainjob.Result) apiserver.DrainPlanResult {
	return movePlanResult(result.Plan, result.Accepted, result.MoveJobID)
}

// movePlanResult converts a mover.MovePlan into apiserver's own plain-data
// shape -- shared by Drain and Rename, since both answer the
// exact same DrainPlanResult DTO (structurally generic despite the name;
// see apiserver.RenameStarter's own doc comment).
func movePlanResult(plan mover.MovePlan, accepted bool, moveJobID int64) apiserver.DrainPlanResult {
	view := apiserver.DrainPlanResult{Accepted: accepted, MoveJobID: moveJobID}
	for _, m := range plan.Moves {
		view.FilesTotal++
		view.BytesTotal += m.SizeBytes
		view.Moves = append(view.Moves, apiserver.DrainMoveRow{
			MediaObjectID: m.MediaObjectID, SourceRelPath: m.SourceRelPath, DestRootFolderID: m.DestRootFolderID,
			DestRelPath: m.DestRelPath, SizeBytes: m.SizeBytes, AffinityKey: m.AffinityKey,
		})
	}
	for _, r := range plan.Refused {
		view.Refused = append(view.Refused, apiserver.DrainRefusalRow{
			MediaObjectID: r.MediaObjectID, AffinityKey: r.AffinityKey, Reason: r.Reason,
		})
	}
	for _, d := range plan.RootDeltas {
		view.RootDeltas = append(view.RootDeltas, apiserver.RootDeltaRow{
			RootFolderID: d.RootFolderID, BytesIn: d.BytesIn, BytesOut: d.BytesOut,
			ResultingFreeBytes: d.ResultingFreeBytes,
		})
	}
	return view
}

// Rename satisfies apiserver.RenameStarter over the media types that can rename.
type Rename struct {
	Registry *media.Registry
	// Settings answers which roots a library uses.
	Settings *settings.Cache
}

// renaming is the rename of a library's media type; a type without one is refused, never renamed by another's formats.
func (a Rename) renaming(categoryID int64) (media.Renaming, error) {
	category, found := a.Settings.Snapshot().Category(categoryID)
	if !found {
		return nil, fmt.Errorf("%w: category %d", settings.ErrNotFound, categoryID)
	}
	renaming, ok := media.Capability[media.Renaming](a.Registry, media.Key(category.MediaType))
	if !ok {
		return nil, errRenameNotBuilt(category.MediaType)
	}
	return renaming, nil
}

// StartLibraryRename renames the library's works on each of its roots.
func (a Rename) StartLibraryRename(
	ctx context.Context, categoryID, workID int64, preview bool,
) ([]apiserver.RootRename, error) {
	renaming, err := a.renaming(categoryID)
	if err != nil {
		return nil, err
	}
	var renames []apiserver.RootRename
	for _, root := range a.Settings.Snapshot().RootFoldersForCategory(categoryID) {
		plan, jobID, warnings, err := renaming.StartRename(ctx, root.ID, media.RenameScope{CategoryID: categoryID, WorkID: workID}, preview)
		if err != nil {
			return nil, err
		}
		result := movePlanResult(plan, jobID != 0, jobID)
		for i, move := range result.Moves {
			result.Moves[i].Warnings = warnings[move.MediaObjectID]
		}
		renames = append(renames, apiserver.RootRename{RootFolderID: root.ID, Plan: result})
	}
	return renames, nil
}

// errRenameNotBuilt refuses a rename the media type has no engine for; applying music's template to its files would
// misname them.
func errRenameNotBuilt(mediaType string) error {
	return fmt.Errorf("%w: renaming a %s library is not built yet; new imports are already named by its format", apiserver.ErrNotBuiltForMediaType, mediaType)
}

func (a Rename) StartRename(ctx context.Context, rootFolderID int64, preview bool) (apiserver.DrainPlanResult, error) {
	var renaming media.Renaming
	for _, attachment := range a.Settings.Snapshot().CategoryRoots() {
		if attachment.RootFolderID != rootFolderID {
			continue
		}
		found, err := a.renaming(attachment.CategoryID)
		if err != nil {
			return apiserver.DrainPlanResult{}, err
		}
		renaming = found
	}
	if renaming == nil {
		return apiserver.DrainPlanResult{}, fmt.Errorf("%w: root folder %d belongs to no library", settings.ErrNotFound, rootFolderID)
	}
	plan, jobID, warnings, err := renaming.StartRename(ctx, rootFolderID, media.RenameScope{}, preview)
	if err != nil {
		return apiserver.DrainPlanResult{}, err
	}
	result := movePlanResult(plan, jobID != 0, jobID)
	for i, move := range result.Moves {
		result.Moves[i].Warnings = warnings[move.MediaObjectID]
	}
	return result, nil
}

// MoveJobs satisfies apiserver.MoveJobLister over library.MoveJobs.
// Reason filtering happens here, in Go, since library.MoveJobs.List only
// filters by state -- move job counts never approach library scale
// (ListImportSessions' own comment gives the identical reasoning), so a
// second pass over an already-small slice costs nothing.
type MoveJobs struct{ MoveJobs library.MoveJobs }

func (a MoveJobs) ListMoveJobs(ctx context.Context, filterReason, filterState string) ([]apiserver.MoveJobRow, error) {
	rows, err := a.MoveJobs.List(ctx, filterState)
	if err != nil {
		return nil, err
	}
	var views []apiserver.MoveJobRow
	for _, row := range rows {
		if filterReason != "" && row.Reason != filterReason {
			continue
		}
		views = append(views, moveJobRow(row))
	}
	return views, nil
}

func moveJobRow(row library.Job) apiserver.MoveJobRow {
	view := apiserver.MoveJobRow{
		ID: row.ID, Reason: row.Reason, State: row.State, SourceRootID: row.SourceRootID,
		FilesTotal: row.FilesTotal, FilesDone: row.FilesDone,
		BytesTotal: row.BytesTotal, BytesDone: row.BytesDone,
		CreatedAt: time.Unix(row.CreatedAt, 0), Error: row.Error,
	}
	if row.FinishedAt != nil {
		at := time.Unix(*row.FinishedAt, 0)
		view.FinishedAt = &at
	}
	return view
}
