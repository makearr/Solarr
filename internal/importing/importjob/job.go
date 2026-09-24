// Package importjob is ADR-48's import.inspect job: the resumable
// WALK/CLASSIFY/GROUP/EVIDENCE/IDENTIFY/SHAPE/DECIDE pipeline, wired to
// internal/db/library.ImportSessions -- its first real caller. Job places a
// COMMIT-decided unit inline, the instant the session has a category to place
// into (todo 4.9's "COMMIT actually imports"); a COMMIT decision with no
// category set yet, or a unit DECIDE queued below the confidence threshold,
// stays merely decided -- ManualCommitter (manualcommit.go, same package) is
// where the Import screen's own two remaining actions live: place what a
// session decided before it had a category, and override one unit's own
// QUEUE decision by hand ("commit what was held").
package importjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sync"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/pack"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/events"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// JobType is the registered type of an import inspection run.
const JobType = jobs.Type("import.inspect")

// Definition declares how the job behaves. It is bulk work: potentially long
// (a whole-library adoption), resumable, and it must never sit in front of
// anything a user is waiting on.
var Definition = jobs.Definition{
	Type: JobType, Pool: jobs.PoolMaintenance, Mutating: true, MaxAttempts: 3,
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
}

// ReasonPreview is why a unit a preview run would otherwise have committed is
// waiting, so the Import screen can say so rather than implying IDENTIFY was
// unsure.
const ReasonPreview = "held for review"

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

// Committer places one COMMIT-decided unit into the library --
// music.Committer's own shape, narrowed to an interface so a test can fake
// it without building every real dependency (settings roots, every library
// store) music.Committer itself needs.
type Committer interface {
	CommitMatchedUnit(
		ctx context.Context, sourceRoot string, unit inspect.Unit, best model.Identification, tags music.Resolved,
		categoryID int64, matched *music.AlbumPlacement, ladder []string,
	) (music.Placed, error)
}

// Job runs one import.inspect execution: walk, identify, shape, decide, record.
type Job struct {
	fsys     fsx.FS
	sessions library.ImportSessions
	policy   Policy
	tool     mediatool.Tool
	// search is optional: when nil, IDENTIFY runs MBIDIdentifier alone (the
	// same scope this pass's own live verification proved). Wiring a real
	// Searcher is deliberately deferred to whichever caller needs real
	// auto-resolution against untagged scene content.
	search music.Searcher
	// albums is optional: when set, a unit its embedded MBIDs did not identify is matched as an album.
	albums music.AlbumLookup
	// acoustID is optional: when set, a unit nothing else named is fingerprinted
	// and looked up (ADR-29's own last rung). Disabled without a key of its own.
	acoustID music.AcoustIDLookup
	// committer is optional: nil means a COMMIT decision never places a file,
	// today's own default for a session with no destination category
	// (private/questions.md) or a caller (a test) with no real library to
	// place into.
	committer Committer
	// packs is todo 4.11's own detection sink, required like sessions/tool --
	// unlike committer, detection needs no category and no destination, so
	// it runs whether or not a session was ever placed.
	packs library.Packs
	// events is optional: nil means no progress is published, the same as
	// every other optional dependency in this codebase.
	events *events.Bus
	clock  clock.Clock
	logger *slog.Logger
}

// Config is what a Job needs. Every field is required except Search,
// Committer, Events and Logger.
type Config struct {
	FS        fsx.FS
	Sessions  library.ImportSessions
	Policy    Policy
	Tool      mediatool.Tool
	Search    music.Searcher
	Albums    music.AlbumLookup
	AcoustID  music.AcoustIDLookup
	Committer Committer
	Packs     library.Packs
	Events    *events.Bus
	Clock     clock.Clock
	Logger    *slog.Logger
}

