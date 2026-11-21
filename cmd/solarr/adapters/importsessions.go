package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
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
	// RootPath answers a root folder's filesystem path.
	RootPath library.RootFolderPath
	// Rescan looks again at folders where a revert put files back, so their items are counted from them.
	Rescan func(ctx context.Context, rootFolderID, categoryID int64, directories []string)
	// EnqueuePlacement hands a manual placement to the import pool.
	EnqueuePlacement func(ctx context.Context, payload importjob.CommitPayload) (jobs.EnqueueResult, error)
	// CommitsHeld says whether a media type commits a held unit by hand; its committer is music's.
	CommitsHeld func(mediaType string) bool
}

// heldCommitBuilt refuses a unit whose library's type does not commit held units by hand, the session's library when
// unitID is 0; one without a library is left to the committer, which asks for one.
func (a ImportSessions) heldCommitBuilt(ctx context.Context, sessionID, unitID int64) error {
	session, err := a.Sessions.Get(ctx, sessionID)
	if err != nil || a.CommitsHeld == nil {
		return err
	}
	categoryID := session.CategoryID
	if unitID != 0 {
		unit, err := a.Sessions.GetUnit(ctx, unitID)
		if err != nil {
			return err
		}
		categoryID = unit.PlacesInto(session)
	}
	if categoryID == nil {
		return nil
	}
	if category, found := a.Libraries.Category(*categoryID); found && !a.CommitsHeld(category.MediaType) {
		return apiserver.ErrNotBuiltForMediaType
	}
	return nil
}

// attentionWindow is how long a hand-started import keeps showing among the sessions needing attention.
const attentionWindow = 7 * 24 * time.Hour

