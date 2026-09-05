// Package importjob is ADR-48's import.inspect job: the resumable
// WALK/CLASSIFY/GROUP/EVIDENCE/IDENTIFY/SHAPE/DECIDE pipeline, wired to
// internal/library.ImportSessions -- its first real caller. It only inspects
// and records; it never moves a file (internal/mover, todo 4.12, does not
// exist yet) and never resolves an identity into a real work/edition row, so a
// COMMIT decision here means "this unit's confidence cleared the threshold",
// not "this unit is in the library" -- turning that into a placed file is a
// later, separate action ("commit what was held", architecture.md section
// 21.14's own Import screen), not this job's job.
package importjob

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/inspect"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/model"
	"github.com/makearr/MediaNexus/internal/music"
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
}

// Policy is the one setting this job needs from settings.db -- asked fresh
// per run rather than cached, the same "ask, don't cache" discipline every
// other job's settings dependency already uses.
type Policy interface {
	ImportAutoCommitConfidence() float64
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
	clock  clock.Clock
	logger *slog.Logger
}

// Config is what a Job needs. Every field is required except Search and Logger.
type Config struct {
	FS       fsx.FS
	Sessions library.ImportSessions
	Policy   Policy
	Tool     mediatool.Tool
	Search   music.Searcher
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
		fsys: config.FS, sessions: config.Sessions, policy: config.Policy,
		tool: config.Tool, search: config.Search, clock: config.Clock, logger: logger,
	}
}

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

	sessionID, cursor, err := j.resumeOrCreate(ctx, payload)
	if err != nil {
		return err
	}

	identifyCfg := model.ChainConfig{
		AcceptThreshold: music.DefaultAcceptThreshold, AmbiguityThreshold: j.policy.ImportAutoCommitConfidence(),
	}
	result, err := inspect.NewWalker().Walk(ctx, j.fsys, payload.SourcePath, inspect.WalkState{Cursor: cursor},
		inspect.WalkOptions{},
		func(ctx context.Context, batch []inspect.Unit) error {
			return j.commitCluster(ctx, sessionID, payload.SourcePath, identifyCfg, batch)
		})
	if err != nil {
		return err
	}
	j.logger.Info("import inspection finished",
		slog.Int64("session_id", sessionID), slog.String("source_path", payload.SourcePath),
		slog.Int("units_emitted", result.UnitsEmitted), slog.Bool("completed", result.Completed))

	return j.sessions.Finish(ctx, sessionID, library.ImportDone, j.clock.Now().Unix(), "")
}

// resumeOrCreate finds an in-progress session for this exact source path
// (INSPECTING, this job's only state while it runs) and resumes it from its
// own last-committed cluster, or starts a new one. Session rows are the
// persisted progress a retry needs -- there is no separate cursor column, the
// most recently inserted unit's own rel_path already names the cluster a
// resumed walk should skip up to.
func (j *Job) resumeOrCreate(ctx context.Context, payload Payload) (sessionID int64, cursor string, err error) {
	existing, err := j.sessions.List(ctx, library.ImportInspecting)
	if err != nil {
		return 0, "", fmt.Errorf("importjob: list in-progress sessions: %w", err)
	}
	for _, session := range existing {
		if session.SourcePath != payload.SourcePath {
			continue
		}
		units, err := j.sessions.ListUnits(ctx, session.ID)
		if err != nil {
			return 0, "", fmt.Errorf("importjob: list units for session %d: %w", session.ID, err)
		}
		if len(units) > 0 {
			cursor = units[len(units)-1].RelPath
		}
		return session.ID, cursor, nil
	}

	id, err := j.sessions.Create(ctx, library.NewSession{
		SourcePath: payload.SourcePath, Origin: payload.Origin,
		AcquisitionID: payload.AcquisitionID, CategoryID: payload.CategoryID,
		Threshold: j.policy.ImportAutoCommitConfidence(), StartedAt: j.clock.Now().Unix(),
	})
	if err != nil {
		return 0, "", fmt.Errorf("importjob: create session: %w", err)
	}
	return id, "", nil
}

// commitCluster probes one cluster's media files in a single batched call
// (ADR-28), identifies and shapes every unit in it, and records the result.
// AddUnits is called before IDENTIFY runs, not after: a unit's row must exist
// -- at UnitQueue, the same default the schema already documents -- before
// SetUnitDecision has an id to update, and inserting first means a probe
// failure still leaves the unit visible rather than silently absent.
func (j *Job) commitCluster(
	ctx context.Context, sessionID int64, sourcePath string, cfg model.ChainConfig, batch []inspect.Unit,
) error {
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

	for i, unit := range batch {
		if err := j.identifyAndRecord(ctx, ids[i], unit, identifiers, cfg); err != nil {
			return err
		}
	}
	return nil
}

// identifyAndRecord runs IDENTIFY for one unit and records DECIDE's outcome.
// An IdentifyUnit error (every file in the unit unreadable) is not
// propagated as a job failure -- it is DecisionFailed, recorded and moved
// past, the same "one bad unit does not fail the batch" reasoning
// consumer.go's own identifyBatch already applies at scan time.
func (j *Job) identifyAndRecord(
	ctx context.Context, unitID int64, unit inspect.Unit, identifiers []model.Identifier, cfg model.ChainConfig,
) error {
	result, err := music.IdentifyUnit(ctx, unit, identifiers, cfg)
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
	if setErr := j.sessions.SetUnitDecision(
		ctx, unitID, string(decision), confidence, nil, nil, candidatesJSON, "",
	); setErr != nil {
		return fmt.Errorf("importjob: record decision for unit %d: %w", unitID, setErr)
	}
	return nil
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