// NewJob builds the handler.
func NewJob(config Config) *Job {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Job{
		fsys: config.FS, sessions: config.Sessions, policy: config.Policy,
		tool: config.Tool, search: config.Search, albums: config.Albums, acoustID: config.AcoustID,
		committer: config.Committer, packs: config.Packs,
		events: config.Events, clock: config.Clock, logger: logger,
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

	identifyCfg := model.ChainConfig{
		AcceptThreshold: music.DefaultAcceptThreshold, AmbiguityThreshold: threshold,
	}
	result, err := inspect.NewWalker().Walk(ctx, j.fsys, payload.SourcePath, inspect.WalkState{Cursor: cursor},
		inspect.WalkOptions{},
		func(ctx context.Context, batch []inspect.Unit) error {
			return j.commitCluster(ctx, sessionID, payload.SourcePath, identifyCfg, payload.Preview,
				payload.TransferLadder, batch)
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
	return nil
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

// identifyParallelism bounds IDENTIFY's fan-out across one cluster's units.
// IDENTIFY is pure CPU today (Search is wired nil in production, cmd/solarr/app/job_import.go)
// so this has no live payoff yet, but todo 4.21 asks for it built ahead of a
// real Searcher -- the same mediatool.defaultParallelism figure, for the same
// reason: a bound worth the same order of magnitude as the machine, not tied
// to any one identifier's own cost.
const identifyParallelism = 8

// identifyCluster runs IdentifyUnit for every unit in a cluster concurrently,
// bounded by identifyParallelism -- writes stay serial (todo 4.21): this
// returns UnitDecision values, and the caller records them all in one
// SetUnitDecisions transaction, not one per unit. Every model.Identifier
// passed in must tolerate concurrent Identify calls across different files;
// today's only one (MBIDIdentifier) does no more than read-only map lookups.
// identifyCluster also returns each unit's own model.ChainResult, positionally
// lined up with the returned decisions -- commitCluster's own placement step
// needs Best to place a COMMIT-decided unit without re-running IDENTIFY a
// second time (ADR-48's own speed rule: "probe only what identification will
// use" applies just as much to a second identify call as to a second probe).
func identifyCluster(
	ctx context.Context, ids []int64, batch []inspect.Unit, identifiers []model.Identifier, cfg model.ChainConfig,
	refine refineUnit,
) ([]library.UnitDecision, []model.ChainResult, []*music.AlbumPlacement) {
	decisions := make([]library.UnitDecision, len(batch))
	results := make([]model.ChainResult, len(batch))
	placements := make([]*music.AlbumPlacement, len(batch))
	slots := make(chan struct{}, identifyParallelism)
	var running sync.WaitGroup
	for i, unit := range batch {
		running.Add(1)
		slots <- struct{}{}
		go func(i int, unit inspect.Unit) {
			defer running.Done()
			defer func() { <-slots }()
			decisions[i], results[i], placements[i] = identifyUnit(ctx, ids[i], unit, identifiers, cfg, refine)
		}(i, unit)
	}
	running.Wait()
	return decisions, results, placements
}

// identifyUnit runs IDENTIFY for one unit and turns DECIDE's outcome into a
// UnitDecision, ready to record. An IdentifyUnit error (every file in the
// unit unreadable) becomes DecisionFailed rather than propagating -- the same
// "one bad unit does not fail the batch" reasoning consumer.go's own
// identifyBatch already applies at scan time.
func identifyUnit(
	ctx context.Context, unitID int64, unit inspect.Unit, identifiers []model.Identifier, cfg model.ChainConfig,
	refine refineUnit,
) (library.UnitDecision, model.ChainResult, *music.AlbumPlacement) {
	result, err := music.IdentifyUnit(ctx, unit, identifiers, cfg)
	var placement *music.AlbumPlacement
	if err == nil && refine != nil {
		result, placement = refine(ctx, unit, result)
	}
	decision := model.DecisionFailed
	var confidence float64
	var candidatesJSON string
	if err == nil {
		decision = model.Decide(result)
		if result.Best != nil {
			confidence = result.Best.Confidence
		}
		if len(result.Candidates) > 0 {
			if data, marshalErr := json.Marshal(result.Candidates); marshalErr == nil {
				candidatesJSON = string(data)
			}
		}
	}
	return library.UnitDecision{UnitID: unitID, Decision: string(decision), Confidence: confidence, CandidatesJSON: candidatesJSON},
		result, placement
}

// refineUnit improves one unit's chain result; it must be safe to call concurrently.
type refineUnit func(ctx context.Context, unit inspect.Unit, result model.ChainResult) (model.ChainResult, *music.AlbumPlacement)

// albumRefiner matches units as albums over the cluster's probes, or nil when no lookup is configured.
func (j *Job) albumRefiner(relPaths []string, probes []mediatool.Probe, threshold float64) refineUnit {
	if j.albums == nil {
		return nil
	}
	byPath := make(map[string]mediatool.Probe, len(probes))
	for i, probe := range probes {
		byPath[relPaths[i]] = probe
	}
	return func(ctx context.Context, unit inspect.Unit, result model.ChainResult) (model.ChainResult, *music.AlbumPlacement) {
		refined, placement, err := music.RefineUnitByAlbum(ctx, j.albums, unit, byPath, result, threshold)
		if err != nil {
			j.logger.Warn("import album identification failed", slog.String("unit", unit.Directory), slog.Any("error", err))
		}
		return refined, placement
	}
}

// poisonedByExclusion checks every file a unit carries -- Media and Other
// alike, since a refuse-action file does not have to be classified as media
// itself (a ".exe" beside audio classifies as ClassUnknown, landing in
// Other, not Media) -- against ADR-53's refuse action (todo 4.15's import
// gate). Positionally aligned with batch; an empty string means the unit is
// not poisoned. Runs before IDENTIFY commits any work to a doomed unit.
func (j *Job) poisonedByExclusion(batch []inspect.Unit, categoryID *int64) []string {
	const mediaType = "music" // this package is music-coupled today; see the package doc comment
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

// commitCluster probes one cluster's media files in a single batched call
// (ADR-28), identifies and shapes every unit in it, and records the result.
// AddUnits is called before IDENTIFY runs, not after: a unit's row must exist
// -- at UnitQueue, the same default the schema already documents -- before
// SetUnitDecisions has an id to update, and inserting first means a probe
// failure still leaves the unit visible rather than silently absent.
//
// Cancellation is checked first, before any work -- cheaper than probing a whole cluster to discard it.
//
// The session's own category is re-read here rather than trusted from the
// job's own payload (captured once, at Run's own start): todo 4.9's "assign
// a category later" UI action can PATCH import_session.category_id while
// this very session is still INSPECTING, and a cluster committed after that
// PATCH must place into it -- the same "ask, don't cache" rule Policy's own
// per-run read already follows.
func (j *Job) commitCluster(
	ctx context.Context, sessionID int64, sourcePath string, cfg model.ChainConfig, preview bool, ladder []string,
	batch []inspect.Unit,
) error {
	session, err := j.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("importjob: check session %d: %w", sessionID, err)
	}
	if session.State == library.ImportCancelling {
		return errCancelled
	}
	categoryID := session.CategoryID

	var relPaths []string
	for _, unit := range batch {
		for _, f := range unit.Media {
			relPaths = append(relPaths, f.RelPath)
		}
	}
	absolute := make([]string, len(relPaths))
	for i, rel := range relPaths {
		absolute[i] = path.Join(sourcePath, rel)
	}
	probes, err := j.tool.ReadTags(ctx, absolute)
	if err != nil {
		return fmt.Errorf("importjob: probe cluster: %w", err)
	}
	tagSource := music.UnitTagSourceFor(sourcePath, probes)
	identifiers := []model.Identifier{music.NewMBIDIdentifier(tagSource)}
	if j.search != nil {
		identifiers = append(identifiers,
			music.NewSearchIdentifier(music.SearchIdentifierConfig{Tags: tagSource, Search: j.search}))
	}
	// No Enabled() check here: the identifier answers nothing and decodes nothing
	// without a key, so one place decides whether the rung is on (D12).
	if j.acoustID != nil {
		identifiers = append(identifiers, music.NewAcoustIDIdentifier(music.AcoustIDIdentifierConfig{
			Fingerprints: j.tool, Lookup: j.acoustID,
			Absolute: func(relPath string) string { return path.Join(sourcePath, relPath) },
		}))
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

	poisonReasons := j.poisonedByExclusion(batch, categoryID)

	decisions, results, placements := identifyCluster(ctx, ids, batch, identifiers, cfg, j.albumRefiner(relPaths, probes, cfg.AmbiguityThreshold))
	for i, reason := range poisonReasons {
		if reason != "" {
			// Overrides whatever IDENTIFY decided -- ADR-53's refuse action
			// poisons the whole unit regardless of how confidently it would
			// otherwise have resolved.
			decisions[i] = library.UnitDecision{UnitID: ids[i], Decision: library.UnitSkip, Reason: reason}
		}
	}
	if preview {
		holdForReview(decisions)
	}
	if err := j.sessions.SetUnitDecisions(ctx, sessionID, decisions); err != nil {
		return fmt.Errorf("importjob: record decisions: %w", err)
	}
	if categoryID != nil && j.committer != nil {
		j.placeCommitted(ctx, sessionID, *categoryID, sourcePath, tagSource, ids, batch, decisions, results,
			placements, ladder)
	}
	j.publishProgress(ctx, sessionID, importPhaseWalking, batch[len(batch)-1].Directory)
	return nil
}

// holdForReview is a preview run's own override of DECIDE: every unit that
// would have committed waits for a human instead. Done here rather than by
// raising the confidence bar, because album matching answers on its own
// capped scale (albumConfidence never reaches an embedded MBID's own
// certainty, deliberately) -- a threshold high enough to hold it would be
// meaningless against every other strategy's. SKIP and FAILED are left alone:
// neither was ever going to be placed.
func holdForReview(decisions []library.UnitDecision) {
	for i, decision := range decisions {
		if decision.Decision == library.UnitCommit {
			decisions[i].Decision = library.UnitQueue
			decisions[i].Reason = ReasonPreview
		}
	}
}

// placeCommitted is ADR-48's own "COMMIT actually imports": every unit this
// cluster just decided COMMIT is placed immediately, in the same pass --
// inline rather than a genuinely separate resumable phase, because a unit's
// own member file list (inspect.Unit.Media) is not persisted independently
// of this in-memory batch, only its identity once resolved (import_unit's
// own columns). A placement failure is logged and the unit is left
// decided-but-unplaced (work_id stays NULL) rather than failing the
// cluster -- the same "one bad unit does not fail the batch" tolerance
// IDENTIFY already has, and matches DEFER's own "structured reason, not a
// crash" (section 11.7). A crash mid-placement (as opposed to a returned
// error) can leave a unit stuck decided-but-unplaced with nothing to retry
// it automatically -- a known, narrow gap (private/notes.md), not handled
// this pass.
func (j *Job) placeCommitted(
	ctx context.Context, sessionID, categoryID int64, sourcePath string, tagSource music.Resolved,
	ids []int64, batch []inspect.Unit, decisions []library.UnitDecision, results []model.ChainResult,
	placements []*music.AlbumPlacement, ladder []string,
) {
	for i, decision := range decisions {
		if decision.Decision != library.UnitCommit || results[i].Best == nil {
			continue
		}
		placed, err := j.committer.CommitMatchedUnit(ctx, sourcePath, batch[i], *results[i].Best, tagSource, categoryID,
			placements[i], ladder)
		if err != nil {
			// ADR-53 part 2 (todo 4.15): a blocked hash is loud, not a silent
			// decided-but-unplaced unit indistinguishable from "no root had
			// room" -- DECIDE already chose COMMIT before the hash was known,
			// so this is the one case that overrides a decision after
			// SetUnitDecisions already ran, not before it (unlike the
			// exclusion-pattern gate above, checked before IDENTIFY).
			if errors.Is(err, music.ErrBlockedContent) {
				if setErr := j.sessions.SetUnitDecisions(ctx, sessionID, []library.UnitDecision{
					{UnitID: ids[i], Decision: library.UnitSkip, Reason: err.Error()},
				}); setErr != nil {
					j.logger.Warn("import commit: could not record blocked-content decision",
						slog.Int64("unit_id", ids[i]), slog.Any("error", setErr))
				}
				continue
			}
			j.logger.Warn("import commit: could not place unit",
				slog.Int64("unit_id", ids[i]), slog.String("path", batch[i].Directory), slog.Any("error", err))
			continue
		}
		if err := j.sessions.RecordPlacement(ctx, ids[i], library.Placement{
			WorkID: placed.WorkID, WorkCreated: placed.WorkCreated,
			EditionID: placed.EditionID, EditionCreated: placed.EditionCreated,
			MediaObjectIDs: placed.MediaObjectIDs, LibraryPathIDs: placed.LibraryPathIDs,
			TransferMethods: placed.TransferMethodNames(),
		}); err != nil {
			j.logger.Warn("import commit: could not record placement",
				slog.Int64("unit_id", ids[i]), slog.Any("error", err))
		}
		logPathWarnings(j.logger, ids[i], placed.PathWarnings)
	}
}

// logPathWarnings surfaces naming.RenderPath's own non-fatal notices (today:
// only the total-length recommendation) at COMMIT time -- ADR-48's inline
// commit has no separate preview step to block on the way rename does, so a
// log line is this path's own "notification only" (architecture §2), not a
// UI-blocking decision.
func logPathWarnings(logger *slog.Logger, unitID int64, warnings map[int64][]string) {
	for mediaObjectID, messages := range warnings {
		for _, message := range messages {
			logger.Warn("import commit: naming warning",
				slog.Int64("unit_id", unitID), slog.Int64("media_object_id", mediaObjectID), slog.String("warning", message))
		}
	}
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
