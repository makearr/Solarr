// Package moverjob is ADR-51's resumable Apply: it works through one
// library.MoveJobs job's own move_item rows, calling internal/mover.ApplyOne
// per file, then RECORDs (library.Physical.Relocate), UNLINKs the old path,
// and TIDYs now-empty source directories -- the same pure-engine/persist-in-
// library/orchestrate-separately split internal/importjob already
// established for WALK/CLASSIFY/GROUP/EVIDENCE/IDENTIFY/SHAPE/DECIDE.
package moverjob

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mover"
)

// JobType is the registered type of one Apply run over an accepted move plan.
const JobType = jobs.Type("move.apply")

// Definition declares how the job behaves -- bulk, resumable, and never in
// front of anything interactive, the same reasoning importjob.Definition
// already gives.
var Definition = jobs.Definition{
	Type: JobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 3,
}

// ActionKeyFor deduplicates concurrent submissions of the same move job.
func ActionKeyFor(moveJobID int64) string {
	return fmt.Sprintf("%s:%d", JobType, moveJobID)
}

// Payload is what an Apply run carries: which already-persisted move_job to
// work through. The plan itself is not re-derived here -- library.MoveJobs
// already holds it, one move_item row per file mover.Plan approved.
type Payload struct {
	MoveJobID int64 `json:"move_job_id"`
}

// RootFolder is the subset of settings.RootFolder Apply needs: where a root
// lives, and which filesystem it is on. FSID is not persisted on move_item
// (mover.Plan decided SameFilesystem once at planning time, but that
// decision is not carried into the schema), so Apply re-derives it fresh
// from whichever root the file currently lives under -- the same "ask
// fresh, never cache" discipline every settings-backed job dependency in
// this codebase already follows.
type RootFolder struct {
	Path string
	FSID string
}

// RootFolders resolves a root folder id to its current path and filesystem.
// internal/moverjob does not import internal/settings directly, the same
// separation internal/mover and internal/importjob already keep -- a
// cmd/medianexus adapter bridges this to the real settings cache.
type RootFolders interface {
	RootFolder(rootFolderID int64) (RootFolder, bool)
}

// Job runs one move.apply execution: claim the next pending item, apply it,
// record and unlink, repeat until the job's items are all terminal.
type Job struct {
	fsys     fsx.FS
	moveJobs library.MoveJobs
	physical library.Physical
	roots    RootFolders
	clock    clock.Clock
	logger   *slog.Logger
}

// Config is what a Job needs. Every field is required except Logger.
type Config struct {
	FS       fsx.FS
	MoveJobs library.MoveJobs
	Physical library.Physical
	Roots    RootFolders
	Clock    clock.Clock
	Logger   *slog.Logger
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{
		fsys: config.FS, moveJobs: config.MoveJobs, physical: config.Physical,
		roots: config.Roots, clock: config.Clock, logger: logger,
	}
}

// Run works through moveJob's own pending items until none remain. One
// item's failure is recorded as MoveItemFailed and does not stop the rest --
// the same "one bad file does not fail the batch" reasoning already applied
// to import.inspect's own per-unit errors -- so the job itself only fails on
// something that stops it from making progress at all (a bad payload, a
// store error). A retried Run resumes rather than repeats: NextPendingItem
// only ever returns items that have not yet reached DONE or FAILED.
func (j *Job) Run(ctx context.Context, job jobs.Job) error {
	var payload Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Terminalf("moverjob.payload", string(job.Type), err)
	}

	moveJob, err := j.moveJobs.Get(ctx, payload.MoveJobID)
	if err != nil {
		return fmt.Errorf("moverjob: get move job %d: %w", payload.MoveJobID, err)
	}
	if moveJob.State == library.MoveJobPlanned {
		if err := j.moveJobs.SetRunning(ctx, moveJob.ID); err != nil {
			return fmt.Errorf("moverjob: set move job %d running: %w", moveJob.ID, err)
		}
	}

	for {
		item, err := j.moveJobs.NextPendingItem(ctx, moveJob.ID)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return fmt.Errorf("moverjob: next pending item for job %d: %w", moveJob.ID, err)
		}

		if err := j.applyItem(ctx, item); err != nil {
			j.logger.Warn("move item failed",
				slog.Int64("move_job_id", moveJob.ID), slog.Int64("media_object_id", item.MediaObjectID),
				slog.String("error", err.Error()))
			if setErr := j.moveJobs.SetItemState(
				ctx, moveJob.ID, item.MediaObjectID, library.MoveItemFailed, err.Error(),
			); setErr != nil {
				return fmt.Errorf("moverjob: record failure for item %d: %w", item.MediaObjectID, setErr)
			}
		}
	}

	j.logger.Info("move job finished", slog.Int64("move_job_id", moveJob.ID))
	return j.moveJobs.Finish(ctx, moveJob.ID, library.MoveJobDone, j.clock.Now().Unix(), "")
}

