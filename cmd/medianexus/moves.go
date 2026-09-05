package main

import (
	"context"
	"errors"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/drainjob"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mover"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/settings"
)

// drainRoots adapts *settings.Cache to drainjob.Roots -- internal/drainjob
// takes settings.RootFolder/CategoryRoot for their types only (the same
// lightweight use internal/scan's own Roots interface already makes), so
// this is a thin method-shape bridge, not a translation of any real logic.
type drainRoots struct{ cache *settings.Cache }

func (a drainRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	return a.cache.Snapshot().RootFolder(id)
}

func (a drainRoots) CategoryRoots() []settings.CategoryRoot {
	return a.cache.Snapshot().CategoryRoots()
}

func (a drainRoots) SetAcceptsNewForRootFolder(ctx context.Context, rootFolderID int64, acceptsNew bool) error {
	return a.cache.SetAcceptsNewForRootFolder(ctx, rootFolderID, acceptsNew)
}

func (a drainRoots) SetRootFolderDrainState(ctx context.Context, id int64, state string, drainJobID *int64) error {
	return a.cache.SetRootFolderDrainState(ctx, id, state, drainJobID)
}

// drainAdapter satisfies apiserver.DrainStarter over internal/drainjob,
// translating its own not-found/no-files sentinels into what apiserver's
// writeError already recognises (settings.ErrNotFound -> 404,
// apiserver.ErrNothingToDrain -> 409) -- apiserver never imports
// internal/drainjob directly, the same boundary every other library-backed
// interface in this file already keeps.
type drainAdapter struct {
	config drainjob.Config
	cache  *settings.Cache
}

func (a drainAdapter) StartDrain(ctx context.Context, rootFolderID int64, preview bool) (apiserver.DrainPlanResult, error) {
	result, err := drainjob.Start(ctx, a.config, rootFolderID, preview)
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

// StopDrain clears drain_state and restores accepts_new -- the settings-only
// half of ADR-51's own "stop draining", not a cancellation of any move job
// already running (DELETE /moves/{id} is that, once it exists).
func (a drainAdapter) StopDrain(ctx context.Context, rootFolderID int64) error {
	if _, found := a.cache.Snapshot().RootFolder(rootFolderID); !found {
		return settings.ErrNotFound
	}
	if err := a.cache.SetAcceptsNewForRootFolder(ctx, rootFolderID, true); err != nil {
		return err
	}
	return a.cache.SetRootFolderDrainState(ctx, rootFolderID, settings.DrainStateNone, nil)
}

func drainPlanResult(result drainjob.Result) apiserver.DrainPlanResult {
	return movePlanResult(result.Plan, result.Accepted, result.MoveJobID)
}

// movePlanResult converts a mover.MovePlan into apiserver's own plain-data
// shape -- shared by drainAdapter and renameAdapter, since both answer the
// exact same DrainPlanResult DTO (structurally generic despite the name;
// see apiserver.RenameStarter's own doc comment).
func movePlanResult(plan mover.MovePlan, accepted bool, moveJobID int64) apiserver.DrainPlanResult {
	view := apiserver.DrainPlanResult{Accepted: accepted, MoveJobID: moveJobID}
	for _, m := range plan.Moves {
		view.FilesTotal++
		view.BytesTotal += m.SizeBytes
		view.Moves = append(view.Moves, apiserver.DrainMoveRow{
			MediaObjectID: m.MediaObjectID, DestRootFolderID: m.DestRootFolderID,
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

// renameAdapter satisfies apiserver.RenameStarter over
// internal/music.RenameStart.
type renameAdapter struct {
	config music.RenameConfig
}

func (a renameAdapter) StartRename(ctx context.Context, rootFolderID int64, preview bool) (apiserver.DrainPlanResult, error) {
	plan, jobID, err := music.RenameStart(ctx, a.config, rootFolderID, preview)
	if err != nil {
		return apiserver.DrainPlanResult{}, err
	}
	return movePlanResult(plan, jobID != 0, jobID), nil
}

// moveJobAdapter satisfies apiserver.MoveJobLister over library.MoveJobs.
// Reason filtering happens here, in Go, since library.MoveJobs.List only
// filters by state -- move job counts never approach library scale
// (ListImportSessions' own comment gives the identical reasoning), so a
// second pass over an already-small slice costs nothing.
type moveJobAdapter struct{ moveJobs library.MoveJobs }

func (a moveJobAdapter) ListMoveJobs(ctx context.Context, filterReason, filterState string) ([]apiserver.MoveJobRow, error) {
	rows, err := a.moveJobs.List(ctx, filterState)
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
