package music

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mover"
	"github.com/makearr/MediaNexus/internal/moverjob"
	"github.com/makearr/MediaNexus/internal/placement"
	"github.com/makearr/MediaNexus/internal/settings"
)

// RenameRoots resolves one root folder's own policy and filesystem facts.
// Only the type is taken from internal/settings (the same lightweight use
// internal/scan's own Roots interface already makes) -- actual cache access
// stays behind this interface, satisfied by the same adapter
// cmd/medianexus already built for internal/drainjob.Roots, a strict
// superset of this one method.
type RenameRoots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// RenameFiles is the library-side fact a preview needs: every real file on
// the root. Satisfied directly by library.Physical.
type RenameFiles interface {
	PathsForRoot(ctx context.Context, rootFolderID int64) ([]library.FileOnRoot, error)
}

// RenameIdentities resolves a file's editorial identity. Satisfied directly
// by library.Desired.
type RenameIdentities interface {
	IdentityForFile(ctx context.Context, mediaObjectID int64) (library.FileIdentity, bool, error)
}

// RenameWorkDetails answers a work's full detail. Satisfied directly by
// library.Works.
type RenameWorkDetails interface {
	Detail(ctx context.Context, workID int64) (library.WorkDetail, error)
}

// RenameMoveJobs is the library-side persistence a commit needs. Satisfied
// directly by library.MoveJobs.
type RenameMoveJobs interface {
	Create(ctx context.Context, params library.NewMoveJob) (int64, error)
	AddItems(ctx context.Context, jobID int64, items []library.NewMoveItem) error
}

// RenameConfig is what RenamePreview and RenameCommit need. Every field is
// required except Clock, which is optional and defaults to the real clock.
type RenameConfig struct {
	Roots      RenameRoots
	Files      RenameFiles
	Identities RenameIdentities
	Works      RenameWorkDetails
	MoveJobs   RenameMoveJobs
	Queue      *jobs.Queue
	Clock      clock.Clock
}

// RenamePreview computes every changed rename target on one root, under the
// shipped default template -- section 11.8's own "preview as a diff"
// (Invariant 73). A file with no editorial identity (never aligned to a
// desired_item) is skipped, and so is one whose identity names no real
// work_part in its own work -- neither is fatal to the whole preview, the
// same "one bad file does not fail the batch" reasoning ADR-48/ADR-51
// already apply elsewhere. A work's own WorkDetail is fetched at most once
// per call, since every track on an album shares it.
func RenamePreview(ctx context.Context, cfg RenameConfig, rootFolderID int64) ([]RenameTarget, error) {
	files, err := cfg.Files.PathsForRoot(ctx, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("music: list files on root %d: %w", rootFolderID, err)
	}

	details := map[int64]library.WorkDetail{}
	var targets []RenameTarget
	for _, f := range files {
		identity, found, err := cfg.Identities.IdentityForFile(ctx, f.MediaObjectID)
		if err != nil {
			return nil, fmt.Errorf("music: identity for media object %d: %w", f.MediaObjectID, err)
		}
		if !found {
			continue
		}
		detail, cached := details[identity.WorkID]
		if !cached {
			detail, err = cfg.Works.Detail(ctx, identity.WorkID)
			if err != nil {
				return nil, fmt.Errorf("music: work detail for %d: %w", identity.WorkID, err)
			}
			details[identity.WorkID] = detail
		}
		target, err := RenameTargetFor(detail, identity, f.MediaObjectID, f.RelPath)
		if err != nil {
			continue
		}
		if target.Changed() {
			targets = append(targets, target)
		}
	}
	return targets, nil
}

// RenameCommit computes the same preview RenamePreview would, then --
// unless there is nothing to rename -- plans it (one affinity group per
// work, so an album renames as one unit) and, unless the plan refuses,
// persists the move job and enqueues moverjob's own Apply. moveJobID is 0
// when nothing was committed (nothing to rename, or the plan refused
// outright) -- the caller reads plan.Refused for why.
func RenameCommit(ctx context.Context, cfg RenameConfig, rootFolderID int64) (mover.MovePlan, int64, error) {
	targets, err := RenamePreview(ctx, cfg, rootFolderID)
	if err != nil {
		return mover.MovePlan{}, 0, err
	}
	if len(targets) == 0 {
		return mover.MovePlan{}, 0, nil
	}

	root, ok := cfg.Roots.RootFolder(rootFolderID)
	if !ok {
		return mover.MovePlan{}, 0, fmt.Errorf("music: root folder %d not found", rootFolderID)
	}

	files := make([]mover.FileToMove, 0, len(targets))
	for _, target := range targets {
		// The identity was already resolved once inside RenamePreview above;
		// re-resolving it here for its own WorkID (RenameTarget itself does
		// not carry one) is a second, cheap lookup rather than widening
		// RenameTarget's own shape for one caller's grouping need.
		identity, found, err := cfg.Identities.IdentityForFile(ctx, target.MediaObjectID)
		if err != nil {
			return mover.MovePlan{}, 0, fmt.Errorf("music: identity for media object %d: %w", target.MediaObjectID, err)
		}
		if !found {
			continue
		}
		files = append(files, mover.FileToMove{
			MediaObjectID: target.MediaObjectID, SourceRootFolderID: rootFolderID,
			SourceRelPath: target.CurrentRelPath, AffinityKey: fmt.Sprintf("work:%d", identity.WorkID),
			DestRootFolderID: rootFolderID, DestRelPath: target.ProposedRelPath,
		})
	}

	rootCandidate := renameRootCandidate(rootFolderID, root)
	plan, err := mover.Plan(mover.Request{
		Reason: mover.ReasonRename, Files: files,
		Roots:             map[int64]placement.RootCandidate{rootFolderID: rootCandidate},
		SourceReadOnly:    map[int64]bool{rootFolderID: root.ReadOnly},
		SourceAllowRename: map[int64]bool{rootFolderID: root.AllowRename},
	})
	if err != nil {
		return mover.MovePlan{}, 0, fmt.Errorf("music: plan rename for root %d: %w", rootFolderID, err)
	}
	if len(plan.Moves) == 0 {
		return plan, 0, nil
	}

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
		return mover.MovePlan{}, 0, fmt.Errorf("music: encode rename plan: %w", err)
	}

	cl := cfg.Clock
	if cl == nil {
		cl = clock.NewSystem()
	}
	jobID, err := cfg.MoveJobs.Create(ctx, library.NewMoveJob{
		Reason: string(mover.ReasonRename), FilesTotal: int64(len(items)), BytesTotal: bytesTotal,
		PlanJSON: string(planJSON), CreatedAt: cl.Now().Unix(),
	})
	if err != nil {
		return mover.MovePlan{}, 0, fmt.Errorf("music: create move job for root %d: %w", rootFolderID, err)
	}
	if err := cfg.MoveJobs.AddItems(ctx, jobID, items); err != nil {
		return mover.MovePlan{}, 0, fmt.Errorf("music: add items to move job %d: %w", jobID, err)
	}
	if _, err := moverjob.Enqueue(ctx, cfg.Queue, jobID); err != nil {
		return mover.MovePlan{}, 0, fmt.Errorf("music: enqueue move job %d: %w", jobID, err)
	}

	return plan, jobID, nil
}

func renameRootCandidate(rootFolderID int64, root settings.RootFolder) placement.RootCandidate {
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
