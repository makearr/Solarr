// Package importjob is ADR-48's import.inspect job: it walks a folder resumably, records each unit, refuses the ones
// an exclusion poisons, and hands the rest to their library's media type (importing/batch), which identifies, decides
// and places them. ManualCommitter is the Import screen's own actions on what the walk held or could not yet place.
package importjob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	importbatch "github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/pack"
	"github.com/makearr/Solarr/internal/importing/unpack"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/events"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// JobType is the registered type of an import inspection run.
const JobType = jobs.Type("import.inspect")

// Definition declares how the job behaves: potentially long (a whole-library adoption) and
// resumable, in the import pool, whose one slot is ADR-3's fixed import concurrency. In
// maintenance a finished download waited behind every rescan (found 2026-09-28).
var Definition = jobs.Definition{
	Name: "Importing",
	Type: JobType, Pool: jobs.PoolImport, Mutating: true, MaxAttempts: 3,
}

// ActionKeyFor deduplicates concurrent submissions of the same source path.
// It does not by itself make a *retry* of one execution safe -- Job.Run's own
// resume-by-lookup does that (see resumeOrCreate).
func ActionKeyFor(sourcePath string) string {
	return fmt.Sprintf("%s:%s", JobType, sourcePath)
}

// ActionKeyOf keys a job by what it imports: a tracked download has its own key, so a watcher's import of the same
// folder cannot absorb it and strip the link to the download.
func ActionKeyOf(payload Payload) string {
	if payload.AcquisitionID != nil {
		return fmt.Sprintf("%s.acquisition:%d", JobType, *payload.AcquisitionID)
	}
	if payload.Preview {
		// A preview never absorbs a real import of the folder, and the other way round.
		return ActionKeyFor(payload.SourcePath) + ":preview"
	}
	return ActionKeyFor(payload.SourcePath)
}

// Payload is what an import job carries.
type Payload struct {
	SourcePath    string `json:"source_path"`
	Origin        string `json:"origin"`
	CategoryID    *int64 `json:"category_id,omitempty"`
	AcquisitionID *int64 `json:"acquisition_id,omitempty"`
	// Preview holds every unit for review instead of placing the confident
	// ones (Lidarr's interactive import, revision.md 7.9).
	Preview bool `json:"preview,omitempty"`
	// TransferLadder is a download folder's own order; empty keeps the Import screen's.
	TransferLadder []string `json:"transfer_ladder,omitempty"`
	// NoUnverified keeps a folder nothing identified out of the library; by default an Import-screen
	// run adds it as an unverified album (D6). Negative, so a resumed session keeps the default.
	NoUnverified bool `json:"no_unverified,omitempty"`
	// Only limits the import to these files, relative to SourcePath: the finished ones of a download that stalled.
	Only []string `json:"only,omitempty"`
	// BestMatch routes each unit of a session without a library to the library that fits it, instead of holding it.
	BestMatch bool `json:"best_match,omitempty"`
}

// Policy is what this job needs from settings.db -- asked fresh per run
// rather than cached, the same "ask, don't cache" discipline every other
// job's settings dependency already uses. No internal/db/settings import
// here (architecture §2) -- cmd/solarr/adapters bridges both methods
// over a real settings.Cache the same way it already bridges
// ImportAutoCommitConfidence.
type Policy interface {
	ImportAutoCommitConfidence() float64
	// RefusedByExclusion reports the ADR-53 refuse-action pattern that
	// matched name, if any -- the import gate's own exclusion-pattern check
	// (todo 4.15). An ignore-action match is never surfaced here: CLASSIFY
	// already treats an ignore-shaped file as junk by its own hardcoded
	// extension list (internal/importing/inspect), and nothing today asks
	// this for anything but the poison case.
	RefusedByExclusion(name, mediaType string, categoryID *int64) (pattern string, found bool)
}

// Job runs one import.inspect execution: walk, identify, shape, decide, record.
type Job struct {
	tracked        func(ctx context.Context, sourcePath string) (int64, bool)
	workspace      string
	recycleArchive func(ctx context.Context, categoryID *int64, sourcePath, file string) error
	archiveLimits  archive.Limits
	fsys           fsx.FS
	sessions       library.ImportSessions
	policy         Policy
	// packs is todo 4.11's own detection sink: detection needs no category, so it runs whether or not anything was placed.
	packs library.Packs
	// importers identify and place a batch, by media type; unassigned names the one a session without a library uses.
	importers   map[string]importbatch.Importer
	mediaTypeOf func(categoryID int64) string
	unassigned  string
	// libraries are the ones a Best match import may route to, asked fresh per cluster.
	libraries func() []Library
	// events is optional: nil means no progress is published.
	events *events.Bus
	// onCommitted is optional: told when a finished session placed anything, so new editions enrich now.
	onCommitted func(context.Context)
	clock       clock.Clock
	logger      *slog.Logger
}

