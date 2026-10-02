package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ImportSessions implements apiserver.ImportSessionLister over
// library.ImportSessions, library.ImportRevert and importjob.ManualCommitter.
type ImportSessions struct {
	Sessions library.ImportSessions
	Revert   library.ImportRevert
	Manual   importjob.ManualCommitter
	Clock    clock.Clock
	// Libraries gives a revert its library's recycling bin.
	Libraries LibraryLookup
}

func (a ImportSessions) ListImportSessions(
	ctx context.Context, filterState string, includeDismissed bool,
) ([]apiserver.ImportSessionRow, error) {
	sessions, err := a.Sessions.List(ctx, filterState, includeDismissed)
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

// CancelImportSession checks existence first for a genuine 404 -- RequestCancel's own UPDATE cannot tell them apart.
func (a ImportSessions) CancelImportSession(ctx context.Context, id int64) error {
	if _, err := a.Sessions.Get(ctx, id); err != nil {
		return err
	}
	err := a.Sessions.RequestCancel(ctx, id)
	if errors.Is(err, library.ErrSessionNotCancellable) {
		return apiserver.ErrImportSessionNotRunning
	}
	return err
}

// DismissImportSession checks existence first for a genuine 404, the same
// shape CancelImportSession above keeps.
func (a ImportSessions) DismissImportSession(ctx context.Context, id int64) error {
	if _, err := a.Sessions.Get(ctx, id); err != nil {
		return err
	}
	err := a.Sessions.Dismiss(ctx, id, a.Clock.Now().Unix())
	if errors.Is(err, library.ErrSessionNotDismissable) {
		return apiserver.ErrImportSessionNotFinished
	}
	return err
}

// RevertImportSession maps library.ErrSessionNotRevertible to
// apiserver.ErrImportSessionNotRevertible, the same translation
// CancelImportSession already does for library.ErrSessionNotCancellable.
func (a ImportSessions) RevertImportSession(ctx context.Context, id int64, retainHours int) (apiserver.ImportRevertResult, error) {
	session, err := a.Sessions.Get(ctx, id)
	if err != nil {
		return apiserver.ImportRevertResult{}, err
	}
	var categoryIDs []int64
	if session.CategoryID != nil {
		categoryIDs = []int64{*session.CategoryID}
	}
	recycling := RecyclingFor(a.Libraries, categoryIDs)
	if retainHours > 0 {
		recycling.Retain = time.Duration(retainHours) * time.Hour
	}
	result, err := a.Revert.Revert(ctx, id, recycling, a.Clock.Now().Unix())
	if errors.Is(err, library.ErrSessionNotRevertible) {
		return apiserver.ImportRevertResult{}, apiserver.ErrImportSessionNotRevertible
	}
	if err != nil {
		return apiserver.ImportRevertResult{}, err
	}
	paths := make([]string, len(result.RecycledPaths))
	for i, entry := range result.RecycledPaths {
		paths[i] = entry.RecycledPath
	}
	return apiserver.ImportRevertResult{
		UnitsReverted: int64(result.UnitsReverted), WorksDeleted: int64(result.WorksDeleted),
		EditionsDeleted: int64(result.EditionsDeleted), RecycledPaths: paths,
	}, nil
}

// AssignImportCategory maps library.ErrSessionCategoryAlreadySet and
// importjob.ErrSessionReverted to their apiserver sentinels, the same
// translation RevertImportSession already does for its own errors.
func (a ImportSessions) AssignImportCategory(ctx context.Context, id, categoryID int64) (apiserver.ImportPlacementResult, error) {
	session, err := a.Sessions.Get(ctx, id)
	if err != nil {
		return apiserver.ImportPlacementResult{}, err
	}
	if session.State == library.ImportReverted {
		return apiserver.ImportPlacementResult{}, apiserver.ErrImportSessionReverted
	}
	if err := a.Sessions.SetCategory(ctx, id, categoryID); err != nil {
		if errors.Is(err, library.ErrSessionCategoryAlreadySet) {
			return apiserver.ImportPlacementResult{}, apiserver.ErrImportSessionCategoryAlreadySet
		}
		return apiserver.ImportPlacementResult{}, err
	}

	placed, placeErrs := a.Manual.PlaceUnplaced(ctx, id)
	messages := make([]string, len(placeErrs))
	for i, placeErr := range placeErrs {
		messages[i] = placeErr.Error()
	}
	return apiserver.ImportPlacementResult{UnitsPlaced: int64(placed), Errors: messages}, nil
}

// CommitHeldImportUnits maps the two refusals that stop the whole call;
// a per-unit failure stays in Errors, never an error, since the bulk commit
// is best-effort the same way PlaceUnplaced is.
func (a ImportSessions) CommitHeldImportUnits(
	ctx context.Context, sessionID int64, unitIDs []int64,
) (apiserver.ImportPlacementResult, error) {
	committed, commitErrs := a.Manual.CommitAllHeld(ctx, sessionID, unitIDs)
	messages := make([]string, 0, len(commitErrs))
	for _, commitErr := range commitErrs {
		switch {
		case errors.Is(commitErr, sql.ErrNoRows):
			return apiserver.ImportPlacementResult{}, sql.ErrNoRows
		case errors.Is(commitErr, importjob.ErrSessionHasNoCategory):
			return apiserver.ImportPlacementResult{}, apiserver.ErrImportSessionHasNoCategory
		case errors.Is(commitErr, importjob.ErrSessionReverted):
			return apiserver.ImportPlacementResult{}, apiserver.ErrImportSessionReverted
		}
		messages = append(messages, commitErr.Error())
	}
	return apiserver.ImportPlacementResult{UnitsPlaced: int64(committed), Errors: messages}, nil
}

// CommitImportUnit maps importjob.ManualCommitter's own sentinel errors to
// their apiserver counterparts.
func (a ImportSessions) CommitImportUnit(
	ctx context.Context, sessionID, unitID int64, as *apiserver.ImportIdentity,
) (apiserver.ImportUnitRow, error) {
	unit, err := a.Sessions.GetUnit(ctx, unitID)
	if err != nil {
		return apiserver.ImportUnitRow{}, err
	}
	if unit.SessionID != sessionID {
		return apiserver.ImportUnitRow{}, sql.ErrNoRows
	}

	var commitErr error
	if as != nil {
		_, commitErr = a.Manual.CommitHeldAs(ctx, unitID, as.Provider, as.ExternalID)
	} else {
		_, commitErr = a.Manual.CommitHeld(ctx, unitID)
	}
	if err := commitErr; err != nil {
		switch {
		case errors.Is(err, importjob.ErrUnitNotQueued):
			return apiserver.ImportUnitRow{}, apiserver.ErrImportUnitNotQueued
		case errors.Is(err, importjob.ErrNoCandidate):
			return apiserver.ImportUnitRow{}, apiserver.ErrImportUnitHasNoCandidate
		case errors.Is(err, importjob.ErrSessionHasNoCategory):
			return apiserver.ImportUnitRow{}, apiserver.ErrImportSessionHasNoCategory
		case errors.Is(err, importjob.ErrSessionReverted):
			return apiserver.ImportUnitRow{}, apiserver.ErrImportSessionReverted
		case errors.Is(err, importjob.ErrUnitChanged):
			return apiserver.ImportUnitRow{}, apiserver.ErrImportUnitChanged
		case errors.Is(err, music.ErrNoPlacement):
			return apiserver.ImportUnitRow{}, apiserver.ErrNoPlacement
		case errors.Is(err, music.ErrBlockedContent):
			return apiserver.ImportUnitRow{}, apiserver.ErrBlockedContent
		default:
			return apiserver.ImportUnitRow{}, err
		}
	}

	updated, err := a.Sessions.GetUnit(ctx, unitID)
	if err != nil {
		return apiserver.ImportUnitRow{}, err
	}
	return importUnitRow(updated), nil
}

func (a ImportSessions) ListImportUnits(
	ctx context.Context, sessionID int64, filterDecision string, afterID int64, limit int,
) ([]apiserver.ImportUnitRow, error) {
	units, err := a.Sessions.ListUnitsPage(ctx, sessionID, afterID, filterDecision, int64(limit))
	if err != nil {
		return nil, err
	}
	// One aggregate read per page rather than per unit: a session's methods
	// are a handful of rows, not one per file.
	methods, err := a.Sessions.UnitTransferMethods(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.ImportUnitRow, len(units))
	for i, u := range units {
		rows[i] = importUnitRow(u)
		for _, m := range methods[u.ID] {
			rows[i].TransferMethods = append(rows[i].TransferMethods,
				apiserver.TransferMethodCount{Method: m.Method, Files: m.Files})
		}
	}
	return rows, nil
}

func importSessionRow(s library.Session) apiserver.ImportSessionRow {
	row := apiserver.ImportSessionRow{
		ID: s.ID, SourcePath: s.SourcePath, Origin: s.Origin, CategoryID: s.CategoryID,
		State: s.State, Threshold: s.Threshold, UnitsTotal: s.UnitsTotal,
		UnitsCommitted: s.UnitsCommitted, UnitsQueued: s.UnitsQueued,
		UnitsSkipped: s.UnitsSkipped, UnitsFailed: s.UnitsFailed, BytesImported: s.BytesImported,
		UnitsEstimated: s.UnitsEstimated,
		StartedAt:      time.Unix(s.StartedAt, 0), Error: s.Error,
	}
	if s.FinishedAt != nil {
		finished := time.Unix(*s.FinishedAt, 0)
		row.FinishedAt = &finished
	}
	if s.DismissedAt != nil {
		dismissed := time.Unix(*s.DismissedAt, 0)
		row.DismissedAt = &dismissed
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
		for _, evidence := range c.Evidence {
			view.Evidence = append(view.Evidence, apiserver.IdentificationEvidence{Kind: evidence.Kind, Detail: evidence.Detail})
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

func (a ImportInspector) InspectImport(
	ctx context.Context, sourcePath, origin string, categoryID *int64, preview, addUnverified bool,
) error {
	_, err := importjob.Enqueue(ctx, a.Queue, importjob.Payload{
		SourcePath: sourcePath, Origin: origin, CategoryID: categoryID, Preview: preview, NoUnverified: !addUnverified,
	})
	return err
}
