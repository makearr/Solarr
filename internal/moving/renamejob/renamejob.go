// Package renamejob plans and starts the rename of a root's files once a media type has said what each should be
// called: one planner and one move job for every type (ADR-51), the type supplying only the proposed names.
package renamejob

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/moverjob"
	"github.com/makearr/Solarr/internal/moving/naming"
	"github.com/makearr/Solarr/internal/moving/placement"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Target is one file and the path its library's naming gives it.
type Target struct {
	MediaObjectID   int64
	WorkID          int64
	SizeBytes       int64
	CurrentRelPath  string
	ProposedRelPath string
	// Warnings are naming.RenderPath's non-fatal notices about the proposed path.
	Warnings []naming.Warning
	// Companions move with the file: what is beside it and, when its whole folder moves, the folder's own files.
	Companions []mover.Companion
}

// Changed reports whether the proposed path differs from the current one.
func (t Target) Changed() bool { return t.ProposedRelPath != t.CurrentRelPath }

// Roots resolves one root folder's own policy and filesystem facts.
type Roots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// MoveJobs is the library-side persistence a started rename needs; satisfied by library.MoveJobs.
type MoveJobs interface {
	Create(ctx context.Context, params library.NewMoveJob) (int64, error)
	AddItems(ctx context.Context, jobID int64, items []library.NewMoveItem) error
}

// Config is what Start needs; Clock is optional and defaults to the real clock.
type Config struct {
	Roots    Roots
	MoveJobs MoveJobs
	Queue    *jobs.Queue
	Clock    clock.Clock
}

// Start plans the targets on one root (one affinity group per work, so a work renames as one unit under mover.Plan's
// own guards: read-only plus allow_rename, and the same-root reserve exemption). With preview true, or when the plan
// refuses or has nothing to move, nothing is persisted and the job id is 0. Otherwise the move job is persisted and its
// apply enqueued. warnings are keyed by media object id. categoryScope, when not zero, limits the job to one library.
func Start(
	ctx context.Context, cfg Config, rootFolderID, categoryScope int64, targets []Target, preview bool,
) (mover.MovePlan, int64, map[int64][]string, error) {
	if len(targets) == 0 {
		return mover.MovePlan{}, 0, nil, nil
	}
	warnings := make(map[int64][]string, len(targets))
	for _, target := range targets {
		if len(target.Warnings) == 0 {
			continue
		}
		messages := make([]string, len(target.Warnings))
		for i, warning := range target.Warnings {
			messages[i] = warning.Message
		}
		warnings[target.MediaObjectID] = messages
	}

	root, ok := cfg.Roots.RootFolder(rootFolderID)
	if !ok {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: root folder %d not found", rootFolderID)
	}
	files := make([]mover.FileToMove, len(targets))
	for i, target := range targets {
		files[i] = mover.FileToMove{
			MediaObjectID: target.MediaObjectID, SourceRootFolderID: rootFolderID,
			SourceRelPath: target.CurrentRelPath, SizeBytes: target.SizeBytes,
			AffinityKey:      fmt.Sprintf("work:%d", target.WorkID),
			DestRootFolderID: rootFolderID, DestRelPath: target.ProposedRelPath, Companions: target.Companions,
		}
	}
	candidate := rootCandidate(rootFolderID, root)
	request := mover.Request{
		Reason: mover.ReasonRename, Files: files,
		Roots:             map[int64]placement.RootCandidate{rootFolderID: candidate},
		SourceReadOnly:    map[int64]bool{rootFolderID: root.ReadOnly},
		SourceAllowRename: map[int64]bool{rootFolderID: root.AllowRename},
	}
	plan, err := mover.Plan(request)
	if err != nil {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: plan rename for root %d: %w", rootFolderID, err)
	}
	if preview && len(plan.Refused) > 0 {
		// A preview names the files even on a root it may not change (user, 2026-10-23); the refusals stay beside them.
		plan.Moves, err = previewNames(request, candidate)
		if err != nil {
			return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: preview names for root %d: %w", rootFolderID, err)
		}
	}
	if preview || len(plan.Moves) == 0 {
		return plan, 0, warnings, nil
	}

	items := make([]library.NewMoveItem, len(plan.Moves))
	var bytesTotal int64
	for i, move := range plan.Moves {
		items[i] = library.NewMoveItem{
			MediaObjectID: move.MediaObjectID, AffinityKey: move.AffinityKey,
			DestRootID: move.DestRootFolderID, DestRelPath: move.DestRelPath, Bytes: move.SizeBytes,
		}
		for _, companion := range move.Companions {
			items[i].Companions = append(items[i].Companions, library.MoveCompanion(companion))
		}
		bytesTotal += move.SizeBytes
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: encode rename plan: %w", err)
	}
	now := cfg.Clock
	if now == nil {
		now = clock.NewSystem()
	}
	var onlyCategoryID *int64
	if categoryScope != 0 {
		onlyCategoryID = &categoryScope
	}
	jobID, err := cfg.MoveJobs.Create(ctx, library.NewMoveJob{
		Reason: string(mover.ReasonRename), FilesTotal: int64(len(items)), BytesTotal: bytesTotal,
		PlanJSON: string(planJSON), CreatedAt: now.Now().Unix(), CategoryID: onlyCategoryID,
	})
	if err != nil {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: create move job for root %d: %w", rootFolderID, err)
	}
	if err := cfg.MoveJobs.AddItems(ctx, jobID, items); err != nil {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: add items to move job %d: %w", jobID, err)
	}
	if _, err := moverjob.Enqueue(ctx, cfg.Queue, jobID); err != nil {
		return mover.MovePlan{}, 0, nil, fmt.Errorf("renamejob: enqueue move job %d: %w", jobID, err)
	}
	return plan, jobID, warnings, nil
}

// previewNames plans the request as if the root were online, writable and allowed to rename, for names alone.
func previewNames(request mover.Request, candidate placement.RootCandidate) ([]mover.PlannedMove, error) {
	candidate.Online, candidate.Writable, candidate.ReadOnly = true, true, false
	request.Roots = map[int64]placement.RootCandidate{candidate.RootFolderID: candidate}
	request.SourceReadOnly = nil
	names, err := mover.Plan(request)
	return names.Moves, err
}

func rootCandidate(rootFolderID int64, root settings.RootFolder) placement.RootCandidate {
	var freeBytes, totalBytes int64
	if root.FreeBytes != nil {
		freeBytes = *root.FreeBytes
	}
	if root.TotalBytes != nil {
		totalBytes = *root.TotalBytes
	}
	return placement.RootCandidate{
		RootFolderID: rootFolderID, FSID: root.FSID, Online: root.Online, Writable: root.Writable,
		ReadOnly: root.ReadOnly, FreeBytes: freeBytes, TotalBytes: totalBytes, MinFreeBytes: root.MinFreeBytes,
	}
}
