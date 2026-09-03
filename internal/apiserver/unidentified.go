package apiserver

import (
	"context"
	"net/http"
	"time"

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

	rootFolderIDs, err := s.visibleRootFolders(access, params.CategoryId)
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
