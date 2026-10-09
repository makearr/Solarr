package apiserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// ErrNotBuiltForMediaType is a feature the media type has no engine for yet; answered as 409 with the reason.
var ErrNotBuiltForMediaType = errors.New("apiserver: not built for this media type")

// ErrInvalidChoice is a value the request named that the series cannot take (an unknown monitor option, a profile of
// another media type); answered as 400 with the reason.
var ErrInvalidChoice = errors.New("apiserver: invalid choice")

// ErrSeriesNotFound is a series id that is not in the library asked about; answered as 404.
var ErrSeriesNotFound = errors.New("apiserver: series not found")

// ErrSeriesSourceUnavailable means no metadata provider could be reached; answered as 502.
var ErrSeriesSourceUnavailable = errors.New("apiserver: the metadata sources could not be reached")

// SeriesCatalog is a series' poster choice until pictures are chosen one way for every type (R1.7), implemented by the
// caller. Every method is scoped to one library the caller was checked for.
type SeriesCatalog interface {
	// PosterOptions lists the posters the providers hold for a series.
	PosterOptions(ctx context.Context, categoryID, seriesID int64) ([]ImageOptionRow, error)
	// SetPoster makes one of the offered posters the series' own; ErrImageNotOffered for any other address.
	SetPoster(ctx context.Context, categoryID, seriesID int64, url string) error
}

// seriesAccess checks the caller against one library and the catalogue being wired; false means it answered.
func (s *Server) seriesAccess(w http.ResponseWriter, r *http.Request, categoryID int64, need settings.Permission) (authz.Access, bool) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return authz.Access{}, false
	}
	category, err := access.Category(categoryID, need)
	if err != nil {
		writeError(w, s.logger, err)
		return authz.Access{}, false
	}
	if category.MediaType != "tv" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "this library is not a TV library")
		return authz.Access{}, false
	}
	if s.series == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "series are not available")
		return authz.Access{}, false
	}
	return access, true
}

// seriesPath reads the series id and the library from a request that carries both.
func (s *Server) seriesPath(w http.ResponseWriter, r *http.Request) (seriesID, categoryID int64, ok bool) {
	seriesID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil {
		categoryID, err = strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	}
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "id and category_id must be numbers")
		return 0, 0, false
	}
	return seriesID, categoryID, true
}

func (s *Server) routeGetSeriesPosters(w http.ResponseWriter, r *http.Request) {
	id, category, ok := s.seriesPath(w, r)
	if !ok {
		return
	}
	s.getSeriesPosters(w, r, id, category)
}

// GetSeriesPosters satisfies the generated interface; routeGetSeriesPosters is what is mounted.
func (s *Server) GetSeriesPosters(w http.ResponseWriter, r *http.Request, id int64, params gen.GetSeriesPostersParams) {
	s.getSeriesPosters(w, r, id, params.CategoryId)
}

func (s *Server) getSeriesPosters(w http.ResponseWriter, r *http.Request, id, categoryID int64) {
	if _, ok := s.seriesAccess(w, r, categoryID, settings.PermissionRead); !ok {
		return
	}
	options, err := s.series.PosterOptions(r.Context(), categoryID, id)
	if errors.Is(err, ErrSeriesNotFound) {
		writeError(w, s.logger, err)
		return
	}
	s.writeImageOptions(w, options, err)
}

func (s *Server) routeSetSeriesPoster(w http.ResponseWriter, r *http.Request) {
	id, category, ok := s.seriesPath(w, r)
	if !ok {
		return
	}
	s.setSeriesPoster(w, r, id, category)
}

// SetSeriesPoster satisfies the generated interface; routeSetSeriesPoster is what is mounted.
func (s *Server) SetSeriesPoster(w http.ResponseWriter, r *http.Request, id int64, params gen.SetSeriesPosterParams) {
	s.setSeriesPoster(w, r, id, params.CategoryId)
}

func (s *Server) setSeriesPoster(w http.ResponseWriter, r *http.Request, id, categoryID int64) {
	if _, ok := s.seriesAccess(w, r, categoryID, settings.PermissionManage); !ok {
		return
	}
	url, ok := s.decodeImagePick(w, r)
	if !ok {
		return
	}
	err := s.series.SetPoster(r.Context(), categoryID, id, url)
	if errors.Is(err, ErrSeriesNotFound) {
		writeError(w, s.logger, err)
		return
	}
	s.writeImagePicked(w, err)
}
