package apiserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/identify/model"
)

// ErrWorkNotWanted reports a search attempted against a work with no
// album-level desired item -- nothing is monitoring it, so there is nothing
// to search for yet (section 19.6's own "wanted" is a precondition here,
// not something a search creates).
var ErrWorkNotWanted = errors.New("apiserver: work is not currently wanted")

// ErrNoIndexersConfigured reports a search with no enabled indexer to run
// it against.
var ErrNoIndexersConfigured = errors.New("apiserver: no indexer is configured and enabled")

// CandidateDecision is one release decided against a specific desired item
// (section 10.1/12). Components/Rejections/Deferrals reuse ADR-10's own
// vocabulary (internal/identify/model, already an allowed apiserver
// dependency via score.go's own Decision) rather than declaring a
// duplicate shape.
type CandidateDecision struct {
	ID              int64
	CandidateID     int64
	Outcome         string
	Score           float64
	Components      []model.ScoreComponent
	Rejections      []model.Rejection
	Deferrals       []model.Deferral
	MatchConfidence *float64
	IndexerID       int64
	GUID            string
	Title           string
	SizeBytes       *int64
	Seeders         *int64
	Leechers        *int64
	PublishedAt     *time.Time
	Protocol        string
	DownloadURL     string
	Source          string
	DecidedAt       time.Time
}

// IndexerSearchError records one indexer's own search failure within a run.
type IndexerSearchError struct {
	IndexerID int64
	Error     string
}

// SearchItemResult is one work's own search run.
type SearchItemResult struct {
	TierUsed      *int
	Decisions     []CandidateDecision
	IndexerErrors []IndexerSearchError
}

// CandidateSearcher is stage 5's own candidate pipeline, exposed to the web
// interface (todo 5.2's "manual search, per-item and per-category").
type CandidateSearcher interface {
	SearchWork(ctx context.Context, workID int64) (SearchItemResult, error)
	ListWorkCandidates(ctx context.Context, workID int64) ([]CandidateDecision, error)
	// SearchCategory enqueues a search run; duplicate reports whether one
	// was already running for this category.
	SearchCategory(ctx context.Context, categoryID int64) (duplicate bool, err error)
}

// routeSearchWork parses the id path parameter.
func (s *Server) routeSearchWork(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a work id must be a number")
	if err != nil {
		return
	}
	s.SearchWork(w, r, id)
}

// SearchWork runs stage 5's candidate pipeline against one work's own
// wanted album. Any signed-in user may trigger it -- the same access level
// ArrSync's own sync already keeps.
func (s *Server) SearchWork(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	result, err := s.candidateSearcher.SearchWork(r.Context(), id)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, searchItemResultView(result))
}

// routeListWorkCandidates parses the id path parameter.
func (s *Server) routeListWorkCandidates(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a work id must be a number")
	if err != nil {
		return
	}
	s.ListWorkCandidates(w, r, id)
}

// ListWorkCandidates answers every decision ever recorded against this
// work's own wanted album, newest first -- empty, never 404, for a work
// nobody has searched.
func (s *Server) ListWorkCandidates(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	decisions, err := s.candidateSearcher.ListWorkCandidates(r.Context(), id)
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

// routeSearchCategory parses the id path parameter.
func (s *Server) routeSearchCategory(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a category id must be a number")
	if err != nil {
		return
	}
	s.SearchCategory(w, r, id)
}

// SearchCategory enqueues a search run across every monitored, incomplete
// item in one category.
func (s *Server) SearchCategory(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	duplicate, err := s.candidateSearcher.SearchCategory(r.Context(), id)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.CategorySearchEnqueued{Duplicate: duplicate})
}

// parsePathID is the shared "{id} must be a number" parse this file's three
// routes all need.
func parsePathID(w http.ResponseWriter, s *Server, r *http.Request, param, message string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", message)
		return 0, err
	}
	return id, nil
}

func writeCandidateSearchError(w http.ResponseWriter, s *Server, err error) {
	switch {
	case errors.Is(err, ErrWorkNotWanted):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Not wanted", "this work has no album-level desired item to search for")
	case errors.Is(err, ErrNoIndexersConfigured):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"No indexers", "no indexer is configured and enabled")
	default:
		writeError(w, s.logger, err)
	}
}

func searchItemResultView(result SearchItemResult) gen.SearchItemResult {
	decisions := make([]gen.CandidateDecision, len(result.Decisions))
	for index, decision := range result.Decisions {
		decisions[index] = candidateDecisionView(decision)
	}
	indexerErrors := make([]gen.IndexerSearchError, len(result.IndexerErrors))
	for index, indexerError := range result.IndexerErrors {
		indexerErrors[index] = gen.IndexerSearchError{IndexerId: indexerError.IndexerID, Error: indexerError.Error}
	}
	return gen.SearchItemResult{TierUsed: result.TierUsed, Decisions: decisions, IndexerErrors: indexerErrors}
}

func candidateDecisionView(decision CandidateDecision) gen.CandidateDecision {
	components := make([]gen.ScoreComponent, len(decision.Components))
	for index, component := range decision.Components {
		components[index] = gen.ScoreComponent{
			Rule: component.Rule, Label: component.Label, Delta: float32(component.Delta),
		}
	}
	rejections := make([]gen.Rejection, len(decision.Rejections))
	for index, rejection := range decision.Rejections {
		rejections[index] = gen.Rejection{
			Rule: rejection.Rule, Field: rejection.Field, Wanted: rejection.Wanted, Got: rejection.Got,
		}
	}
	deferrals := make([]gen.Deferral, len(decision.Deferrals))
	for index, deferral := range decision.Deferrals {
		deferrals[index] = gen.Deferral{Reason: deferral.Reason}
	}

	view := gen.CandidateDecision{
		Id: decision.ID, CandidateId: decision.CandidateID,
		Outcome: gen.CandidateDecisionOutcome(decision.Outcome), Score: float32(decision.Score),
		Components: components, Rejections: rejections, Deferrals: deferrals,
		IndexerId: decision.IndexerID, Guid: decision.GUID, Title: decision.Title,
		Protocol: gen.CandidateDecisionProtocol(decision.Protocol), DownloadUrl: decision.DownloadURL,
		Source: gen.CandidateDecisionSource(decision.Source), DecidedAt: decision.DecidedAt,
	}
	if decision.MatchConfidence != nil {
		confidence := float32(*decision.MatchConfidence)
		view.MatchConfidence = &confidence
	}
	view.SizeBytes = decision.SizeBytes
	view.Seeders = decision.Seeders
	view.Leechers = decision.Leechers
	view.PublishedAt = decision.PublishedAt
	return view
}