// Config is what a Job needs. Every field is required except MediaTypeOf, Libraries, Workspace, ArchiveLimits, Events,
// OnCommitted and Logger.
type Config struct {
	FS       fsx.FS
	Sessions library.ImportSessions
	Policy   Policy
	Packs    library.Packs
	// Importers identify and place a batch by media type; MediaTypeOf names a library's type. Unassigned is the type
	// whose importer takes a session with no library yet, identifying without placing.
	Importers   map[string]importbatch.Importer
	MediaTypeOf func(categoryID int64) string
	Unassigned  string
	// Libraries are the ones a Best match import may route a unit to; nil routes none, so every unit is skipped.
	Libraries func() []Library
	// Tracked answers the download a folder belongs to while that download is still being followed; the folder watcher
	// leaves such a folder to the download's own import. Nil tracks nothing.
	Tracked func(ctx context.Context, sourcePath string) (acquisitionID int64, tracked bool)
	// RecycleArchive moves one archive file of an imported source to the recycle bin; nil keeps the archives in place.
	RecycleArchive func(ctx context.Context, categoryID *int64, sourcePath, file string) error
	// Workspace is where archives are opened (outside every library root); empty leaves archives unopened.
	Workspace string
	// ArchiveLimits bounds an expansion; zero is archive.DefaultLimits.
	ArchiveLimits archive.Limits
	Events        *events.Bus
	OnCommitted   func(context.Context)
	Clock         clock.Clock
	Logger        *slog.Logger
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{
		fsys: config.FS, sessions: config.Sessions, policy: config.Policy, packs: config.Packs,
		workspace: config.Workspace, recycleArchive: config.RecycleArchive, archiveLimits: config.ArchiveLimits,
		importers: config.Importers, mediaTypeOf: config.MediaTypeOf, unassigned: config.Unassigned, libraries: config.Libraries,
		tracked: config.Tracked, events: config.Events, onCommitted: config.OnCommitted, clock: config.Clock, logger: logger,
	}
}

// errCancelled stops Walk once RequestCancel marks the session; Run turns it
// into an ImportCancelled finish, never the executor's retryable failure.
var errCancelled = errors.New("importjob: session cancelled")

// Run walks payload.SourcePath, identifying and recording each unit it finds.
// Retried by the executor on a plain (unlabelled, so retryable) error; a
// retry resumes rather than duplicating a session's own rows, since
// resumeOrCreate looks for one already in progress for this exact source
// path before starting a new one.
func (j *Job) Run(ctx context.Context, job jobs.Job) error {
	var payload Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Terminalf("importjob.payload", string(job.Type), err)
	}
	if payload.Origin == library.ImportOriginWatched && j.tracked != nil {
		if id, tracked := j.tracked(ctx, payload.SourcePath); tracked {
			j.logger.Info("watched folder left to its tracked download",
				slog.String("source_path", payload.SourcePath), slog.Int64("acquisition_id", id))
			return nil
		}
	}
	// An archive is a folder (ADR-49): its contents are opened into a workspace and imported as one, then the
	// source's own loose files, if it has any.
	// A download's finished files are imported as they are: its other archive parts are missing or half written.
	if len(payload.Only) > 0 {
		return j.runSession(ctx, job, payload)
	}
	if workspace, opened := j.unpack(ctx, payload.SourcePath); opened > 0 {
		inner := payload
		inner.SourcePath = workspace
		if err := j.runSession(ctx, job, inner); err != nil {
			return err
		}
		// Units held for review still read their files from the workspace; the weekly sweep clears it later.
		if !j.holdsQueuedUnits(ctx, workspace) {
			if err := unpack.RemoveTree(j.fsys, workspace); err != nil {
				j.logger.Warn("import workspace not removed", slog.String("workspace", workspace), slog.Any("error", err))
			} else if payload.Origin == library.ImportOriginManual && !payload.Preview && j.placedAll(ctx, workspace) {
				j.retireArchives(ctx, payload.SourcePath, payload.CategoryID)
			}
		}
		if !j.hasMedia(ctx, payload.SourcePath) {
			return nil
		}
	}
	return j.runSession(ctx, job, payload)
}

// holdsQueuedUnits reports whether the newest finished session of a source still has units waiting for a person.
func (j *Job) holdsQueuedUnits(ctx context.Context, sourcePath string) bool {
	sessions, err := j.sessions.ListBySourcePath(ctx, sourcePath, library.ImportDone)
	if err != nil {
		return true
	}
	for _, session := range sessions {
		units, err := j.sessions.ListUnits(ctx, session.ID)
		if err != nil {
			return true
		}
		for _, unit := range units {
			if unit.Decision == library.UnitQueue && unit.WorkID == nil {
				return true
			}
		}
	}
	return false
}

