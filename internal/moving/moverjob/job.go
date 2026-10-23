// Package moverjob is ADR-51's resumable Apply: it works through one
// library.MoveJobs job's own move_item rows, calling internal/moving/mover.ApplyOne
// per file, then RECORDs (library.Physical.Relocate), UNLINKs the old path,
// and TIDYs now-empty source directories -- the same pure-engine/persist-in-
// library/orchestrate-separately split internal/importing/importjob already
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

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// JobType is the registered type of one Apply run over an accepted move plan.
const JobType = jobs.Type("move.apply")

// Definition declares how the job behaves -- bulk, resumable, and never in
// front of anything interactive, the same reasoning importjob.Definition
// already gives.
var Definition = jobs.Definition{
	Name: "Moving files",
	Type: JobType, Pool: jobs.PoolMove, Mutating: true, MaxAttempts: 3,
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
// internal/moving/moverjob does not import internal/db/settings directly, the same
// separation internal/moving/mover and internal/importing/importjob already keep -- a
// cmd/solarr adapter bridges this to the real settings cache.
type RootFolders interface {
	RootFolder(rootFolderID int64) (RootFolder, bool)
}

// DrainCompletion closes ADR-51's own drain lifecycle: once a DRAIN job's
// files have all moved, the source root may hold nothing left, at which
// point drain_state should advance from DRAINING to DRAINED ("holds no
// library files and can be unmounted"). Optional -- nil leaves a DRAIN
// job's own drain_state exactly where starting it put it, the same gap
// live verification against a real container found (the state stayed
// DRAINING forever with no caller ever checking back). internal/moving/moverjob
// itself has no way to answer "is this root now empty" (that needs
// internal/db/library.Physical.PathsForRoot, which this package does not
// import), so a cmd/solarr adapter over internal/moving/drainjob answers it.
type DrainCompletion interface {
	CompleteDrainIfEmpty(ctx context.Context, rootFolderID, moveJobID int64) error
}

// Permissions answers a library's Set Permissions; ok is false when it is off.
type Permissions func(categoryID int64) (permissions mover.Permissions, ok bool, err error)

// Job runs one move.apply execution: claim the next pending item, apply it,
// record and unlink, repeat until the job's items are all terminal.
type Job struct {
	fsys            fsx.FS
	moveJobs        library.MoveJobs
	physical        library.Physical
	links           library.ObjectLinks
	roots           RootFolders
	drainCompletion DrainCompletion
	permissions     Permissions
	clock           clock.Clock
	logger          *slog.Logger
}

// Config is what a Job needs. Every field is required except Logger and
// DrainCompletion.
type Config struct {
	FS       fsx.FS
	MoveJobs library.MoveJobs
	Physical library.Physical
	// Links records the further path a copy to another library places.
	Links           library.ObjectLinks
	Roots           RootFolders
	DrainCompletion DrainCompletion
	// Permissions is optional; a rename run for one library applies it to what it moves.
	Permissions Permissions
	Clock       clock.Clock
	Logger      *slog.Logger
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{
		fsys: config.FS, moveJobs: config.MoveJobs, physical: config.Physical, links: config.Links,
		roots: config.Roots, drainCompletion: config.DrainCompletion, permissions: config.Permissions,
		clock: config.Clock, logger: logger,
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

	permissions := j.permissionsFor(moveJob)
	for {
		item, err := j.moveJobs.NextPendingItem(ctx, moveJob.ID)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return fmt.Errorf("moverjob: next pending item for job %d: %w", moveJob.ID, err)
		}

		if err := j.applyItem(ctx, item, permissions); err != nil {
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
	if err := j.moveJobs.Finish(ctx, moveJob.ID, library.MoveJobDone, j.clock.Now().Unix(), ""); err != nil {
		return err
	}

	if j.drainCompletion != nil && moveJob.Reason == string(mover.ReasonDrain) && moveJob.SourceRootID != nil {
		if err := j.drainCompletion.CompleteDrainIfEmpty(ctx, *moveJob.SourceRootID, moveJob.ID); err != nil {
			// The move itself already succeeded and is recorded -- failing
			// the whole job over a drain_state bookkeeping error would only
			// cause a pointless retry of already-finished work.
			j.logger.Warn("drain completion check failed",
				slog.Int64("move_job_id", moveJob.ID), slog.Int64("root_folder_id", *moveJob.SourceRootID),
				slog.String("error", err.Error()))
		}
	}
	return nil
}

// applyItem moves one file: locate its current path, copy/verify or rename
// it into place (mover.ApplyOne), RECORD the new location, UNLINK the old
// one, then TIDY now-empty source directories -- ADR-51's own
// copy-verify-repoint-unlink order (Invariant 77), repoint and unlink both
// the caller's job since ApplyOne imports no store.
func (j *Job) applyItem(ctx context.Context, item library.Item, permissions *mover.Permissions) error {
	paths, err := j.physical.PathsForObject(ctx, item.MediaObjectID)
	if err != nil {
		return fmt.Errorf("paths for media object %d: %w", item.MediaObjectID, err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("media object %d has no library_path left to move", item.MediaObjectID)
	}
	primary := paths[0]
	// A rescan after a folder was renamed by hand leaves the old row, missing, before the file's present one.
	for _, candidate := range paths {
		if candidate.IsPrimary == 1 && !candidate.MissingSince.Valid {
			primary = candidate
			break
		}
	}

	if primary.RootFolderID == item.DestRootID && primary.RelPath == item.DestRelPath {
		// Already at the destination: a prior attempt completed RECORD (and
		// likely UNLINK) before crashing on the bookkeeping below. The old
		// location is not recoverable from move_item, which does not
		// persist it -- what is left is the companions a crash may have cut short.
		j.moveCompanions(ctx, item, primary.RootFolderID, permissions)
		return j.moveJobs.SetItemState(ctx, item.MoveJobID, item.MediaObjectID, library.MoveItemDone, "")
	}

	sourceRoot, ok := j.roots.RootFolder(primary.RootFolderID)
	if !ok {
		return fmt.Errorf("source root %d has no known path", primary.RootFolderID)
	}
	if item.KeepSource {
		return j.copyItem(ctx, item, primary.RootFolderID, primary.RelPath, sourceRoot, permissions)
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
	if permissions != nil {
		destAbs := path.Join(destRoot.Path, item.DestRelPath)
		if err := mover.ApplyPermissions(j.fsys, destRoot.Path, destAbs, *permissions); err != nil {
			j.logger.Warn("permissions not set", slog.String("path", destAbs), slog.Any("error", err))
		}
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
	j.moveCompanions(ctx, item, primary.RootFolderID, permissions)
	j.tidy(path.Dir(sourceAbs), sourceRoot.Path)

	return j.moveJobs.SetItemState(ctx, item.MoveJobID, item.MediaObjectID, library.MoveItemDone, "")
}

// moveCompanions moves the item's subtitles, nfo and pictures after it from sourceRootID; one already moved or gone is
// done, one whose new name is taken stays (documentation/naming.md).
func (j *Job) moveCompanions(ctx context.Context, item library.Item, sourceRootID int64, permissions *mover.Permissions) {
	companions, err := j.moveJobs.Companions(ctx, item.MoveJobID, item.MediaObjectID)
	if err != nil {
		j.logger.Warn("companions not read", slog.Int64("media_object_id", item.MediaObjectID), slog.Any("error", err))
		return
	}
	sourceRoot, sourceKnown := j.roots.RootFolder(sourceRootID)
	destRoot, destKnown := j.roots.RootFolder(item.DestRootID)
	if len(companions) == 0 || !sourceKnown || !destKnown {
		return
	}
	rootPath := func(rootFolderID int64) (string, bool) {
		root, ok := j.roots.RootFolder(rootFolderID)
		return root.Path, ok
	}
	for _, companion := range companions {
		sourceAbs, destAbs := path.Join(sourceRoot.Path, companion.SourceRelPath), path.Join(destRoot.Path, companion.DestRelPath)
		info, err := j.fsys.Stat(sourceAbs)
		if err != nil || sourceAbs == destAbs {
			continue
		}
		if _, err := j.fsys.Stat(destAbs); err == nil {
			j.logger.Warn("companion left where it is: its new name is taken", slog.String("path", sourceAbs), slog.String("taken", destAbs))
			continue
		}
		move := mover.PlannedMove{
			MediaObjectID: item.MediaObjectID, SourceRootFolderID: sourceRootID, SourceRelPath: companion.SourceRelPath,
			DestRootFolderID: item.DestRootID, DestRelPath: companion.DestRelPath, SizeBytes: info.Size,
			SameFilesystem: sourceRoot.FSID != "" && sourceRoot.FSID == destRoot.FSID,
		}
		if err := mover.ApplyOne(j.fsys, move, rootPath); err != nil {
			j.logger.Warn("companion not moved", slog.String("path", sourceAbs), slog.Any("error", err))
			continue
		}
		if !move.SameFilesystem {
			if err := j.fsys.Remove(sourceAbs); err != nil && !errors.Is(err, fs.ErrNotExist) {
				j.logger.Warn("companion copied but its old name not removed", slog.String("path", sourceAbs), slog.Any("error", err))
			}
		}
		if permissions != nil {
			if err := mover.ApplyPermissions(j.fsys, destRoot.Path, destAbs, *permissions); err != nil {
				j.logger.Warn("permissions not set", slog.String("path", destAbs), slog.Any("error", err))
			}
		}
		j.tidy(path.Dir(sourceAbs), sourceRoot.Path)
	}
}

// permissionsFor is the job's library's Set Permissions, or nil when it has none or it is off.
func (j *Job) permissionsFor(moveJob library.Job) *mover.Permissions {
	if j.permissions == nil || moveJob.CategoryID == nil {
		return nil
	}
	permissions, ok, err := j.permissions(*moveJob.CategoryID)
	if err != nil {
		j.logger.Warn("permissions not resolved", slog.Int64("move_job_id", moveJob.ID), slog.Any("error", err))
		return nil
	}
	if !ok {
		return nil
	}
	return &permissions
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

// copyLadder is how a copy to another library shares the file's bytes: independence first, never a symlink (ADR-11).
var copyLadder = []mover.PlaceMethod{mover.PlaceReflink, mover.PlaceHardlink, mover.PlaceCopy}

// copyItem places the file at its destination and records it as a further path of the same media object, leaving the
// source where it is; its companions are placed beside it the same way.
func (j *Job) copyItem(
	ctx context.Context, item library.Item, sourceRootID int64, sourceRel string, sourceRoot RootFolder, permissions *mover.Permissions,
) error {
	destRoot, ok := j.roots.RootFolder(item.DestRootID)
	if !ok {
		return fmt.Errorf("destination root %d has no known path", item.DestRootID)
	}
	sourceAbs, destAbs := path.Join(sourceRoot.Path, sourceRel), path.Join(destRoot.Path, item.DestRelPath)
	if _, err := j.fsys.Stat(destAbs); err != nil {
		method, err := mover.PlaceOne(j.fsys, sourceAbs, destAbs, item.Bytes, copyLadder)
		if err != nil {
			return fmt.Errorf("copy %q: %w", sourceAbs, err)
		}
		if permissions != nil {
			if err := mover.ApplyPermissions(j.fsys, destRoot.Path, destAbs, *permissions); err != nil {
				j.logger.Warn("permissions not set", slog.String("path", destAbs), slog.Any("error", err))
			}
		}
		if err := j.links.AddLinkedPath(ctx, item.MediaObjectID, item.DestRootID, item.DestRelPath, linkKindOf(method)); err != nil {
			return errors.Join(fmt.Errorf("record the copy: %w", err), j.fsys.Remove(destAbs))
		}
	} else if err := j.links.AddLinkedPath(ctx, item.MediaObjectID, item.DestRootID, item.DestRelPath, library.LinkPrimary); err != nil {
		return fmt.Errorf("%q is taken: %w", destAbs, err)
	}
	j.copyCompanions(ctx, item, sourceRootID, sourceRoot, destRoot)
	return j.moveJobs.SetItemState(ctx, item.MoveJobID, item.MediaObjectID, library.MoveItemDone, "")
}

// copyCompanions places the item's subtitles, nfo and pictures beside its copy; one whose name is taken is left out.
func (j *Job) copyCompanions(ctx context.Context, item library.Item, sourceRootID int64, sourceRoot, destRoot RootFolder) {
	companions, err := j.moveJobs.Companions(ctx, item.MoveJobID, item.MediaObjectID)
	if err != nil {
		j.logger.Warn("companions not read", slog.Int64("media_object_id", item.MediaObjectID), slog.Any("error", err))
		return
	}
	for _, companion := range companions {
		sourceAbs, destAbs := path.Join(sourceRoot.Path, companion.SourceRelPath), path.Join(destRoot.Path, companion.DestRelPath)
		info, err := j.fsys.Stat(sourceAbs)
		if err != nil {
			continue
		}
		if _, err := j.fsys.Stat(destAbs); err == nil {
			continue
		}
		if _, err := mover.PlaceOne(j.fsys, sourceAbs, destAbs, info.Size, copyLadder); err != nil {
			j.logger.Warn("companion not copied", slog.String("path", sourceAbs), slog.Int64("source_root", sourceRootID), slog.Any("error", err))
		}
	}
}

// linkKindOf is how a placed copy is recorded: a link of its kind, or a primary copy of its own.
func linkKindOf(method mover.PlaceMethod) string {
	switch method {
	case mover.PlaceReflink:
		return library.LinkReflink
	case mover.PlaceHardlink:
		return library.LinkHardlink
	default:
		return library.LinkPrimary
	}
}