func (a ImportSessions) ListImportSessions(
	ctx context.Context, query apiserver.ImportSessionQuery,
) ([]apiserver.ImportSessionRow, error) {
	page := library.SessionPage{
		State: query.State, IncludeDismissed: query.IncludeDismissed, Attention: query.Attention,
		RecentSince: a.Clock.Now().Add(-attentionWindow).Unix(), Limit: int64(query.Limit),
	}
	if query.HasCursor {
		page.HasCursor, page.BeforeStartedAt, page.BeforeID = true, query.BeforeStartedAt.Unix(), query.BeforeID
	}
	sessions, err := a.Sessions.ListPage(ctx, page)
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

// DismissFinishedImportSessions hides every stopped session that holds nothing for review.
func (a ImportSessions) DismissFinishedImportSessions(ctx context.Context) (int64, error) {
	return a.Sessions.DismissFinished(ctx, a.Clock.Now().Unix())
}

// RevertImportSession maps library.ErrSessionNotRevertible to
// apiserver.ErrImportSessionNotRevertible, the same translation
// CancelImportSession already does for library.ErrSessionNotCancellable.
func (a ImportSessions) RevertImportSession(ctx context.Context, id int64, retainHours int) (apiserver.ImportRevertResult, error) {
	session, err := a.Sessions.Get(ctx, id)
	if err != nil {
		return apiserver.ImportRevertResult{}, err
	}
	categoryIDs, err := a.sessionLibraries(ctx, session)
	if err != nil {
		return apiserver.ImportRevertResult{}, err
	}
	recycling := RecyclingFor(a.Libraries, categoryIDs)
	if retainHours > 0 {
		recycling.Retain = time.Duration(retainHours) * time.Hour
	}
	result, err := a.Revert.Revert(ctx, id, recycling, a.Clock.Now().Unix())
	if errors.Is(err, library.ErrSessionNotRevertible) {
		return apiserver.ImportRevertResult{}, apiserver.ErrImportSessionNotRevertible
	}
	if errors.Is(err, library.ErrRevertFileBlocked) {
		return apiserver.ImportRevertResult{}, fmt.Errorf("%w: %w", apiserver.ErrImportRevertBlocked, err)
	}
	if err != nil {
		return apiserver.ImportRevertResult{}, err
	}
	paths := make([]string, len(result.RecycledPaths))
	for i, entry := range result.RecycledPaths {
		paths[i] = entry.RecycledPath
	}
	restored := make([]string, len(result.Restored))
	byRoot := map[int64][]string{}
	for i, entry := range result.Restored {
		restored[i] = entry.OriginalPath
		if entry.RootFolderID != nil {
			byRoot[*entry.RootFolderID] = append(byRoot[*entry.RootFolderID], filepath.Dir(entry.OriginalPath))
		}
	}
	if a.Rescan != nil && len(categoryIDs) > 0 {
		for rootFolderID, directories := range byRoot {
			rootPath, ok := a.RootPath(rootFolderID)
			if !ok {
				continue
			}
			relative := make([]string, 0, len(directories))
			for _, directory := range directories {
				if rel, err := filepath.Rel(rootPath, directory); err == nil {
					relative = append(relative, rel)
				}
			}
			a.Rescan(ctx, rootFolderID, categoryIDs[0], relative)
		}
	}
	notRestored := make([]string, len(result.NotRestored))
	for i, entry := range result.NotRestored {
		notRestored[i] = entry.OriginalPath
	}
	return apiserver.ImportRevertResult{
		UnitsReverted: int64(result.UnitsReverted), WorksDeleted: int64(result.WorksDeleted),
		EditionsDeleted: int64(result.EditionsDeleted), RecycledPaths: paths,
		RestoredPaths: restored, NotRestoredPaths: notRestored,
	}, nil
}

// sessionLibraries are every library a session placed into: its own, and those a Best match import routed units to.
func (a ImportSessions) sessionLibraries(ctx context.Context, session library.Session) ([]int64, error) {
	var categoryIDs []int64
	if session.CategoryID != nil {
		categoryIDs = append(categoryIDs, *session.CategoryID)
	}
	units, err := a.Sessions.ListUnits(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	for _, unit := range units {
		if unit.CategoryID != nil && !slices.Contains(categoryIDs, *unit.CategoryID) {
			categoryIDs = append(categoryIDs, *unit.CategoryID)
		}
	}
	return categoryIDs, nil
}

// AssignImportCategory maps library.ErrSessionCategoryAlreadySet and
// importjob.ErrSessionReverted to their apiserver sentinels, the same
// translation RevertImportSession already does for its own errors. The placing runs as a job.
func (a ImportSessions) AssignImportCategory(ctx context.Context, id, categoryID int64) (apiserver.ImportPlacementJob, error) {
	session, err := a.Sessions.Get(ctx, id)
	if err != nil {
		return apiserver.ImportPlacementJob{}, err
	}
	if session.State == library.ImportReverted {
		return apiserver.ImportPlacementJob{}, apiserver.ErrImportSessionReverted
	}
	if err := a.Sessions.SetCategory(ctx, id, categoryID); err != nil {
		if errors.Is(err, library.ErrSessionCategoryAlreadySet) {
			return apiserver.ImportPlacementJob{}, apiserver.ErrImportSessionCategoryAlreadySet
		}
		return apiserver.ImportPlacementJob{}, err
	}
	return a.enqueuePlacement(ctx, importjob.CommitPayload{SessionID: id, Unplaced: true})
}

// CommitHeldImportUnits refuses what stops the whole call before queueing it; a per-unit failure is the job's to log,
// since the bulk commit is best-effort the same way PlaceUnplaced is.
func (a ImportSessions) CommitHeldImportUnits(
	ctx context.Context, sessionID int64, unitIDs []int64,
) (apiserver.ImportPlacementJob, error) {
	if err := a.heldCommitBuilt(ctx, sessionID, 0); err != nil {
		return apiserver.ImportPlacementJob{}, err
	}
	session, err := a.Sessions.Get(ctx, sessionID)
	if err != nil {
		return apiserver.ImportPlacementJob{}, err
	}
	switch {
	case session.State == library.ImportReverted:
		return apiserver.ImportPlacementJob{}, apiserver.ErrImportSessionReverted
	case session.CategoryID == nil:
		return apiserver.ImportPlacementJob{}, apiserver.ErrImportSessionHasNoCategory
	}
	return a.enqueuePlacement(ctx, importjob.CommitPayload{SessionID: sessionID, UnitIDs: unitIDs})
}

func (a ImportSessions) enqueuePlacement(ctx context.Context, payload importjob.CommitPayload) (apiserver.ImportPlacementJob, error) {
	result, err := a.EnqueuePlacement(ctx, payload)
	if err != nil {
		return apiserver.ImportPlacementJob{}, err
	}
	return apiserver.ImportPlacementJob{JobID: result.Job.ID, AlreadyQueued: result.Duplicate}, nil
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
	if err := a.heldCommitBuilt(ctx, sessionID, unitID); err != nil {
		return apiserver.ImportUnitRow{}, err
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
		case errors.Is(err, batch.ErrUnresolved):
			return apiserver.ImportUnitRow{}, apiserver.ErrImportUnitHasNoCandidate
		case errors.Is(err, importjob.ErrHeldCommitNotBuilt):
			return apiserver.ImportUnitRow{}, apiserver.ErrNotBuiltForMediaType
		case errors.Is(err, batch.ErrNothingToPlace):
			return apiserver.ImportUnitRow{}, fmt.Errorf("%w: every episode in this folder is already in the library, or none is an episode of that series", apiserver.ErrInvalidChoice)
		case errors.Is(err, batch.ErrGuessTooWeak):
			return apiserver.ImportUnitRow{}, fmt.Errorf("%w: the best guess is too weak to place on its own; say what it is with Identify as", apiserver.ErrInvalidChoice)
		case errors.Is(err, media.ErrInvalidAdd):
			return apiserver.ImportUnitRow{}, fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
		case errors.Is(err, tv.ErrNotASeriesID):
			return apiserver.ImportUnitRow{}, fmt.Errorf("%w: that is not a series id", apiserver.ErrInvalidChoice)
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
		WorkID: u.WorkID, EditionID: u.EditionID, Reason: u.Reason, CategoryID: u.CategoryID,
	}
	var evidence inspect.Evidence
	if u.EvidenceJSON != "" && json.Unmarshal([]byte(u.EvidenceJSON), &evidence) == nil {
		row.Evidence = importEvidenceFrom(evidence)
	}
	if candidates, err := library.UnitCandidates(u.CandidatesJSON); err == nil && len(candidates) > 0 {
		row.Candidates = importCandidatesFrom(candidates)
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
	// Settings gives the library roots; FS checks the folder; Workspace is Solarr's own working folder, never a source.
	Settings  *settings.Cache
	FS        fsx.FS
	Workspace string
}

// CheckSource refuses a folder an import must not walk: one that is missing, a library root, the folder holding a
// root (it would import the library into itself) or Solarr's own workspace.
func (a ImportInspector) CheckSource(sourcePath string) error {
	if _, err := a.FS.Stat(sourcePath); err != nil {
		return fmt.Errorf("%w: %s does not exist", apiserver.ErrInvalidChoice, sourcePath)
	}
	if a.Workspace != "" && fsx.Within(a.Workspace, sourcePath) {
		return fmt.Errorf("%w: %s is Solarr's own working folder", apiserver.ErrImportSourceRefused, sourcePath)
	}
	if a.Settings == nil {
		return nil
	}
	for _, root := range a.Settings.Snapshot().RootFolders() {
		switch {
		case filepath.Clean(root.Path) == sourcePath:
			return fmt.Errorf("%w: %s is a library root folder itself", apiserver.ErrImportSourceRefused, sourcePath)
		case fsx.Within(sourcePath, root.Path):
			return fmt.Errorf("%w: %s holds the library root folder %s, so importing from it would import the library into itself",
				apiserver.ErrImportSourceRefused, sourcePath, root.Path)
		}
	}
	return nil
}

func (a ImportInspector) InspectImport(
	ctx context.Context, sourcePath, origin string, categoryID *int64, preview, addUnverified, bestMatch bool,
) (bool, error) {
	if err := a.CheckSource(sourcePath); err != nil {
		return false, err
	}
	result, err := importjob.Enqueue(ctx, a.Queue, importjob.Payload{
		SourcePath: sourcePath, Origin: origin, CategoryID: categoryID, Preview: preview, NoUnverified: !addUnverified,
		BestMatch: bestMatch,
	})
	return result.Duplicate, err
}