// placedAll reports whether the newest finished session of a source placed every unit it held.
func (j *Job) placedAll(ctx context.Context, sourcePath string) bool {
	sessions, err := j.sessions.ListBySourcePath(ctx, sourcePath, library.ImportDone)
	if err != nil || len(sessions) == 0 {
		return false
	}
	units, err := j.sessions.ListUnits(ctx, sessions[0].ID)
	if err != nil || len(units) == 0 {
		return false
	}
	for _, unit := range units {
		if unit.WorkID == nil {
			return false
		}
	}
	return true
}

// retireArchives sends the archives of a folder the user pointed at to the recycle bin once their contents are in the
// library; a source that cannot be written to (a read-only download mount) simply keeps them.
func (j *Job) retireArchives(ctx context.Context, sourcePath string, categoryID *int64) {
	if j.recycleArchive == nil {
		return
	}
	files, err := unpack.ArchiveFiles(ctx, j.fsys, sourcePath)
	if err != nil {
		j.logger.Info("source archives kept in place", slog.String("source_path", sourcePath), slog.Any("error", err))
		return
	}
	recycled := 0
	for _, file := range files {
		if err := j.recycleArchive(ctx, categoryID, sourcePath, file); err != nil {
			j.logger.Info("source archive kept in place", slog.String("file", file), slog.Any("error", err))
			continue
		}
		recycled++
	}
	j.logger.Info("source archives recycled", slog.String("source_path", sourcePath), slog.Int("files", recycled))
}

// workspaceLifetime is how long a workspace kept for a review waits before it is swept.
const workspaceLifetime = 7 * 24 * time.Hour

// sweepWorkspaces removes the workspaces older than workspaceLifetime.
func (j *Job) sweepWorkspaces() {
	if _, err := unpack.SweepOlderThan(j.fsys, j.workspace, j.clock.Now().Add(-workspaceLifetime)); err != nil {
		j.logger.Warn("old import workspaces not removed", slog.String("workspace", j.workspace), slog.Any("error", err))
	}
}

// unpack opens the source's archives into a workspace of its own; none opened, or a failure, leaves the source to
// be imported as it is. A workspace is never itself unpacked again.
func (j *Job) unpack(ctx context.Context, sourcePath string) (workspace string, opened int) {
	if j.workspace == "" || fsx.Within(j.workspace, sourcePath) {
		return "", 0
	}
	j.sweepWorkspaces()
	sum := sha256.Sum256([]byte(sourcePath))
	// Named after the source so a workspace session reads as what it came from, and the hash keeps two sources apart.
	workspace = path.Join(j.workspace, fsx.SanitiseComponent(path.Base(sourcePath))+"-"+hex.EncodeToString(sum[:])[:8])
	if j.holdsQueuedUnits(ctx, workspace) {
		// Held units still read their files from here: opening the archives again would wipe them.
		j.logger.Info("source waits for its held units; archives not opened again", slog.String("source_path", sourcePath))
		return workspace, 0
	}
	if err := unpack.RemoveTree(j.fsys, workspace); err != nil {
		j.logger.Warn("import workspace not cleared", slog.String("workspace", workspace), slog.Any("error", err))
		return "", 0
	}
	opened, err := unpack.Expand(ctx, j.fsys, sourcePath, workspace, j.limits())
	if err != nil {
		j.logger.Warn("archives could not be opened", slog.String("source_path", sourcePath), slog.Any("error", err))
		_ = unpack.RemoveTree(j.fsys, workspace)
		return "", 0
	}
	if opened > 0 {
		j.logger.Info("archives opened for import", slog.String("source_path", sourcePath), slog.Int("archives", opened))
	}
	return workspace, opened
}

func (j *Job) limits() archive.Limits {
	if j.archiveLimits == (archive.Limits{}) {
		return archive.DefaultLimits()
	}
	return j.archiveLimits
}

// hasMedia reports whether the source holds media files of its own, beside its archives.
func (j *Job) hasMedia(ctx context.Context, sourcePath string) bool {
	files, err := inspect.ListFiles(ctx, j.fsys, sourcePath, 0)
	if err != nil {
		return true
	}
	for _, file := range files {
		if file.Class == inspect.ClassMedia {
			return true
		}
	}
	return false
}

