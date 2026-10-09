package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
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

// SeriesCatalog is the TV screens' data, implemented by the caller so this package never imports the library store or
// a provider (the same separation WorksReader keeps). Every method is scoped to one library the caller was checked for.
type SeriesCatalog interface {
	// List returns up to limit+1 series after the cursor, so the caller can tell whether a page follows.
	List(ctx context.Context, categoryID int64, after paginate.TextCursor, limit int) ([]gen.SeriesSummary, error)
	Get(ctx context.Context, categoryID, seriesID int64) (gen.SeriesDetail, error)
	Lookup(ctx context.Context, categoryID int64, query string, limit int) ([]gen.SeriesCandidate, error)
	Add(ctx context.Context, userID int64, request gen.AddSeriesRequest) (gen.AddSeriesResult, error)
	Refresh(ctx context.Context, categoryID, seriesID int64) (gen.AddSeriesResult, error)
	// Delete removes a series; deleteFiles sends its files to the recycle bin, exclude remembers it so it is not added back.
	Delete(ctx context.Context, categoryID, seriesID int64, deleteFiles, exclude bool) error
	// SetType says what kind of series this is; "auto" returns to what the providers say.
	SetType(ctx context.Context, categoryID, seriesID int64, seriesType string) error
	// SetProfile gives the series a quality profile of its own, or the library's when nil.
	SetProfile(ctx context.Context, categoryID, seriesID int64, profileID *int64) error
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

func (s *Server) routeListSeries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	categoryID, err := strconv.ParseInt(query.Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id is required and must be a number")
		return
	}
	params := gen.ListSeriesParams{CategoryId: categoryID}
	if raw := query.Get("cursor"); raw != "" {
		params.Cursor = &raw
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListSeries(w, r, params)
}

// ListSeries answers one page of a TV library's series.
func (s *Server) ListSeries(w http.ResponseWriter, r *http.Request, params gen.ListSeriesParams) {
	if _, ok := s.seriesAccess(w, r, params.CategoryId, settings.PermissionRead); !ok {
		return
	}
	cursor, err := paginate.DecodeText(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	rows, err := s.series.List(r.Context(), params.CategoryId, cursor, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.SeriesPage{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		last := page.Items[len(page.Items)-1]
		next := paginate.TextCursor{Sort: last.SortTitle, ID: last.Id}.Encode()
		page.NextCursor = &next
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

func (s *Server) routeLookupSeries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	categoryID, err := strconv.ParseInt(query.Get("category_id"), 10, 64)
	if err != nil || query.Get("q") == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id and q are required")
		return
	}
	params := gen.LookupSeriesParams{CategoryId: categoryID, Q: query.Get("q")}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.LookupSeries(w, r, params)
}

// LookupSeries searches the metadata sources for a series to add.
func (s *Server) LookupSeries(w http.ResponseWriter, r *http.Request, params gen.LookupSeriesParams) {
	if _, ok := s.seriesAccess(w, r, params.CategoryId, settings.PermissionRead); !ok {
		return
	}
	if len([]rune(params.Q)) < 2 {
		writeError(w, s.logger, ErrQueryTooShort)
		return
	}
	candidates, err := s.series.Lookup(r.Context(), params.CategoryId, params.Q, min(derefOr(params.Limit, 20), 50))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, candidates)
}

// AddSeries adds a series to a TV library; adding one the library holds refreshes it.
func (s *Server) AddSeries(w http.ResponseWriter, r *http.Request) {
	var body gen.AddSeriesRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	if body.ExternalId == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "external_id is required")
		return
	}
	access, ok := s.seriesAccess(w, r, body.CategoryId, settings.PermissionManage)
	if !ok {
		return
	}
	result, err := s.series.Add(r.Context(), access.UserID(), body)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, result)
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

func (s *Server) routeGetSeries(w http.ResponseWriter, r *http.Request) {
	if id, category, ok := s.seriesPath(w, r); ok {
		s.GetSeries(w, r, id, gen.GetSeriesParams{CategoryId: category})
	}
}

