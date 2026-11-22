// Package drainjob is ADR-51's POST /rootfolders/{id}/drain: it gathers facts, plans via mover.PlanDrain, persists an accepted plan, sets accepts_new = 0 and DRAINING, and enqueues moverjob; the first caller of placement.Resolve.
package drainjob

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/moverjob"
	"github.com/makearr/Solarr/internal/moving/placement"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Roots is the settings-side facts and writes Start needs; only the types come from internal/db/settings, and a cmd/solarr adapter bridges the cache.
type Roots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
	CategoryRoots() []settings.CategoryRoot
	SetAcceptsNewForRootFolder(ctx context.Context, rootFolderID int64, acceptsNew bool) error
	SetRootFolderDrainState(ctx context.Context, id int64, state string, drainJobID *int64) error
}

// Files is the library-side fact Start needs: every real file on the root
// being drained.
type Files interface {
	PathsForRoot(ctx context.Context, rootFolderID int64) ([]library.FileOnRoot, error)
}

// MoveJobs is the library-side persistence Start needs once a plan is
// accepted.
type MoveJobs interface {
	Create(ctx context.Context, params library.NewMoveJob) (int64, error)
	AddItems(ctx context.Context, jobID int64, items []library.NewMoveItem) error
}

// Config is what Start needs. Every field is required.
type Config struct {
	Roots    Roots
	Files    Files
	MoveJobs MoveJobs
	Queue    *jobs.Queue
	Clock    clock.Clock
}

// ErrNoFiles means the root has nothing on it to drain -- not a failure,
// just nothing to do, so the caller can report it distinctly from a real
// refusal.
var ErrNoFiles = errors.New("drainjob: root folder has no files to drain")

// ErrRootFolderNotFound means the id Start was asked to drain does not
// exist.
var ErrRootFolderNotFound = errors.New("drainjob: root folder not found")

// Result is what Start answers: the computed plan (always, even refused or
// previewed, so the caller can show why), and the move job id once accepted.
type Result struct {
	Plan      mover.MovePlan
	MoveJobID int64
	Accepted  bool
}

// Start computes ADR-51's drain plan for one root; when previewing or the plan refuses, nothing is persisted, else it persists the move job, forces accepts_new = 0 and DRAINING across shared categories (section 11.7) and enqueues Apply.
func Start(ctx context.Context, cfg Config, rootFolderID int64, preview bool) (Result, error) {
	source, ok := cfg.Roots.RootFolder(rootFolderID)
	if !ok {
		return Result{}, fmt.Errorf("%w: %d", ErrRootFolderNotFound, rootFolderID)
	}

	files, err := cfg.Files.PathsForRoot(ctx, rootFolderID)
	if err != nil {
		return Result{}, fmt.Errorf("drainjob: list files on root %d: %w", rootFolderID, err)
	}
	if len(files) == 0 {
		return Result{}, ErrNoFiles
	}
	drainFiles := make([]mover.FileOnRoot, len(files))
	for i, f := range files {
		drainFiles[i] = mover.FileOnRoot{MediaObjectID: f.MediaObjectID, RelPath: f.RelPath, SizeBytes: f.SizeBytes}
	}

	plan, err := mover.PlanDrain(mover.DrainRequest{
		Source: placement.CandidateOf(source, false), SourceReadOnly: source.ReadOnly, SourceAllowRename: source.AllowRename,
		Files: drainFiles, Destinations: eligibleDestinations(cfg.Roots, rootFolderID),
	})
	if err != nil {
		return Result{}, fmt.Errorf("drainjob: plan drain for root %d: %w", rootFolderID, err)
	}
	if preview || len(plan.Moves) == 0 {
		return Result{Plan: plan}, nil
	}

	return commit(ctx, cfg, rootFolderID, plan)
}