// runSession imports one source folder as one session.
func (j *Job) runSession(ctx context.Context, job jobs.Job, payload Payload) error {
	sessionID, cursor, threshold, cancelling, err := j.resumeOrCreate(ctx, &payload)
	if err != nil {
		return err
	}
	if cancelling {
		// Caught mid-cancel by a crash or retry -- honour it now rather than
		// silently starting a duplicate session over the same path.
		j.publishProgress(ctx, sessionID, importPhaseCancelled, "")
		return j.sessions.Finish(ctx, sessionID, library.ImportCancelled, j.clock.Now().Unix(), "")
	}
	j.estimateOnce(ctx, sessionID, payload.SourcePath)
	j.publishProgress(ctx, sessionID, importPhaseWalking, "")
	j.reportProgress(ctx, sessionID, job.Progress)

	result, err := inspect.NewWalker().Walk(ctx, j.fsys, payload.SourcePath, inspect.WalkState{Cursor: cursor},
		j.walkOptions(payload),
		func(ctx context.Context, batch []inspect.Unit) error {
			err := j.commitCluster(ctx, sessionID, payload.SourcePath, threshold, payload.Preview,
				unverifiedWanted(payload), payload.BestMatch, payload.TransferLadder, batch)
			j.reportProgress(ctx, sessionID, job.Progress)
			return err
		})
	if errors.Is(err, errCancelled) {
		j.logger.Info("import inspection cancelled",
			slog.Int64("session_id", sessionID), slog.String("source_path", payload.SourcePath))
		j.publishProgress(ctx, sessionID, importPhaseCancelled, "")
		return j.sessions.Finish(ctx, sessionID, library.ImportCancelled, j.clock.Now().Unix(), "")
	}
	if err != nil {
		return j.failSessionIfFinal(ctx, job, sessionID, err)
	}
	j.logger.Info("import inspection finished",
		slog.Int64("session_id", sessionID), slog.String("source_path", payload.SourcePath),
		slog.Int("units_emitted", result.UnitsEmitted), slog.Bool("completed", result.Completed))

	j.detectPacks(ctx, sessionID, payload.SourcePath, payload.Origin)

	if err := j.sessions.Finish(ctx, sessionID, library.ImportDone, j.clock.Now().Unix(), ""); err != nil {
		return err
	}
	j.publishProgress(ctx, sessionID, importPhaseDone, "")
	j.notifyCommitted(ctx, sessionID)
	return nil
}

// failSessionIfFinal closes the session as failed when the executor will not run this job again, so the Import page
// and the download's poll see the failure instead of an inspection that never ends.
func (j *Job) failSessionIfFinal(ctx context.Context, job jobs.Job, sessionID int64, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	last := job.MaxAttempts > 0 && job.Attempts+1 >= job.MaxAttempts
	if !last && jobs.Classify(cause) == jobs.ClassRetryable {
		return cause
	}
	if err := j.sessions.Finish(context.WithoutCancel(ctx), sessionID, library.ImportFailed, j.clock.Now().Unix(), cause.Error()); err != nil {
		j.logger.Warn("import session not closed as failed", slog.Int64("session_id", sessionID), slog.Any("error", err))
	}
	j.publishProgress(context.WithoutCancel(ctx), sessionID, importPhaseFailed, "")
	return cause
}

func (j *Job) notifyCommitted(ctx context.Context, sessionID int64) {
	if j.onCommitted == nil {
		return
	}
	session, err := j.sessions.Get(ctx, sessionID)
	if err != nil {
		j.logger.Debug("could not read session after import", slog.Any("error", err))
		return
	}
	if session.UnitsCommitted > 0 {
		j.onCommitted(ctx)
	}
}

