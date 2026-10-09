package apiserver

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// ExclusionRow is one removed work that is not added back, by one of its ids.
type ExclusionRow struct {
	Title, Provider, ExternalID string
	CreatedAt                   int64
}

// WorkExclusions lists and lifts the works removed with "do not add it back". Defined here, implemented by the caller.
type WorkExclusions interface {
	// List answers one entry per removed work of a media type.
	List(ctx context.Context, mediaType string) ([]ExclusionRow, error)
	// Allow lifts the exclusion of the work the id belongs to, by every id it was excluded by.
	Allow(ctx context.Context, mediaType, provider, externalID string) error
}

// ListWorkExclusions answers the removed works of a media type that are not added again. Administrator only.
func (s *Server) ListWorkExclusions(w http.ResponseWriter, r *http.Request, params gen.ListWorkExclusionsParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workExclusions == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "exclusions are not available")
		return
	}
	rows, err := s.workExclusions.List(r.Context(), params.MediaType)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.WorkExclusion, len(rows))
	for i, row := range rows {
		views[i] = gen.WorkExclusion{Title: row.Title, Provider: row.Provider, ExternalId: row.ExternalID, CreatedAt: row.CreatedAt}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func (s *Server) routeListWorkExclusions(w http.ResponseWriter, r *http.Request) {
	s.ListWorkExclusions(w, r, gen.ListWorkExclusionsParams{MediaType: r.URL.Query().Get("media_type")})
}

func (s *Server) routeRemoveWorkExclusion(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	s.RemoveWorkExclusion(w, r, gen.RemoveWorkExclusionParams{
		MediaType: query.Get("media_type"), Provider: query.Get("provider"), ExternalId: query.Get("external_id"),
	})
}

// RemoveWorkExclusion lets a removed work be added again. Administrator only.
func (s *Server) RemoveWorkExclusion(w http.ResponseWriter, r *http.Request, params gen.RemoveWorkExclusionParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.workExclusions == nil || params.MediaType == "" || params.Provider == "" || params.ExternalId == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "media_type, provider and external_id are required")
		return
	}
	if err := s.workExclusions.Allow(r.Context(), params.MediaType, params.Provider, params.ExternalId); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
