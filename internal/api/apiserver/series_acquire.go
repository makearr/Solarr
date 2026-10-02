package apiserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

// SeasonAcquirer is a TV season's search and download, implemented by the caller beside SeriesCatalog. Every method
// is scoped to one library the caller was checked for.
type SeasonAcquirer interface {
	// Search is the interactive search: decisions are recorded, nothing is sent.
	Search(ctx context.Context, categoryID, seriesID int64, season int) (SearchItemResult, error)
	// Grab is the automatic search: search, then send the best accepted release.
	Grab(ctx context.Context, categoryID, seriesID int64, season int) (GrabResult, error)
	Candidates(ctx context.Context, categoryID, seriesID int64, season int) ([]CandidateDecision, error)
	Send(ctx context.Context, categoryID, seriesID int64, season int, decisionID int64) (Acquisition, error)
}

// seasonRequest reads a season route's three identifiers and checks the caller against the library.
func (s *Server) seasonRequest(w http.ResponseWriter, r *http.Request) (seriesID, categoryID int64, season int, ok bool) {
	seriesID, categoryID, ok = s.seriesPath(w, r)
	if !ok {
		return 0, 0, 0, false
	}
	season, err := strconv.Atoi(chi.URLParam(r, "season"))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "season must be a number")
		return 0, 0, 0, false
	}
	if _, ok := s.seriesAccess(w, r, categoryID, settings.PermissionRequest); !ok {
		return 0, 0, 0, false
	}
	if s.seasons == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "searching is not available")
		return 0, 0, 0, false
	}
	return seriesID, categoryID, season, true
}

func (s *Server) routeSearchSeason(w http.ResponseWriter, r *http.Request) {
	id, category, season, ok := s.seasonRequest(w, r)
	if !ok {
		return
	}
	s.searchSeason(w, r, id, category, season)
}

// SearchSeason satisfies the generated interface; routeSearchSeason is what is mounted.
func (s *Server) SearchSeason(w http.ResponseWriter, r *http.Request, id int64, season int, params gen.SearchSeasonParams) {
	s.searchSeason(w, r, id, params.CategoryId, season)
}

func (s *Server) searchSeason(w http.ResponseWriter, r *http.Request, id, categoryID int64, season int) {
	result, err := s.seasons.Search(r.Context(), categoryID, id, season)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, searchItemResultView(result))
}

func (s *Server) routeGrabSeason(w http.ResponseWriter, r *http.Request) {
	id, category, season, ok := s.seasonRequest(w, r)
	if !ok {
		return
	}
	s.grabSeason(w, r, id, category, season)
}

// GrabSeason satisfies the generated interface; routeGrabSeason is what is mounted.
func (s *Server) GrabSeason(w http.ResponseWriter, r *http.Request, id int64, season int, params gen.GrabSeasonParams) {
	s.grabSeason(w, r, id, params.CategoryId, season)
}

func (s *Server) grabSeason(w http.ResponseWriter, r *http.Request, id, categoryID int64, season int) {
	result, err := s.seasons.Grab(r.Context(), categoryID, id, season)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	view := gen.GrabResult{Found: result.Found, AlreadyDownloading: result.AlreadyDownloading, Held: &result.Held, Refused: &result.Refused}
	if result.Sent != nil {
		sent := acquisitionView(*result.Sent)
		view.Sent = &sent
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

func (s *Server) routeListSeasonCandidates(w http.ResponseWriter, r *http.Request) {
	id, category, season, ok := s.seasonRequest(w, r)
	if !ok {
		return
	}
	s.listSeasonCandidates(w, r, id, category, season)
}

// ListSeasonCandidates satisfies the generated interface; routeListSeasonCandidates is what is mounted.
func (s *Server) ListSeasonCandidates(w http.ResponseWriter, r *http.Request, id int64, season int, params gen.ListSeasonCandidatesParams) {
	s.listSeasonCandidates(w, r, id, params.CategoryId, season)
}

func (s *Server) listSeasonCandidates(w http.ResponseWriter, r *http.Request, id, categoryID int64, season int) {
	decisions, err := s.seasons.Candidates(r.Context(), categoryID, id, season)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	views := make([]gen.CandidateDecision, len(decisions))
	for index, decision := range decisions {
		views[index] = candidateDecisionView(decision)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func (s *Server) routeSendSeasonCandidate(w http.ResponseWriter, r *http.Request) {
	id, category, season, ok := s.seasonRequest(w, r)
	if !ok {
		return
	}
	decisionID, err := strconv.ParseInt(chi.URLParam(r, "decision_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a decision id must be a number")
		return
	}
	s.sendSeasonCandidate(w, r, id, category, season, decisionID)
}

// SendSeasonCandidate satisfies the generated interface; routeSendSeasonCandidate is what is mounted.
func (s *Server) SendSeasonCandidate(w http.ResponseWriter, r *http.Request, id int64, season int, decisionID int64, params gen.SendSeasonCandidateParams) {
	s.sendSeasonCandidate(w, r, id, params.CategoryId, season, decisionID)
}

func (s *Server) sendSeasonCandidate(w http.ResponseWriter, r *http.Request, id, categoryID int64, season int, decisionID int64) {
	acquisition, err := s.seasons.Send(r.Context(), categoryID, id, season, decisionID)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, acquisitionView(acquisition))
}