// resumeOrCreate finds a not-yet-finished session for this exact source path and resumes it with
// the threshold and preview it was started with (answered 2026-09-24), or starts a new one; one
// found CANCELLING is reported as such rather than resumed.
func (j *Job) resumeOrCreate(
	ctx context.Context, payload *Payload,
) (sessionID int64, cursor string, threshold float64, cancelling bool, err error) {
	for _, state := range []string{library.ImportInspecting, library.ImportCancelling} {
		// Dismissal is a display concern, so resume looks past it.
		existing, err := j.sessions.ListBySourcePath(ctx, payload.SourcePath, state)
		if err != nil {
			return 0, "", 0, false, fmt.Errorf("importjob: list %s sessions: %w", state, err)
		}
		for _, session := range existing {
			if !sameAcquisition(session.AcquisitionID, payload.AcquisitionID) {
				// A re-grab lands in the same folder: resuming the old session would skip its units and keep its acquisition.
				finished := library.ImportFailed
				if state == library.ImportCancelling {
					finished = library.ImportCancelled
				}
				if err := j.sessions.Finish(ctx, session.ID, finished, j.clock.Now().Unix(), "superseded by a newer import of the same folder"); err != nil {
					return 0, "", 0, false, fmt.Errorf("importjob: close superseded session %d: %w", session.ID, err)
				}
				continue
			}
			if state == library.ImportCancelling {
				return session.ID, "", session.Threshold, true, nil
			}
			units, err := j.sessions.ListUnits(ctx, session.ID)
			if err != nil {
				return 0, "", 0, false, fmt.Errorf("importjob: list units for session %d: %w", session.ID, err)
			}
			if len(units) > 0 {
				cursor = units[len(units)-1].RelPath
			}
			payload.Preview = session.Preview
			return session.ID, cursor, session.Threshold, false, nil
		}
	}

	threshold = j.policy.ImportAutoCommitConfidence()
	id, err := j.sessions.Create(ctx, library.NewSession{
		SourcePath: payload.SourcePath, Origin: payload.Origin,
		AcquisitionID: payload.AcquisitionID, CategoryID: payload.CategoryID,
		Threshold: threshold, StartedAt: j.clock.Now().Unix(), Preview: payload.Preview,
	})
	if err != nil {
		return 0, "", 0, false, fmt.Errorf("importjob: create session: %w", err)
	}
	return id, "", threshold, false, nil
}

// CloseOrphanedSessions finishes the sessions that are still walking or cancelling with no job left to carry them on
// (a crash on the last attempt, or an older build that never closed them); it returns how many it closed.
func CloseOrphanedSessions(
	ctx context.Context, sessions library.ImportSessions, hasActiveJob func(ctx context.Context, actionKey string) (bool, error), at time.Time,
) (int, error) {
	closed := 0
	for _, state := range []string{library.ImportInspecting, library.ImportCancelling} {
		stuck, err := sessions.List(ctx, state, true)
		if err != nil {
			return closed, fmt.Errorf("importjob: list %s sessions: %w", state, err)
		}
		for _, session := range stuck {
			active, err := hasActiveJob(ctx, ActionKeyOf(Payload{SourcePath: session.SourcePath, AcquisitionID: session.AcquisitionID, Preview: session.Preview}))
			if err != nil {
				return closed, err
			}
			if active {
				continue
			}
			finished, reason := library.ImportFailed, "the import was interrupted and not resumed"
			if state == library.ImportCancelling {
				finished, reason = library.ImportCancelled, ""
			}
			if err := sessions.Finish(ctx, session.ID, finished, at.Unix(), reason); err != nil {
				return closed, fmt.Errorf("importjob: close orphaned session %d: %w", session.ID, err)
			}
			closed++
		}
	}
	return closed, nil
}

