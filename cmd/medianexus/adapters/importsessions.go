package adapters

import (
	"context"
	"encoding/json"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/importing/importjob"
	"github.com/makearr/MediaNexus/internal/importing/inspect"
	"github.com/makearr/MediaNexus/internal/jobs"
)

// ImportSessions implements apiserver.ImportSessionLister over library.ImportSessions.
type ImportSessions struct {
	Sessions library.ImportSessions
}

func (a ImportSessions) ListImportSessions(
	ctx context.Context, filterState string,
) ([]apiserver.ImportSessionRow, error) {
	sessions, err := a.Sessions.List(ctx, filterState)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.ImportSessionRow, len(sessions))
	for i, s := range sessions {
		rows[i] = importSessionRow(s)
	}
	return rows, nil
}

func (a ImportSessions) GetImportSession(ctx context.Context, id int64) (apiserver.ImportSessionRow, error) {
	s, err := a.Sessions.Get(ctx, id)
	if err != nil {
		return apiserver.ImportSessionRow{}, err
	}
	return importSessionRow(s), nil
}

func (a ImportSessions) ListImportUnits(
	ctx context.Context, sessionID int64, filterDecision string, afterID int64, limit int,
) ([]apiserver.ImportUnitRow, error) {
	units, err := a.Sessions.ListUnitsPage(ctx, sessionID, afterID, filterDecision, int64(limit))
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.ImportUnitRow, len(units))
	for i, u := range units {
		rows[i] = importUnitRow(u)
	}
	return rows, nil
}

func importSessionRow(s library.Session) apiserver.ImportSessionRow {
	row := apiserver.ImportSessionRow{
		ID: s.ID, SourcePath: s.SourcePath, Origin: s.Origin, CategoryID: s.CategoryID,
		State: s.State, Threshold: s.Threshold, UnitsTotal: s.UnitsTotal,
		UnitsCommitted: s.UnitsCommitted, UnitsQueued: s.UnitsQueued, BytesImported: s.BytesImported,
		StartedAt: time.Unix(s.StartedAt, 0), Error: s.Error,
	}
	if s.FinishedAt != nil {
		finished := time.Unix(*s.FinishedAt, 0)
		row.FinishedAt = &finished
	}
	return row
}

// importUnitRow decodes evidence_json/candidates_json back into the exact
// types they were marshaled from (inspect.Evidence, []model.Identification)
// before mapping into apiserver's own view -- reusing those types rather
// than re-deriving the JSON shape by hand, the same reconciliation lesson
// this session's own duplicated-enum finding already recorded.
func importUnitRow(u library.Unit) apiserver.ImportUnitRow {
	row := apiserver.ImportUnitRow{
		ID: u.ID, SessionID: u.SessionID, RelPath: u.RelPath, Shape: u.Shape,
		FileCount: u.FileCount, Bytes: u.Bytes, Confidence: u.Confidence, Decision: u.Decision,
		WorkID: u.WorkID, EditionID: u.EditionID, Reason: u.Reason,
	}
	var evidence inspect.Evidence
	if u.EvidenceJSON != "" && json.Unmarshal([]byte(u.EvidenceJSON), &evidence) == nil {
		row.Evidence = importEvidenceFrom(evidence)
	}
	if u.CandidatesJSON != "" {
		var candidates []model.Identification
		if json.Unmarshal([]byte(u.CandidatesJSON), &candidates) == nil {
			row.Candidates = importCandidatesFrom(candidates)
		}
	}
	return row
}

func importEvidenceFrom(e inspect.Evidence) apiserver.ImportEvidence {
	signals := make([]apiserver.ImportEvidenceSignal, len(e.Signals))
	for i, s := range e.Signals {
		signals[i] = apiserver.ImportEvidenceSignal{Kind: string(s.Kind), Detail: s.Detail}
	}
	return apiserver.ImportEvidence{Signals: signals, Ambiguous: e.Ambiguous}
}

func importCandidatesFrom(candidates []model.Identification) []apiserver.ImportCandidate {
	views := make([]apiserver.ImportCandidate, len(candidates))
	for i, c := range candidates {
		view := apiserver.ImportCandidate{Strategy: c.Strategy, Confidence: c.Confidence, WorkID: c.WorkID}
		if c.ExternalID != nil {
			view.Provider, view.ExternalID = c.ExternalID.Provider, c.ExternalID.ExternalID
		}
		views[i] = view
	}
	return views
}

// ImportInspector implements apiserver.ImportInspector by enqueuing
// import.inspect (ActionKeyFor deduplicates concurrent submissions of the
// same source path; a retry of one execution resumes rather than
// duplicating, importjob.Job's own concern, not this adapter's).
type ImportInspector struct {
	Queue *jobs.Queue
}

func (a ImportInspector) InspectImport(ctx context.Context, sourcePath, origin string, categoryID *int64) error {
	_, err := importjob.Enqueue(ctx, a.Queue, importjob.Payload{
		SourcePath: sourcePath, Origin: origin, CategoryID: categoryID,
	})
	return err
}
