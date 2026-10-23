package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// ErrDiscoverUnsupportedMediaType reports a library whose media type cannot add, or a candidate it cannot add.
var ErrDiscoverUnsupportedMediaType = errors.New("apiserver: no search provider for this category's media type")

// AddCandidateRow is one thing a lookup found.
type AddCandidateRow struct {
	Kind, Provider, ExternalID, Title     string
	Subtitle, Overview, PosterURL, Status string
	Year                                  int64
	// LibraryID is the work or person already held for it, 0 when new.
	LibraryID int64
}

// AddWorkRequest is one add a person asked for; Kind empty is the type's first.
type AddWorkRequest struct {
	CategoryID, UserID         int64
	Kind, Provider, ExternalID string
	Monitored                  bool
	MonitorOption              string
	Specials                   bool
	ProfileID                  *int64
	Search                     bool
}

// AddWorkResult is what an add stored; Searching says the type started looking for it.
type AddWorkResult struct {
	Kind             string
	WorkID, PersonID int64
	Searching        bool
}

// WorkAdder looks works up at a library's type's sources and adds one (ADR-56's per-library half). Defined here and
// implemented by the caller: ErrQueryTooShort under the floor, ErrDiscoverUnsupportedMediaType for a type that
// cannot add, ErrInvalidChoice for a kind the type does not have.
type WorkAdder interface {
	Lookup(ctx context.Context, categoryID int64, kind, query string, limit int) ([]AddCandidateRow, error)
	Add(ctx context.Context, request AddWorkRequest) (AddWorkResult, error)
}

func (s *Server) routeLookupWorks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	categoryID, err := strconv.ParseInt(query.Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id is required and must be a number")
		return
	}
	params := gen.LookupWorksParams{CategoryId: categoryID, Q: query.Get("q")}
	if raw := query.Get("kind"); raw != "" {
		params.Kind = &raw
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.LookupWorks(w, r, params)
}

// LookupWorks answers a library's type's sources; read on the library.
func (s *Server) LookupWorks(w http.ResponseWriter, r *http.Request, params gen.LookupWorksParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(params.CategoryId, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workAdder == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "adding is not available")
		return
	}
	limit := min(derefOr(params.Limit, 20), 100)
	found, err := s.workAdder.Lookup(r.Context(), params.CategoryId, derefOr(params.Kind, ""), params.Q, max(limit, 1))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.AddCandidate, len(found))
	for i, candidate := range found {
		views[i] = gen.AddCandidate{
			Kind: candidate.Kind, Provider: candidate.Provider, ExternalId: candidate.ExternalID, Title: candidate.Title,
			Subtitle: strPtr(candidate.Subtitle), Overview: strPtr(candidate.Overview), PosterUrl: strPtr(candidate.PosterURL),
			Status: strPtr(candidate.Status),
		}
		if candidate.Year > 0 {
			views[i].Year = &candidate.Year
		}
		if candidate.LibraryID != 0 {
			views[i].LibraryId = &candidate.LibraryID
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// AddWork adds a candidate to a library as intent, never a download; manage on the library, as adding always needed.
func (s *Server) AddWork(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body gen.AddWorkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	if body.Provider == "" || body.ExternalId == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "provider and external_id are required")
		return
	}
	if _, err := access.Category(body.CategoryId, settings.PermissionManage); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workAdder == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "adding is not available")
		return
	}
	result, err := s.workAdder.Add(r.Context(), AddWorkRequest{
		CategoryID: body.CategoryId, UserID: access.UserID(), Kind: derefOr(body.Kind, ""), Provider: body.Provider,
		ExternalID: body.ExternalId, Monitored: derefOr(body.Monitored, true), MonitorOption: derefOr(body.MonitorOption, ""),
		Specials: derefOr(body.Specials, false), ProfileID: body.QualityProfileId, Search: derefOr(body.Search, false),
	})
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.AddWorkResult{Kind: result.Kind, Searching: result.Searching}
	if result.WorkID != 0 {
		view.WorkId = &result.WorkID
	}
	if result.PersonID != 0 {
		view.PersonId = &result.PersonID
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}