func sameAcquisition(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// estimateOnce runs inspect.EstimateUnits and persists it, the first time
// only -- a resumed session already carrying an estimate skips straight past.
// Failure is logged and otherwise ignored, the same "a courtesy, never a
// reason to fail the run" rule publishProgress already applies: a wrong or
// missing ETA is a worse UI than a stalled import.
func (j *Job) estimateOnce(ctx context.Context, sessionID int64, sourcePath string) {
	session, err := j.sessions.Get(ctx, sessionID)
	if err != nil {
		j.logger.Warn("could not read session before estimating units", slog.Any("error", err))
		return
	}
	if session.UnitsEstimated != nil {
		return
	}
	estimate, err := inspect.EstimateUnits(ctx, j.fsys, sourcePath)
	if err != nil {
		j.logger.Warn("import unit estimate failed", slog.String("source_path", sourcePath), slog.Any("error", err))
		return
	}
	if err := j.sessions.SetUnitsEstimated(ctx, sessionID, int64(estimate)); err != nil {
		j.logger.Warn("could not record import unit estimate", slog.Any("error", err))
	}
}

// detectPacks runs todo 4.11's own detection once a walk finishes cleanly
// (Run's own caller: never on a cancelled or still-interrupted run) --
// pack.Detect is pure and in-memory (ADR-23); this is where the result is
// persisted. Adoption sessions are skipped: an existing, already-organised
// library naturally has many same-artist sibling directories that are an
// established catalogue, not "one source, many works" in ADR-50's own sense
// (private/questions.md). Failure is logged and otherwise ignored, the same
// "a courtesy, never a reason to fail the run" rule estimateOnce already
// follows -- a missed pack is a worse UI than a stalled import, and every
// member unit is still fully imported on its own regardless.
func (j *Job) detectPacks(ctx context.Context, sessionID int64, sourcePath, origin string) {
	if origin == library.ImportOriginAdoption {
		return
	}
	units, err := j.sessions.ListUnits(ctx, sessionID)
	if err != nil {
		j.logger.Warn("could not list units for pack detection", slog.Int64("session_id", sessionID), slog.Any("error", err))
		return
	}
	candidates := make([]pack.Candidate, len(units))
	byID := make(map[int64]library.Unit, len(units))
	for i, unit := range units {
		candidates[i] = pack.Candidate{UnitID: unit.ID, RelPath: unit.RelPath}
		byID[unit.ID] = unit
	}

	for _, group := range pack.Detect(path.Base(sourcePath), candidates) {
		items := make([]library.NewPackItem, len(group.Members))
		for i, member := range group.Members {
			unit := byID[member.UnitID]
			items[i] = library.NewPackItem{
				WorkID: unit.WorkID, EditionID: unit.EditionID, ImportUnitID: unit.ID,
				State: packItemStateFor(unit),
			}
		}
		if _, err := j.packs.Create(ctx, group.Kind, group.DetectedFrom, group.Confidence, group.Title,
			sessionID, j.clock.Now().Unix(), items,
		); err != nil {
			j.logger.Warn("could not record detected pack",
				slog.String("title", group.Title), slog.Int("members", len(group.Members)), slog.Any("error", err))
		}
	}
}

// packItemStateFor maps one unit's own decision (and whether it actually
// placed, for COMMIT) onto pack_item's narrower vocabulary -- COMMIT with no
// work_id yet (no category on the session at WALK time) reads as QUEUED,
// same as a real QUEUE decision: neither has resolved into the library yet,
// and ManualCommitter's own ResolveItem is what moves either one to RESOLVED
// once it does.
func packItemStateFor(unit library.Unit) string {
	switch {
	case unit.Decision == library.UnitCommit && unit.WorkID != nil:
		return library.PackItemResolved
	case unit.Decision == library.UnitCommit, unit.Decision == library.UnitQueue:
		return library.PackItemQueued
	case unit.Decision == library.UnitSkip:
		return library.PackItemSkipped
	default:
		return library.PackItemFailed
	}
}

// poisonedByExclusion checks every file a unit carries, Media and Other alike (a ".exe" beside audio lands in
// Other), against ADR-53's refuse action; "" for a unit not poisoned.
func (j *Job) poisonedByExclusion(unit inspect.Unit, mediaType string, categoryID *int64) string {
	for _, files := range [][]inspect.File{unit.Media, unit.Other} {
		for _, f := range files {
			if pattern, found := j.policy.RefusedByExclusion(path.Base(f.RelPath), mediaType, categoryID); found {
				return fmt.Sprintf("%s: %q matched %s", model.ReasonBlockedPattern, pattern, f.RelPath)
			}
		}
	}
	return ""
}

// commitCluster records one walked cluster's units, refuses those carrying an excluded file, and hands the rest to
// their media type to identify, decide and place. The session's category is read here rather than taken from the
// payload: a library assigned while the session walks places every cluster after it.
func (j *Job) commitCluster(
	ctx context.Context, sessionID int64, sourcePath string, threshold float64, preview, unverified, bestMatch bool,
	ladder []string, batch []inspect.Unit,
) error {
	session, err := j.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("importjob: check session %d: %w", sessionID, err)
	}
	if session.State == library.ImportCancelling {
		return errCancelled
	}
	var routes []routed
	if bestMatch && session.CategoryID == nil {
		routes = j.routeByFit(j.routableLibraries(), batch)
	} else {
		routes = j.routeBySession(session.CategoryID, batch)
	}

	newUnits := make([]library.NewUnit, len(routes))
	for i, route := range routes {
		if route.importer == nil && route.skip == "" {
			return fmt.Errorf("importjob: %q libraries have no import", route.mediaType)
		}
		evidenceJSON, err := json.Marshal(route.unit.Evidence)
		if err != nil {
			return fmt.Errorf("importjob: encode evidence for %s: %w", route.unit.Directory, err)
		}
		var bytes int64
		for _, f := range route.unit.Media {
			bytes += f.SizeBytes
		}
		newUnits[i] = library.NewUnit{
			RelPath: route.unit.Directory, Shape: string(inspect.ShapeOf(route.unit)),
			FileCount: int64(len(route.unit.Media)), Bytes: bytes, EvidenceJSON: string(evidenceJSON), CategoryID: route.own,
		}
	}
	ids, err := j.sessions.AddUnits(ctx, sessionID, newUnits)
	if err != nil {
		return fmt.Errorf("importjob: add units: %w", err)
	}

	// A refused file poisons its whole unit however confidently it would have resolved, so it never reaches IDENTIFY.
	decisions := make([]library.UnitDecision, len(routes))
	var groups []importGroup
	for i, route := range routes {
		reason := route.skip
		if reason == "" {
			reason = j.poisonedByExclusion(route.unit, route.mediaType, route.category)
		}
		if reason != "" {
			decisions[i] = library.UnitDecision{UnitID: ids[i], Decision: library.UnitSkip, Reason: reason}
			continue
		}
		groups = addToGroup(groups, route, i)
	}
	// What needs no importer is recorded at once; each group is recorded the moment its files are placed, so a later
	// group failing cannot make the session forget a unit that is already in the library.
	var skipped []library.UnitDecision
	for _, decision := range decisions {
		if decision.UnitID != 0 {
			skipped = append(skipped, decision)
		}
	}
	if err := j.sessions.SetUnitDecisions(ctx, sessionID, skipped); err != nil {
		return fmt.Errorf("importjob: record decisions: %w", err)
	}
	recordedAny := len(skipped) > 0
	for position, group := range groups {
		request := importbatch.Request{
			SessionID: sessionID, AcquisitionID: session.AcquisitionID, SourcePath: sourcePath,
			Preview: preview, Ladder: ladder, Threshold: threshold, AddUnidentified: unverified,
		}
		if group.category != nil {
			request.CategoryID = *group.category
		}
		for _, i := range group.members {
			request.Units = append(request.Units, routes[i].unit)
			request.UnitIDs = append(request.UnitIDs, ids[i])
		}
		result, err := group.importer.ImportBatch(ctx, request)
		if err != nil {
			j.abandonUnrecorded(ctx, sessionID, ids, groups[position:], recordedAny, err)
			return fmt.Errorf("importjob: import batch: %w", err)
		}
		groupDecisions := make([]library.UnitDecision, len(group.members))
		for k := range group.members {
			groupDecisions[k] = result.Decisions[k]
		}
		if err := j.sessions.SetUnitDecisions(ctx, sessionID, groupDecisions); err != nil {
			return fmt.Errorf("importjob: record decisions: %w", err)
		}
		recordedAny = true
		for k, i := range group.members {
			if result.Placements[k] == nil {
				continue
			}
			// A placement that is not recorded is a unit in the library the session cannot revert: not a thing to log and go on.
			if err := j.sessions.RecordPlacement(ctx, ids[i], *result.Placements[k]); err != nil {
				return fmt.Errorf("importjob: record placement of unit %d: %w", ids[i], err)
			}
		}
	}
	j.publishProgress(ctx, sessionID, importPhaseWalking, batch[len(batch)-1].Directory)
	return nil
}