// commit persists an accepted plan and starts moving it -- split out from
// Start so the "nothing to persist" branches above stay a plain early
// return, not a conditional wrapping the whole write sequence.
func commit(ctx context.Context, cfg Config, rootFolderID int64, plan mover.MovePlan) (Result, error) {
	items := make([]library.NewMoveItem, len(plan.Moves))
	var bytesTotal int64
	for i, m := range plan.Moves {
		items[i] = library.NewMoveItem{
			MediaObjectID: m.MediaObjectID, AffinityKey: m.AffinityKey,
			DestRootID: m.DestRootFolderID, DestRelPath: m.DestRelPath, Bytes: m.SizeBytes,
			SourceRootID: m.SourceRootFolderID, SourceRelPath: m.SourceRelPath,
		}
		bytesTotal += m.SizeBytes
	}

	jobID, err := cfg.MoveJobs.Create(ctx, library.NewMoveJob{
		Reason: string(mover.ReasonDrain), SourceRootID: &rootFolderID,
		FilesTotal: int64(len(items)), BytesTotal: bytesTotal,
		CreatedAt: cfg.Clock.Now().Unix(),
	})
	if err != nil {
		return Result{}, fmt.Errorf("drainjob: create move job for root %d: %w", rootFolderID, err)
	}
	if err := cfg.MoveJobs.AddItems(ctx, jobID, items); err != nil {
		return Result{}, fmt.Errorf("drainjob: add items to move job %d: %w", jobID, err)
	}
	if err := cfg.Roots.SetAcceptsNewForRootFolder(ctx, rootFolderID, false); err != nil {
		return Result{}, fmt.Errorf("drainjob: force accepts_new=0 for root %d: %w", rootFolderID, err)
	}
	if err := cfg.Roots.SetRootFolderDrainState(ctx, rootFolderID, settings.DrainStateDraining, &jobID); err != nil {
		return Result{}, fmt.Errorf("drainjob: set drain state for root %d: %w", rootFolderID, err)
	}
	if _, err := moverjob.Enqueue(ctx, cfg.Queue, jobID); err != nil {
		return Result{}, fmt.Errorf("drainjob: enqueue move job %d: %w", jobID, err)
	}

	return Result{Plan: plan, MoveJobID: jobID, Accepted: true}, nil
}

// eligibleDestinations answers every other root sharing a category with rootFolderID; accepts_new is true if any shared category admits placements, as library.FileOnRoot has no per-file category.
func eligibleDestinations(roots Roots, rootFolderID int64) []placement.RootCandidate {
	sourceCategories := map[int64]bool{}
	for _, attachment := range roots.CategoryRoots() {
		if attachment.RootFolderID == rootFolderID {
			sourceCategories[attachment.CategoryID] = true
		}
	}

	acceptsNew := map[int64]bool{}
	var order []int64
	seen := map[int64]bool{}
	for _, attachment := range roots.CategoryRoots() {
		if attachment.RootFolderID == rootFolderID || !sourceCategories[attachment.CategoryID] {
			continue
		}
		if !seen[attachment.RootFolderID] {
			seen[attachment.RootFolderID] = true
			order = append(order, attachment.RootFolderID)
		}
		if attachment.AcceptsNew {
			acceptsNew[attachment.RootFolderID] = true
		}
	}

	candidates := make([]placement.RootCandidate, 0, len(order))
	for _, id := range order {
		root, ok := roots.RootFolder(id)
		if !ok {
			continue
		}
		candidates = append(candidates, placement.CandidateOf(root, acceptsNew[id]))
	}
	return candidates
}

// Completer satisfies moverjob.DrainCompletion: when a DRAIN job finishes with the root holding no files it advances drain_state DRAINING to DRAINED, a transition nothing performed before.
type Completer struct {
	Roots Roots
	Files Files
}

// CompleteDrainIfEmpty is a no-op unless drain_state is still DRAINING for this moveJobID (a concurrent drain or StopDrain may have replaced it); leftover files keep it DRAINING.
func (c Completer) CompleteDrainIfEmpty(ctx context.Context, rootFolderID, moveJobID int64) error {
	root, ok := c.Roots.RootFolder(rootFolderID)
	if !ok || root.DrainState != settings.DrainStateDraining || root.DrainJobID == nil || *root.DrainJobID != moveJobID {
		return nil
	}

	remaining, err := c.Files.PathsForRoot(ctx, rootFolderID)
	if err != nil {
		return fmt.Errorf("drainjob: check root %d is empty: %w", rootFolderID, err)
	}
	if len(remaining) > 0 {
		return nil
	}

	return c.Roots.SetRootFolderDrainState(ctx, rootFolderID, settings.DrainStateDrained, nil)
}
