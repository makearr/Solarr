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

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/paginate"
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
	// creates the work, links the root folder and clears the queue entry.
	ResolveUnidentified(ctx context.Context, mediaObjectID int64, provider, externalID string) (workID int64, err error)
}

// ErrUnsupportedProvider mirrors music.ErrUnsupportedProvider without this
// package importing internal/music (WorksReader's own doc comment explains
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
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.unidentifiedResolver == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "manual resolution is not available")
		return
	}

	var body gen.ResolveUnidentifiedJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
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
	// Root-folder-only, the same reasoning ListUnidentified's own comment
	// gives: a queued file is by definition physical.
	rootFolderIDs, _, err := s.visibility(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !sharesAnID(rootFolderIDs, []int64{entry.RootFolderID}) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such queued file")
		return
	}

	workID, err := s.unidentifiedResolver.ResolveUnidentified(r.Context(), mediaObjectID, body.Provider, body.ExternalId)
	if err != nil {
		if errors.Is(err, ErrUnsupportedProvider) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]int64{"work_id": workID})
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