// GetSeries answers one series with its seasons and episodes.
func (s *Server) GetSeries(w http.ResponseWriter, r *http.Request, id int64, params gen.GetSeriesParams) {
	if _, ok := s.seriesAccess(w, r, params.CategoryId, settings.PermissionRead); !ok {
		return
	}
	detail, err := s.series.Get(r.Context(), params.CategoryId, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, detail)
}

func (s *Server) routeRefreshSeries(w http.ResponseWriter, r *http.Request) {
	if id, category, ok := s.seriesPath(w, r); ok {
		s.RefreshSeries(w, r, id, gen.RefreshSeriesParams{CategoryId: category})
	}
}

// RefreshSeries re-reads a series from the metadata sources.
func (s *Server) RefreshSeries(w http.ResponseWriter, r *http.Request, id int64, params gen.RefreshSeriesParams) {
	if _, ok := s.seriesAccess(w, r, params.CategoryId, settings.PermissionManage); !ok {
		return
	}
	result, err := s.series.Refresh(r.Context(), params.CategoryId, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, result)
}

func (s *Server) routeSetSeriesQuality(w http.ResponseWriter, r *http.Request) {
	id, category, ok := s.seriesPath(w, r)
	if !ok {
		return
	}
	s.setSeriesQuality(w, r, id, category)
}

// SetSeriesQuality satisfies the generated interface; routeSetSeriesQuality is what is mounted.
func (s *Server) SetSeriesQuality(w http.ResponseWriter, r *http.Request, id int64, params gen.SetSeriesQualityParams) {
	s.setSeriesQuality(w, r, id, params.CategoryId)
}

func (s *Server) setSeriesQuality(w http.ResponseWriter, r *http.Request, id, categoryID int64) {
	if _, ok := s.seriesAccess(w, r, categoryID, settings.PermissionManage); !ok {
		return
	}
	var body struct {
		QualityProfileID *int64 `json:"quality_profile_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	if err := s.series.SetProfile(r.Context(), categoryID, id, body.QualityProfileID); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) routeDeleteSeries(w http.ResponseWriter, r *http.Request) {
	id, category, ok := s.seriesPath(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	params := gen.DeleteSeriesParams{CategoryId: category}
	for name, target := range map[string]**bool{"delete_files": &params.DeleteFiles, "exclude": &params.Exclude} {
		if raw := query.Get(name); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", name+" must be true or false")
				return
			}
			*target = &value
		}
	}
	s.DeleteSeries(w, r, id, params)
}

// DeleteSeries removes a series from a TV library, its files going to the recycle bin when asked.
func (s *Server) DeleteSeries(w http.ResponseWriter, r *http.Request, id int64, params gen.DeleteSeriesParams) {
	if _, ok := s.seriesAccess(w, r, params.CategoryId, settings.PermissionManage); !ok {
		return
	}
	if err := s.series.Delete(r.Context(), params.CategoryId, id, derefOr(params.DeleteFiles, false), derefOr(params.Exclude, true)); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) routeSetSeriesType(w http.ResponseWriter, r *http.Request) {
	id, category, ok := s.seriesPath(w, r)
	if !ok {
		return
	}
	s.setSeriesType(w, r, id, category)
}

// SetSeriesType satisfies the generated interface; routeSetSeriesType is what is mounted.
func (s *Server) SetSeriesType(w http.ResponseWriter, r *http.Request, id int64, params gen.SetSeriesTypeParams) {
	s.setSeriesType(w, r, id, params.CategoryId)
}

func (s *Server) setSeriesType(w http.ResponseWriter, r *http.Request, id, categoryID int64) {
	if _, ok := s.seriesAccess(w, r, categoryID, settings.PermissionManage); !ok {
		return
	}
	var body struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil || body.Type == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "type is required")
		return
	}
	if err := s.series.SetType(r.Context(), categoryID, id, body.Type); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
