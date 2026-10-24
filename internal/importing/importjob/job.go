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
	workspace     string
	archiveLimits archive.Limits
	fsys          fsx.FS
	sessions      library.ImportSessions
	policy        Policy
	// packs is todo 4.11's own detection sink: detection needs no category, so it runs whether or not anything was placed.
	packs library.Packs
	// importers identify and place a batch, by media type; unassigned names the one a session without a library uses.
	importers   map[string]importbatch.Importer
	mediaTypeOf func(categoryID int64) string
	unassigned  string
	// events is optional: nil means no progress is published.
	events *events.Bus
	// onCommitted is optional: told when a finished session placed anything, so new editions enrich now.
	onCommitted func(context.Context)
	clock       clock.Clock
	logger      *slog.Logger
}

// Config is what a Job needs. Every field is required except MediaTypeOf, Workspace, ArchiveLimits, Events,
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
		workspace: config.Workspace, archiveLimits: config.ArchiveLimits,
		importers: config.Importers, mediaTypeOf: config.MediaTypeOf, unassigned: config.Unassigned,
		events: config.Events, onCommitted: config.OnCommitted, clock: config.Clock, logger: logger,
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
				j.retireArchives(ctx, payload.SourcePath)
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
	sessions, err := j.sessions.List(ctx, library.ImportDone, true)
	if err != nil {
		return true
	}
	for _, session := range sessions {
		if session.SourcePath != sourcePath {
			continue
		}
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
	sessions, err := j.sessions.List(ctx, library.ImportDone, true)
	if err != nil {
		return false
	}
	for _, session := range sessions {
		if session.SourcePath != sourcePath {
			continue
		}
		units, err := j.sessions.ListUnits(ctx, session.ID)
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
	return false
}

// retireArchives moves a folder the user pointed at past its archives once their contents are in the library; a
// source that cannot be written to (a read-only download mount) simply keeps them.
func (j *Job) retireArchives(ctx context.Context, sourcePath string) {
	if _, err := unpack.SweepRecycled(j.fsys, sourcePath, j.clock.Now().Add(-workspaceLifetime)); err != nil {
		j.logger.Warn("old recycled archives not removed", slog.String("source_path", sourcePath), slog.Any("error", err))
	}
	moved, err := unpack.Retire(ctx, j.fsys, sourcePath)
	if err != nil {
		j.logger.Info("source archives kept in place", slog.String("source_path", sourcePath), slog.Any("error", err))
		return
	}
	j.logger.Info("source archives recycled", slog.String("source_path", sourcePath), slog.Int("archives", moved))
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
		j.walkOptions(payload.CategoryID, payload.Only),
		func(ctx context.Context, batch []inspect.Unit) error {
			err := j.commitCluster(ctx, sessionID, payload.SourcePath, threshold, payload.Preview,
				unverifiedWanted(payload), payload.TransferLadder, batch)
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
		return err
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
		existing, err := j.sessions.List(ctx, state, true)
		if err != nil {
			return 0, "", 0, false, fmt.Errorf("importjob: list %s sessions: %w", state, err)
		}
		for _, session := range existing {
			if session.SourcePath != payload.SourcePath {
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
// Other), against ADR-53's refuse action; aligned with batch, "" for a unit not poisoned.
func (j *Job) poisonedByExclusion(batch []inspect.Unit, mediaType string, categoryID *int64) []string {
	reasons := make([]string, len(batch))
	for i, unit := range batch {
		for _, files := range [][]inspect.File{unit.Media, unit.Other} {
			for _, f := range files {
				if pattern, found := j.policy.RefusedByExclusion(path.Base(f.RelPath), mediaType, categoryID); found {
					reasons[i] = fmt.Sprintf("%s: %q matched %s", model.ReasonBlockedPattern, pattern, f.RelPath)
					break
				}
			}
			if reasons[i] != "" {
				break
			}
		}
	}
	return reasons
}

// commitCluster records one walked cluster's units, refuses those carrying an excluded file, and hands the rest to
// their media type to identify, decide and place. The session's category is read here rather than taken from the
// payload: a library assigned while the session walks places every cluster after it.
func (j *Job) commitCluster(
	ctx context.Context, sessionID int64, sourcePath string, threshold float64, preview, unverified bool,
	ladder []string, batch []inspect.Unit,
) error {
	session, err := j.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("importjob: check session %d: %w", sessionID, err)
	}
	if session.State == library.ImportCancelling {
		return errCancelled
	}
	importer, mediaType := j.importerFor(session.CategoryID)
	if importer == nil {
		return fmt.Errorf("importjob: %q libraries have no import", mediaType)
	}

	newUnits := make([]library.NewUnit, len(batch))
	for i, unit := range batch {
		evidenceJSON, err := json.Marshal(unit.Evidence)
		if err != nil {
			return fmt.Errorf("importjob: encode evidence for %s: %w", unit.Directory, err)
		}
		var bytes int64
		for _, f := range unit.Media {
			bytes += f.SizeBytes
		}
		newUnits[i] = library.NewUnit{
			RelPath: unit.Directory, Shape: string(inspect.ShapeOf(unit)),
			FileCount: int64(len(unit.Media)), Bytes: bytes, EvidenceJSON: string(evidenceJSON),
		}
	}
	ids, err := j.sessions.AddUnits(ctx, sessionID, newUnits)
	if err != nil {
		return fmt.Errorf("importjob: add units: %w", err)
	}

	// A refused file poisons its whole unit however confidently it would have resolved, so it never reaches IDENTIFY.
	decisions := make([]library.UnitDecision, len(batch))
	var kept []int
	for i, reason := range j.poisonedByExclusion(batch, mediaType, session.CategoryID) {
		if reason != "" {
			decisions[i] = library.UnitDecision{UnitID: ids[i], Decision: library.UnitSkip, Reason: reason}
			continue
		}
		kept = append(kept, i)
	}
	placements := make([]*library.Placement, len(batch))
	if len(kept) > 0 {
		request := importbatch.Request{
			SessionID: sessionID, AcquisitionID: session.AcquisitionID, SourcePath: sourcePath,
			Preview: preview, Ladder: ladder, Threshold: threshold, AddUnidentified: unverified,
		}
		if session.CategoryID != nil {
			request.CategoryID = *session.CategoryID
		}
		for _, i := range kept {
			request.Units = append(request.Units, batch[i])
			request.UnitIDs = append(request.UnitIDs, ids[i])
		}
		result, err := importer.ImportBatch(ctx, request)
		if err != nil {
			// A retry resumes after the last unit recorded: this cluster's rows go, so it is inspected again.
			if deleteErr := j.sessions.DeleteUnits(ctx, sessionID, ids); deleteErr != nil {
				j.logger.Warn("import: could not forget an unfinished cluster", slog.Int64("session_id", sessionID), slog.Any("error", deleteErr))
			}
			return fmt.Errorf("importjob: import batch: %w", err)
		}
		for k, i := range kept {
			decisions[i] = result.Decisions[k]
			placements[i] = result.Placements[k]
		}
	}
	if err := j.sessions.SetUnitDecisions(ctx, sessionID, decisions); err != nil {
		return fmt.Errorf("importjob: record decisions: %w", err)
	}
	for i, placement := range placements {
		if placement == nil {
			continue
		}
		if err := j.sessions.RecordPlacement(ctx, ids[i], *placement); err != nil {
			j.logger.Warn("import commit: could not record placement", slog.Int64("unit_id", ids[i]), slog.Any("error", err))
		}
	}
	j.publishProgress(ctx, sessionID, importPhaseWalking, batch[len(batch)-1].Directory)
	return nil
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
	if j.events == nil {
		return
	}
	session, err := j.sessions.Get(ctx, sessionID)
	if err != nil {
		j.logger.Debug("could not read session for a progress event", slog.Any("error", err))
		return
	}
	var category int64
	if session.CategoryID != nil {
		category = *session.CategoryID
	}
	j.events.Publish(events.Event{
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
		ActionKey: ActionKeyFor(payload.SourcePath),
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

// walkOptions are the inspection options of a category's media type: its own size floor, and the files asked for.
func (j *Job) walkOptions(categoryID *int64, only []string) inspect.WalkOptions {
	var options inspect.WalkOptions
	if importer, _ := j.importerFor(categoryID); importer != nil {
		if floor, ok := importer.(importbatch.SizeFloor); ok {
			options.MediaSizeFloorBytes = floor.MediaSizeFloorBytes()
		}
	}
	if len(only) > 0 {
		options.Only = make(map[string]bool, len(only))
		for _, file := range only {
			options.Only[file] = true
		}
	}
	return options
}
