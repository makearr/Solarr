package apiserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// UnidentifiedReader lists F29's manual identification queue. Defined here and
// implemented by the caller, so this package never imports the library store
// (WorksReader's own doc comment explains the same choice).
type UnidentifiedReader interface {
	ListUnidentified(ctx context.Context, rootFolderIDs []int64, cursor paginate.Cursor, pageSize int) ([]UnidentifiedRow, error)
}

// UnidentifiedCandidate is one ranked suggestion the chain found but was not
// confident enough to resolve automatically.
type UnidentifiedCandidate struct {
	Strategy   string
	Provider   string
	ExternalID string
	Confidence float64
}

// UnidentifiedRow is one queued file.
type UnidentifiedRow struct {
	MediaObjectID int64
	RootFolderID  int64
	RelPath       string
	Candidates    []UnidentifiedCandidate
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
}

// ListUnidentified returns a page of F29's manual identification queue, oldest
// first, filtered to what the caller may see the same way ListWorks is
// (Invariant 37: a hidden category's files are excluded, never 403).
func (s *Server) ListUnidentified(w http.ResponseWriter, r *http.Request, params gen.ListUnidentifiedParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	cursor, err := paginate.Decode(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))

	if s.unidentified == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the library is not available")
		return
	}

	// Root-folder-only: an unidentified file is by definition physical (it
	// exists on disk with no work resolved yet), so it can never have a
	// desired_item.category_id -- the category half of ADR-27 visibility has
	// nothing to gate here.
	rootFolderIDs, _, err := s.visibility(access, params.CategoryId)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	rows, err := s.unidentified.ListUnidentified(r.Context(), rootFolderIDs, cursor, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildUnidentifiedPage(rows, limit))
}

// UnidentifiedResolver confirms a candidate for one queued file (section
// 21.3's manual-resolve action). Defined here and implemented by the caller,
// same reasoning as UnidentifiedReader's own doc comment.
type UnidentifiedResolver interface {
	// GetUnidentified answers one queued entry, for the visibility check
	// before resolving it. A missing id wraps sql.ErrNoRows.
	GetUnidentified(ctx context.Context, mediaObjectID int64) (UnidentifiedRow, error)
	// ResolveUnidentified fetches the candidate's real title, resolves or
	// creates the work, links the root folder and clears the queue entry;
	// wholeFolder does the same for every file queued beside it.
	ResolveUnidentified(
		ctx context.Context, mediaObjectID int64, provider, externalID string, wholeFolder bool,
	) (workID, resolved int64, err error)
	// MarkNotMedia takes a queued file out of the queue for good.
	MarkNotMedia(ctx context.Context, mediaObjectID int64) error
	// IdentificationAttempts answers a file's recorded identification runs, newest first.
	IdentificationAttempts(ctx context.Context, mediaObjectID int64) ([]IdentificationAttempt, error)
}

// IdentificationAttempt is one identification run for a file.
type IdentificationAttempt struct {
	At      time.Time
	Outcome string
	Passes  []IdentificationPass
}

// IdentificationPass is one pass of a run and the candidates it weighed.
type IdentificationPass struct {
	Pass, Outcome string
	Candidates    []IdentificationCandidate
}

// IdentificationCandidate is one candidate a pass weighed.
type IdentificationCandidate struct {
	Strategy, Provider, ExternalID string
	Confidence                     float64
	Evidence                       []IdentificationEvidence
}

// IdentificationEvidence is one fact a verdict rests on.
type IdentificationEvidence struct {
	Kind, Detail string
}

// ErrUnsupportedProvider mirrors music.ErrUnsupportedProvider without this
// package importing internal/media/music (WorksReader's own doc comment explains
// why) -- the caller's adapter translates.
var ErrUnsupportedProvider = errors.New("apiserver: unsupported provider for manual resolve")

// routeResolveUnidentified parses the mediaObjectId path parameter, mirroring
// routeGetWork.
func (s *Server) routeResolveUnidentified(w http.ResponseWriter, r *http.Request) {
	mediaObjectID, err := strconv.ParseInt(chi.URLParam(r, "mediaObjectId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a media object id must be a number")
		return
	}
	s.ResolveUnidentified(w, r, mediaObjectID)
}

// ResolveUnidentified confirms a candidate for one queued file (section 21.3).
// A file under a root folder the caller cannot read answers 404, the same as
// missing (Invariant 37).
func (s *Server) ResolveUnidentified(w http.ResponseWriter, r *http.Request, mediaObjectID int64) {
	if !s.queuedFileVisible(w, r, mediaObjectID) {
		return
	}
	var body gen.ResolveUnidentifiedJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	wholeFolder := body.WholeFolder != nil && *body.WholeFolder
	workID, resolved, err := s.unidentifiedResolver.ResolveUnidentified(
		r.Context(), mediaObjectID, body.Provider, body.ExternalId, wholeFolder)
	if err != nil {
		if errors.Is(err, ErrUnsupportedProvider) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]int64{"work_id": workID, "resolved": resolved})
}

func (s *Server) routeMarkUnidentifiedNotMedia(w http.ResponseWriter, r *http.Request) {
	mediaObjectID, err := strconv.ParseInt(chi.URLParam(r, "mediaObjectId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a media object id must be a number")
		return
	}
	s.MarkUnidentifiedNotMedia(w, r, mediaObjectID)
}

