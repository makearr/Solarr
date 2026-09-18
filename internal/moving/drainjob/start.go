// Package drainjob is ADR-51's own POST /rootfolders/{id}/drain: gathers
// real facts (every file on the root, every other root sharing a category
// with it), calls mover.PlanDrain, and -- unless previewing or the plan
// refuses -- persists the accepted plan (internal/db/library.MoveJobs), forces
// accepts_new = 0 and drain_state = DRAINING, and enqueues internal/moving/moverjob
// to actually move the files. This is the first real caller of
// internal/moving/placement.Resolve.
package drainjob

import (
	"context"
	"encoding/json"
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

// Roots is the settings-side facts and writes Start needs. Only the types
// are taken from internal/db/settings (the same lightweight use
// internal/scanning/scan's own Roots interface already makes) -- the actual cache
// access stays behind this interface, a cmd/solarr adapter bridging it.
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

// Start computes ADR-51's drain plan for one root. With preview true, or
// when the plan refuses outright (nothing in Plan.Moves), nothing is
// persisted -- the caller gets the plan back to show why. Otherwise it
// creates and persists the move job, forces accepts_new = 0 and
// drain_state = DRAINING across every category the root shares (section
// 11.7), and enqueues moverjob's own Apply.
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
		Source: rootCandidate(source, false), SourceReadOnly: source.ReadOnly, SourceAllowRename: source.AllowRename,
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
		}
		bytesTotal += m.SizeBytes
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return Result{}, fmt.Errorf("drainjob: encode plan: %w", err)
	}

	jobID, err := cfg.MoveJobs.Create(ctx, library.NewMoveJob{
		Reason: string(mover.ReasonDrain), SourceRootID: &rootFolderID,
		FilesTotal: int64(len(items)), BytesTotal: bytesTotal,
		PlanJSON: string(planJSON), CreatedAt: cfg.Clock.Now().Unix(),
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

// eligibleDestinations answers every other root sharing at least one
// category with rootFolderID, real facts only. accepts_new is collapsed to
// "true if any shared category admits new placements" -- library.FileOnRoot
// carries no per-file category to check any finer than that against, the
// same all-categories scoping Start's own doc comment already states.
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
		candidates = append(candidates, rootCandidate(root, acceptsNew[id]))
	}
	return candidates
}

// Completer satisfies moverjob.DrainCompletion: once a DRAIN job's own
// moverjob.Job.Run finishes, it checks whether the drained root now holds
// no files at all, and if so advances drain_state from DRAINING to
// DRAINED. A live container run found this transition never happened
// before Completer existed -- nothing had ever checked back once a drain's
// own move job finished.
type Completer struct {
	Roots Roots
	Files Files
}

// CompleteDrainIfEmpty is a no-op unless rootFolderID's own drain_state is
// still DRAINING for exactly this moveJobID -- a stale or already-advanced
// state, or a moveJobID that is not the root's own current drain, means
// this is not this drain's job to complete (a concurrent drain replaced it,
// or StopDrain already reset it). Files left behind (a partial failure)
// leave drain_state at DRAINING rather than falsely advancing it.
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

func rootCandidate(root settings.RootFolder, acceptsNew bool) placement.RootCandidate {
	var freeBytes, totalBytes int64
	if root.FreeBytes != nil {
		freeBytes = *root.FreeBytes
	}
	if root.TotalBytes != nil {
		totalBytes = *root.TotalBytes
	}
	return placement.RootCandidate{
		RootFolderID: root.ID, FSID: root.FSID, Online: root.Online, Writable: root.Writable,
		ReadOnly: root.ReadOnly, AcceptsNew: acceptsNew, FreeBytes: freeBytes, TotalBytes: totalBytes,
		MinFreeBytes: root.MinFreeBytes,
	}
}