// applyItem moves one file: locate its current path, copy/verify or rename
// it into place (mover.ApplyOne), RECORD the new location, UNLINK the old
// one, then TIDY now-empty source directories -- ADR-51's own
// copy-verify-repoint-unlink order (Invariant 77), repoint and unlink both
// the caller's job since ApplyOne imports no store.
func (j *Job) applyItem(ctx context.Context, item library.Item) error {
	paths, err := j.physical.PathsForObject(ctx, item.MediaObjectID)
	if err != nil {
		return fmt.Errorf("paths for media object %d: %w", item.MediaObjectID, err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("media object %d has no library_path left to move", item.MediaObjectID)
	}
	primary := paths[0]

	if primary.RootFolderID == item.DestRootID && primary.RelPath == item.DestRelPath {
		// Already at the destination: a prior attempt completed RECORD (and
		// likely UNLINK) before crashing on the bookkeeping below. The old
		// location is not recoverable from move_item, which does not
		// persist it -- nothing left to do but finish the item.
		return j.moveJobs.SetItemState(ctx, item.MoveJobID, item.MediaObjectID, library.MoveItemDone, "")
	}

	sourceRoot, ok := j.roots.RootFolder(primary.RootFolderID)
	if !ok {
		return fmt.Errorf("source root %d has no known path", primary.RootFolderID)
	}
	destRoot, ok := j.roots.RootFolder(item.DestRootID)
	if !ok {
		return fmt.Errorf("destination root %d has no known path", item.DestRootID)
	}

	move := mover.PlannedMove{
		MediaObjectID: item.MediaObjectID, SourceRootFolderID: primary.RootFolderID,
		SourceRelPath: primary.RelPath, DestRootFolderID: item.DestRootID, DestRelPath: item.DestRelPath,
		SizeBytes: item.Bytes, AffinityKey: item.AffinityKey,
		SameFilesystem: sourceRoot.FSID != "" && sourceRoot.FSID == destRoot.FSID,
	}
	rootPath := func(rootFolderID int64) (string, bool) {
		root, ok := j.roots.RootFolder(rootFolderID)
		return root.Path, ok
	}
	if err := mover.ApplyOne(j.fsys, move, rootPath); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	if err := j.moveJobs.SetItemState(ctx, item.MoveJobID, item.MediaObjectID, library.MoveItemVerified, ""); err != nil {
		return fmt.Errorf("record verified: %w", err)
	}

	if err := j.physical.Relocate(
		ctx, primary.RootFolderID, primary.RelPath, item.DestRootID, item.DestRelPath,
	); err != nil {
		return fmt.Errorf("record relocation: %w", err)
	}

	sourceAbs := path.Join(sourceRoot.Path, primary.RelPath)
	if err := j.fsys.Remove(sourceAbs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// SameFilesystem's own rename(2) already moved the file, so a
		// same-filesystem move reaches here with nothing left to unlink --
		// ErrNotExist is the expected outcome then, not a failure.
		return fmt.Errorf("unlink source %q: %w", sourceAbs, err)
	}
	j.tidy(path.Dir(sourceAbs), sourceRoot.Path)

	return j.moveJobs.SetItemState(ctx, item.MoveJobID, item.MediaObjectID, library.MoveItemDone, "")
}

// tidy removes now-empty directories from dir up to (never including)
// rootBase. Best-effort: any error, including "not empty", just stops --
// TIDY is a courtesy cleanup, not a correctness guarantee.
func (j *Job) tidy(dir, rootBase string) {
	for dir != rootBase && dir != "." && dir != "/" && dir != "" {
		if err := j.fsys.Remove(dir); err != nil {
			return
		}
		dir = path.Dir(dir)
	}
}

// Enqueue submits an Apply run for one already-persisted move job,
// deduplicated by ActionKeyFor.
func Enqueue(ctx context.Context, queue *jobs.Queue, moveJobID int64) (jobs.EnqueueResult, error) {
	data, err := json.Marshal(Payload{MoveJobID: moveJobID})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("moverjob: encode payload: %w", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type:      JobType,
		ActionKey: ActionKeyFor(moveJobID),
		Priority:  jobs.PriorityLow,
		Payload:   data,
	})
}