// abandonUnrecorded handles a group whose import failed. When nothing of the cluster is recorded yet, every unit row goes so
// the retry inspects the cluster again; when an earlier group is already recorded those rows must stay (their files are
// placed), and the units of the failed and later groups are held for a person, since a retry would skip past them.
func (j *Job) abandonUnrecorded(
	ctx context.Context, sessionID int64, ids []int64, failed []importGroup, recordedAny bool, cause error,
) {
	if !recordedAny {
		if err := j.sessions.DeleteUnits(ctx, sessionID, ids); err != nil {
			j.logger.Warn("import: could not forget an unfinished cluster", slog.Int64("session_id", sessionID), slog.Any("error", err))
		}
		return
	}
	var held []library.UnitDecision
	for _, group := range failed {
		for _, i := range group.members {
			held = append(held, library.UnitDecision{
				UnitID: ids[i], Decision: library.UnitQueue, Reason: "the import stopped before this unit: " + cause.Error(),
			})
		}
	}
	if err := j.sessions.SetUnitDecisions(ctx, sessionID, held); err != nil {
		j.logger.Warn("import: could not hold the units a failed group left", slog.Int64("session_id", sessionID), slog.Any("error", err))
	}
}

// importGroup is the units of a cluster one importer places into one library, as positions in the cluster.
type importGroup struct {
	mediaType string
	importer  importbatch.Importer
	category  *int64
	members   []int
}

// addToGroup puts a unit into the group of its type and library, in the order the groups first appear.
func addToGroup(groups []importGroup, route routed, position int) []importGroup {
	for k := range groups {
		if groups[k].mediaType == route.mediaType && sameCategory(groups[k].category, route.category) {
			groups[k].members = append(groups[k].members, position)
			return groups
		}
	}
	return append(groups, importGroup{mediaType: route.mediaType, importer: route.importer, category: route.category, members: []int{position}})
}