// MarkUnidentifiedNotMedia takes a queued file out of the queue for good (F29's "not media").
func (s *Server) MarkUnidentifiedNotMedia(w http.ResponseWriter, r *http.Request, mediaObjectID int64) {
	if !s.queuedFileVisible(w, r, mediaObjectID) {
		return
	}
	if err := s.unidentifiedResolver.MarkNotMedia(r.Context(), mediaObjectID); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// queuedFileVisible answers 404 for a file that is not queued or sits under a root folder the
// caller cannot read (Invariant 37); a queued file is by definition physical, so root folders decide.
func (s *Server) queuedFileVisible(w http.ResponseWriter, r *http.Request, mediaObjectID int64) bool {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	if s.unidentifiedResolver == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "manual resolution is not available")
		return false
	}
	entry, err := s.unidentifiedResolver.GetUnidentified(r.Context(), mediaObjectID)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such queued file")
		return false
	}
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	rootFolderIDs, _, err := s.visibility(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	if !sharesAnID(rootFolderIDs, []int64{entry.RootFolderID}) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such queued file")
		return false
	}
	return true
}

func buildUnidentifiedPage(rows []UnidentifiedRow, limit int) gen.UnidentifiedPage {
	items := rows
	var nextCursor string
	if len(rows) > limit {
		items = rows[:limit]
		last := items[len(items)-1]
		nextCursor = paginate.Cursor{Sort: last.FirstSeenAt.Unix(), ID: last.MediaObjectID}.Encode()
	}

	views := make([]gen.UnidentifiedFile, len(items))
	for index, row := range items {
		views[index] = gen.UnidentifiedFile{
			MediaObjectId: row.MediaObjectID, RootFolderId: row.RootFolderID, RelPath: row.RelPath,
			Candidates:  toCandidateViews(row.Candidates),
			FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
		}
	}

	page := gen.UnidentifiedPage{Items: views}
	if nextCursor != "" {
		page.NextCursor = &nextCursor
	}
	return page
}

func toCandidateViews(from []UnidentifiedCandidate) []gen.UnidentifiedCandidate {
	views := make([]gen.UnidentifiedCandidate, len(from))
	for index, candidate := range from {
		view := gen.UnidentifiedCandidate{Strategy: candidate.Strategy, Confidence: float32(candidate.Confidence)}
		if candidate.Provider != "" {
			view.Provider = &candidate.Provider
		}
		if candidate.ExternalID != "" {
			view.ExternalId = &candidate.ExternalID
		}
		views[index] = view
	}
	return views
}

// routeListIdentificationAttempts parses the mediaObjectId path parameter, mirroring routeResolveUnidentified.
func (s *Server) routeListIdentificationAttempts(w http.ResponseWriter, r *http.Request) {
	mediaObjectID, err := strconv.ParseInt(chi.URLParam(r, "mediaObjectId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a media object id must be a number")
		return
	}
	s.ListIdentificationAttempts(w, r, mediaObjectID)
}

// ListIdentificationAttempts answers how identification of a queued file went. A file under a
// root folder the caller cannot read answers 404, the same as missing (Invariant 37).
func (s *Server) ListIdentificationAttempts(w http.ResponseWriter, r *http.Request, mediaObjectID int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.unidentifiedResolver == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "identification history is not available")
		return
	}
	entry, err := s.unidentifiedResolver.GetUnidentified(r.Context(), mediaObjectID)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such queued file")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	rootFolderIDs, _, err := s.visibility(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !sharesAnID(rootFolderIDs, []int64{entry.RootFolderID}) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such queued file")
		return
	}

	attempts, err := s.unidentifiedResolver.IdentificationAttempts(r.Context(), mediaObjectID)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.IdentificationAttempt, len(attempts))
	for i, attempt := range attempts {
		passes := make([]gen.IdentificationPass, len(attempt.Passes))
		for j, pass := range attempt.Passes {
			candidates := make([]gen.IdentificationCandidate, len(pass.Candidates))
			for k, candidate := range pass.Candidates {
				view := gen.IdentificationCandidate{
					Strategy: candidate.Strategy, Confidence: float32(candidate.Confidence),
					Evidence: toEvidenceViews(candidate.Evidence),
				}
				if candidate.Provider != "" {
					view.Provider = &candidate.Provider
				}
				if candidate.ExternalID != "" {
					view.ExternalId = &candidate.ExternalID
				}
				candidates[k] = view
			}
			passes[j] = gen.IdentificationPass{Pass: pass.Pass, Outcome: pass.Outcome, Candidates: candidates}
		}
		views[i] = gen.IdentificationAttempt{
			AttemptedAt: attempt.At, Outcome: gen.IdentificationAttemptOutcome(attempt.Outcome), Passes: passes,
		}
	}
	file := gen.UnidentifiedFile{
		MediaObjectId: entry.MediaObjectID, RootFolderId: entry.RootFolderID, RelPath: entry.RelPath,
		Candidates: toCandidateViews(entry.Candidates), FirstSeenAt: entry.FirstSeenAt, LastSeenAt: entry.LastSeenAt,
	}
	writeJSON(w, s.logger, http.StatusOK, gen.IdentificationAttemptList{File: file, Items: views})
}

func toEvidenceViews(from []IdentificationEvidence) []gen.IdentificationEvidence {
	views := make([]gen.IdentificationEvidence, len(from))
	for i, evidence := range from {
		views[i] = gen.IdentificationEvidence{Kind: evidence.Kind, Detail: evidence.Detail}
	}
	return views
}
