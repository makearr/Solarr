package importjob

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/events"
)

// CommitJobType is the registered type of a manual placement: the held units a person committed, or the decided units a
// library was just given.
const CommitJobType = jobs.Type("import.commit_held")

// CommitDefinition declares it in the import pool, so a manual placement never runs beside an import (concurrency 1).
var CommitDefinition = jobs.Definition{
	Name: "Placing imported files",
	Type: CommitJobType, Pool: jobs.PoolImport, Mutating: true, MaxAttempts: 1,
}

const (
	importPhasePlacing = "placing"
	importPhasePlaced  = "placed"
)

// CommitPayload is what a manual placement carries.
type CommitPayload struct {
	SessionID int64 `json:"session_id"`
	// UnitIDs limits a held commit to these units; empty means every held unit.
	UnitIDs []int64 `json:"unit_ids,omitempty"`
	// Unplaced places the units already decided COMMIT instead of committing held ones.
	Unplaced bool `json:"unplaced,omitempty"`
}

// CommitActionKey allows one manual placement per session at a time; a second request answers the running job.
func CommitActionKey(sessionID int64) string {
	return fmt.Sprintf("%s:%d", CommitJobType, sessionID)
}

// CommitJob runs a manual placement off the request, in the import pool.
type CommitJob struct {
	sessions  library.ImportSessions
	committer ManualCommitter
	events    *events.Bus
	logger    *slog.Logger
}

// NewCommitJob builds the handler; bus and logger may be nil.
func NewCommitJob(sessions library.ImportSessions, committer ManualCommitter, bus *events.Bus, logger *slog.Logger) *CommitJob {
	if logger == nil {
		logger = slog.Default()
	}
	return &CommitJob{sessions: sessions, committer: committer, events: bus, logger: logger}
}

// Run places best-effort: a unit that cannot be placed is logged and stays for the next try, never fails the rest.
func (j *CommitJob) Run(ctx context.Context, job jobs.Job) error {
	var payload CommitPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Terminalf("import.commit_held", string(CommitJobType), err)
	}
	publishSessionProgress(ctx, j.events, j.sessions, j.logger, payload.SessionID, importPhasePlacing, "")
	var (
		placed int
		errs   []error
	)
	if payload.Unplaced {
		placed, errs = j.committer.PlaceUnplaced(ctx, payload.SessionID)
	} else {
		placed, errs = j.committer.CommitAllHeld(ctx, payload.SessionID, payload.UnitIDs)
	}
	for _, err := range errs {
		j.logger.Warn("manual placement", slog.Int64("session_id", payload.SessionID), slog.Any("error", err))
	}
	j.logger.Info("manual placement", slog.Int64("session_id", payload.SessionID), slog.Int("placed", placed), slog.Int("not_placed", len(errs)))
	publishSessionProgress(context.WithoutCancel(ctx), j.events, j.sessions, j.logger, payload.SessionID, importPhasePlaced, "")
	return nil
}

// EnqueueCommit submits a manual placement, deduplicated per session.
func EnqueueCommit(ctx context.Context, queue *jobs.Queue, payload CommitPayload) (jobs.EnqueueResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("importjob: encode commit payload: %w", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: CommitJobType, ActionKey: CommitActionKey(payload.SessionID), Priority: jobs.PriorityInteractive, Payload: raw,
	})
}