func sameCategory(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// routableLibraries are the libraries a Best match import may route to now.
func (j *Job) routableLibraries() []Library {
	if j.libraries == nil {
		return nil
	}
	return j.libraries()
}

// unverifiedWanted is D6's rule: only a run started from the Import screen, not a preview, and not
// switched off, adds what nothing identified. A finished download was grabbed for a known album.
func unverifiedWanted(payload Payload) bool {
	return payload.Origin == library.ImportOriginManual && !payload.Preview && !payload.NoUnverified
}

// Import progress phases (todo 4.21's own SSE shape).
const (
	importPhaseWalking   = "walking"
	importPhaseDone      = "done"
	importPhaseCancelled = "cancelled"
	importPhaseFailed    = "failed"
)

// ImportProgress is one session's live update, published on topic "job",
// entity "import_session:<id>" -- phase, the per-decision breakdown and the unit last processed.
type ImportProgress struct {
	SessionID      int64  `json:"session_id"`
	Phase          string `json:"phase"`
	CurrentUnit    string `json:"current_unit,omitempty"`
	UnitsTotal     int64  `json:"units_total"`
	UnitsCommitted int64  `json:"units_committed"`
	UnitsQueued    int64  `json:"units_queued"`
	UnitsSkipped   int64  `json:"units_skipped"`
	UnitsFailed    int64  `json:"units_failed"`
	BytesImported  int64  `json:"bytes_imported"`
}

// publishProgress re-reads and publishes the session; silent on any failure
// or without an events.Bus -- a courtesy, never a reason to fail the run.
// The session's own category_id is read fresh here too, not passed in --
// commitCluster's own doc comment gives the reason (todo 4.9's "assign a
// category later" can change it mid-run).
func (j *Job) publishProgress(ctx context.Context, sessionID int64, phase, currentUnit string) {
	publishSessionProgress(ctx, j.events, j.sessions, j.logger, sessionID, phase, currentUnit)
}

// publishSessionProgress is publishProgress for any job that works on a session.
func publishSessionProgress(
	ctx context.Context, bus *events.Bus, sessions library.ImportSessions, logger *slog.Logger,
	sessionID int64, phase, currentUnit string,
) {
	if bus == nil {
		return
	}
	session, err := sessions.Get(ctx, sessionID)
	if err != nil {
		logger.Debug("could not read session for a progress event", slog.Any("error", err))
		return
	}
	var category int64
	if session.CategoryID != nil {
		category = *session.CategoryID
	}
	bus.Publish(events.Event{
		Topic: events.TopicJob, Entity: fmt.Sprintf("import_session:%d", sessionID), CategoryID: category,
		Kind: "progress",
		Data: ImportProgress{
			SessionID: sessionID, Phase: phase, CurrentUnit: currentUnit,
			UnitsTotal: session.UnitsTotal, UnitsCommitted: session.UnitsCommitted,
			UnitsQueued: session.UnitsQueued, UnitsSkipped: session.UnitsSkipped,
			UnitsFailed: session.UnitsFailed, BytesImported: session.BytesImported,
		},
	})
}

// reportProgress gives the status bar the units handled of the estimated total; best-effort.
func (j *Job) reportProgress(ctx context.Context, sessionID int64, progress *jobs.Progress) {
	session, err := j.sessions.Get(ctx, sessionID)
	if err != nil {
		return
	}
	handled := session.UnitsCommitted + session.UnitsQueued + session.UnitsSkipped + session.UnitsFailed
	progress.Set(handled, session.UnitsTotal)
}

// Enqueue submits an import inspection of one source path, deduplicated by
// ActionKeyFor.
func Enqueue(ctx context.Context, queue *jobs.Queue, payload Payload) (jobs.EnqueueResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("importjob: encode payload: %w", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type:      JobType,
		ActionKey: ActionKeyOf(payload),
		Priority:  jobs.PriorityLow,
		Payload:   data,
	})
}

// importerFor is the importer of a category's media type, or of the unassigned type for a session with no library.
func (j *Job) importerFor(categoryID *int64) (importbatch.Importer, string) {
	mediaType := j.unassigned
	if categoryID != nil && j.mediaTypeOf != nil {
		mediaType = j.mediaTypeOf(*categoryID)
	}
	return j.importers[mediaType], mediaType
}

// walkOptions are the inspection options of a category's media type, or the loosest of every library's for a Best
// match import: the size floor, whether ebooks count, and the files asked for.
func (j *Job) walkOptions(payload Payload) inspect.WalkOptions {
	var options inspect.WalkOptions
	if payload.BestMatch && payload.CategoryID == nil {
		options.MediaSizeFloorBytes, options.Books = j.loosestWalk(j.routableLibraries())
	} else if importer, _ := j.importerFor(payload.CategoryID); importer != nil {
		options.MediaSizeFloorBytes, options.Books = typeWalk(importer)
	}
	if len(payload.Only) > 0 {
		options.Only = make(map[string]bool, len(payload.Only))
		for _, file := range payload.Only {
			options.Only[file] = true
		}
	}
	return options
}
