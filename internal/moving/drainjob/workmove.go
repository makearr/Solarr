package drainjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/moverjob"
	"github.com/makearr/Solarr/internal/moving/placement"
)

// WorkFiles is the library-side fact StartWorkMove needs: one work's files on one root.
type WorkFiles interface {
	PathsForWorkOnRoot(ctx context.Context, rootFolderID, workID int64) ([]library.FileOnRoot, error)
}

// ErrNothingToMove means the work has no file off the target root: already there, or no files at all.
var ErrNothingToMove = errors.New("drainjob: the work has no files to move to that root")

// StartWorkMove moves every file of one work, from every root of the target's libraries, to the target root under
// the same relative path. It is all or nothing: a plan that refuses any file is answered, never started.
func StartWorkMove(ctx context.Context, cfg Config, files WorkFiles, workID, targetRootID int64, preview bool) (Result, error) {
	target, ok := cfg.Roots.RootFolder(targetRootID)
	if !ok {
		return Result{}, fmt.Errorf("%w: %d", ErrRootFolderNotFound, targetRootID)
	}
	roots := map[int64]placement.RootCandidate{targetRootID: rootCandidate(target, true)}
	readOnly, allowRename := map[int64]bool{}, map[int64]bool{}
	var moves []mover.FileToMove
	for _, sourceID := range sharingRoots(cfg.Roots, targetRootID) {
		source, ok := cfg.Roots.RootFolder(sourceID)
		if !ok {
			continue
		}
		onRoot, err := files.PathsForWorkOnRoot(ctx, sourceID, workID)
		if err != nil {
			return Result{}, fmt.Errorf("drainjob: files of work %d on root %d: %w", workID, sourceID, err)
		}
		if len(onRoot) == 0 {
			continue
		}
		roots[sourceID] = rootCandidate(source, false)
		readOnly[sourceID], allowRename[sourceID] = source.ReadOnly, source.AllowRename
		for _, file := range onRoot {
			moves = append(moves, mover.FileToMove{
				MediaObjectID: file.MediaObjectID, SourceRootFolderID: sourceID, SourceRelPath: file.RelPath,
				SizeBytes: file.SizeBytes, AffinityKey: mover.AffinityKeyForPath(file.RelPath),
				DestRootFolderID: targetRootID, DestRelPath: file.RelPath,
			})
		}
	}
	if len(moves) == 0 {
		return Result{}, ErrNothingToMove
	}
	plan, err := mover.Plan(mover.Request{
		Reason: mover.ReasonMove, Files: moves, Roots: roots, SourceReadOnly: readOnly, SourceAllowRename: allowRename,
	})
	if err != nil {
		return Result{}, fmt.Errorf("drainjob: plan the move of work %d: %w", workID, err)
	}
	if preview || len(plan.Refused) > 0 || len(plan.Moves) == 0 {
		return Result{Plan: plan}, nil
	}
	return commitMove(ctx, cfg, plan)
}

// sharingRoots is every root other than rootFolderID that shares a library with it.
func sharingRoots(roots Roots, rootFolderID int64) []int64 {
	libraries := map[int64]bool{}
	for _, attachment := range roots.CategoryRoots() {
		if attachment.RootFolderID == rootFolderID {
			libraries[attachment.CategoryID] = true
		}
	}
	var sharing []int64
	seen := map[int64]bool{rootFolderID: true}
	for _, attachment := range roots.CategoryRoots() {
		if libraries[attachment.CategoryID] && !seen[attachment.RootFolderID] {
			seen[attachment.RootFolderID] = true
			sharing = append(sharing, attachment.RootFolderID)
		}
	}
	return sharing
}

// commitMove persists a work move and starts it; unlike a drain it leaves every root's state alone.
func commitMove(ctx context.Context, cfg Config, plan mover.MovePlan) (Result, error) {
	items := make([]library.NewMoveItem, len(plan.Moves))
	var bytesTotal int64
	for i, m := range plan.Moves {
		items[i] = library.NewMoveItem{
			MediaObjectID: m.MediaObjectID, AffinityKey: m.AffinityKey,
			DestRootID: m.DestRootFolderID, DestRelPath: m.DestRelPath, Bytes: m.SizeBytes,
		}
		bytesTotal += m.SizeBytes
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return Result{}, fmt.Errorf("drainjob: encode plan: %w", err)
	}
	jobID, err := cfg.MoveJobs.Create(ctx, library.NewMoveJob{
		Reason: string(mover.ReasonMove), FilesTotal: int64(len(items)), BytesTotal: bytesTotal,
		PlanJSON: string(planJSON), CreatedAt: cfg.Clock.Now().Unix(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("drainjob: create move job: %w", err)
	}
	if err := cfg.MoveJobs.AddItems(ctx, jobID, items); err != nil {
		return Result{}, fmt.Errorf("drainjob: add items to move job %d: %w", jobID, err)
	}
	if _, err := moverjob.Enqueue(ctx, cfg.Queue, jobID); err != nil {
		return Result{}, fmt.Errorf("drainjob: enqueue move job %d: %w", jobID, err)
	}
	return Result{Plan: plan, MoveJobID: jobID, Accepted: true}, nil
}
