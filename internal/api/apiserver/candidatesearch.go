package apiserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
)

// ErrWorkNotWanted reports a search attempted against a work with no
// album-level desired item -- nothing is monitoring it, so there is nothing
// to search for yet (section 19.6's own "wanted" is a precondition here,
// not something a search creates).
var ErrWorkNotWanted = errors.New("apiserver: work is not currently wanted")

// ErrNoIndexersConfigured reports a search with no enabled indexer to run
// it against.
var ErrNoIndexersConfigured = errors.New("apiserver: no indexer is configured and enabled")

// ErrDecisionNotAccepted reports a dispatch attempted against a REJECT/DEFER
// decision -- ADR-57's acquire.send only ever fires on an ACCEPT.
var ErrDecisionNotAccepted = errors.New("apiserver: decision is not ACCEPT")

// ErrContentBlocklisted is a release whose content failed before under another listing.
var ErrContentBlocklisted = errors.New("apiserver: the same content failed before")

// ErrDownloadClientRefused is the client declining the release it was sent; the wrapped text says why.
var ErrDownloadClientRefused = errors.New("apiserver: the download client refused the release")

// ErrNoDownloaderForProtocol reports no enabled downloader matching a
// candidate's own protocol.
var ErrNoDownloaderForProtocol = errors.New("apiserver: no downloader configured for this candidate's protocol")

// ErrAcquisitionAlreadyInFlight reports a desired item that already has a
// non-terminal acquisition (Invariant 15).
var ErrAcquisitionAlreadyInFlight = errors.New("apiserver: this item already has an acquisition in flight")

// ErrCandidateDecisionNotFound reports a decision id that does not belong
// to the work it was asked to dispatch from.
var ErrCandidateDecisionNotFound = errors.New("apiserver: no such decision for this work")

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
	Source          string
	DecidedAt       time.Time
	// IndexerName is empty when the indexer has since been removed.
	IndexerName string
	Blocklisted bool
	// NoClient means no enabled download client takes the release's protocol.
	NoClient bool
}

// SearchTerms are a person's own artist and album, searched instead of the work's own.
type SearchTerms struct {
	Artist, Album string
}

// IndexerSearchError records one indexer's own search failure within a run.
type IndexerSearchError struct {
	IndexerID   int64
	IndexerName string
	Error       string
	TooSlow     bool
}

// SearchItemResult is one work's own search run.
type SearchItemResult struct {
	TierUsed      *int
	Decisions     []CandidateDecision
	IndexerErrors []IndexerSearchError
}

// Acquisition is one dispatch to a download client (ADR-57, section 12).
type Acquisition struct {
	ID                int64
	DownloaderID      int64
	DownloadClientRef string
	ClientCategory    string
	Protocol          string
	State             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CandidateSearcher is stage 5's own candidate pipeline, exposed to the web
// interface (todo 5.2's "manual search, per-item and per-category").
type CandidateSearcher interface {
	// SearchWork searches the work's own terms, or terms when a person gave different ones.
	SearchWork(ctx context.Context, workID int64, terms *SearchTerms) (SearchItemResult, error)
	// SearchTrack searches for one track on its own, wanting it first if
	// nothing did.
	SearchTrack(ctx context.Context, workID, partID int64) (SearchItemResult, error)
	ListWorkCandidates(ctx context.Context, workID int64) ([]CandidateDecision, error)
	// SearchCategory enqueues an automatic search of the library, or of workIDs only when given;
	// duplicate reports whether the same run was already queued.
	SearchCategory(ctx context.Context, categoryID int64, workIDs []int64) (duplicate bool, err error)
	// GrabWork is the automatic search of one album: search, then send the best release.
	GrabWork(ctx context.Context, workID int64) (GrabResult, error)
	// SendCandidate is ADR-57's own acquire.send: dispatch decisionID (which
	// must belong to workID and be ACCEPT) to a matching downloader.
	SendCandidate(ctx context.Context, workID, decisionID int64) (Acquisition, error)
	// BlocklistCandidate never offers decisionID's release again (section 10.11).
	BlocklistCandidate(ctx context.Context, workID, decisionID int64) error
}

// routeSearchWork parses the id path parameter.
func (s *Server) routeSearchWork(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a work id must be a number")
	if err != nil {
		return
	}
	s.SearchWork(w, r, id)
}

// SearchWork runs stage 5's candidate pipeline against one work's own wanted album. It can send
// a download, so it needs request on the album's library, as the library-wide search does.
func (s *Server) SearchWork(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.workPermitted(w, r, access, id, settings.PermissionRequest) {
		return
	}
	var terms *SearchTerms
	if r.ContentLength != 0 {
		var body gen.SearchWorkJSONRequestBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil ||
			strings.TrimSpace(body.Artist) == "" || strings.TrimSpace(body.Album) == "" {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "different terms need both an artist and an album")
			return
		}
		terms = &SearchTerms{Artist: strings.TrimSpace(body.Artist), Album: strings.TrimSpace(body.Album)}
	}
	result, err := s.candidateSearcher.SearchWork(r.Context(), id, terms)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, searchItemResultView(result))
}

// routeSearchTrack parses both path parameters.
func (s *Server) routeSearchTrack(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a work id must be a number")
	if err != nil {
		return
	}
	partID, err := parsePathID(w, s, r, "partId", "a part id must be a number")
	if err != nil {
		return
	}
	s.SearchTrack(w, r, id, partID)
}

// SearchTrack runs the same pipeline against one track's own SINGLE item.
func (s *Server) SearchTrack(w http.ResponseWriter, r *http.Request, id, partID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.candidateSearcher == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeInternal,
			"Not available", "searching is not wired up in this build")
		return
	}
	result, err := s.candidateSearcher.SearchTrack(r.Context(), id, partID)
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
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.workVisible(w, r, access, id) {
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

// routeSendCandidate parses both path parameters.
func (s *Server) routeSendCandidate(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a work id must be a number")
	if err != nil {
		return
	}
	decisionID, err := parsePathID(w, s, r, "decisionId", "a decision id must be a number")
	if err != nil {
		return
	}
	s.SendCandidate(w, r, id, decisionID)
}

// SendCandidate dispatches one ACCEPTed decision to a matching downloader (ADR-57's
// acquire.send); request on the album's library, as SearchWork.
func (s *Server) SendCandidate(w http.ResponseWriter, r *http.Request, id, decisionID int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.workPermitted(w, r, access, id, settings.PermissionRequest) {
		return
	}
	acquisition, err := s.candidateSearcher.SendCandidate(r.Context(), id, decisionID)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, acquisitionView(acquisition))
}

func (s *Server) routeBlocklistCandidate(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a work id must be a number")
	if err != nil {
		return
	}
	decisionID, err := parsePathID(w, s, r, "decisionId", "a decision id must be a number")
	if err != nil {
		return
	}
	s.BlocklistCandidate(w, r, id, decisionID)
}

// BlocklistCandidate never offers one release again. Administrator only, like the Blocklists page.
func (s *Server) BlocklistCandidate(w http.ResponseWriter, r *http.Request, id, decisionID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.candidateSearcher.BlocklistCandidate(r.Context(), id, decisionID); err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxPageSearch is the most albums "search this page" names, the Wanted page's largest page.
const maxPageSearch = 200

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
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	// Searching sends downloads now, so it is a request on the library, not a read of it.
	if err := access.Require(id, settings.PermissionRequest); err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body gen.SearchCategoryJSONRequestBody
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil ||
			body.WorkIds != nil && len(*body.WorkIds) > maxPageSearch {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "work_ids must be a list of at most 200 album ids")
			return
		}
	}
	duplicate, err := s.candidateSearcher.SearchCategory(r.Context(), id, derefOr(body.WorkIds, nil))
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.CategorySearchEnqueued{Duplicate: duplicate})
}

// GrabResult is one automatic search: how many releases matched, and what was sent.
type GrabResult struct {
	Found              int
	Sent               *Acquisition
	AlreadyDownloading bool
	Held               bool
	Refused            int
}

func (s *Server) routeGrabWork(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a work id must be a number")
	if err != nil {
		return
	}
	s.GrabWork(w, r, id)
}

// GrabWork is Lidarr's automatic search of one album; request on its library, as SearchWork.
func (s *Server) GrabWork(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.workPermitted(w, r, access, id, settings.PermissionRequest) {
		return
	}
	result, err := s.candidateSearcher.GrabWork(r.Context(), id)
	if err != nil {
		writeCandidateSearchError(w, s, err)
		return
	}
	view := gen.GrabResult{
		Found: result.Found, AlreadyDownloading: result.AlreadyDownloading, Held: &result.Held, Refused: &result.Refused,
	}
	if result.Sent != nil {
		sent := acquisitionView(*result.Sent)
		view.Sent = &sent
	}
	writeJSON(w, s.logger, http.StatusOK, view)
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
	case errors.Is(err, ErrDecisionNotAccepted):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Not accepted", "only an ACCEPT decision can be dispatched")
	case errors.Is(err, ErrNoDownloaderForProtocol):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"No downloader", "no downloader is configured for this candidate's own protocol")
	case errors.Is(err, ErrAcquisitionAlreadyInFlight):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"Already in flight", "this item already has an acquisition in flight")
	case errors.Is(err, ErrContentBlocklisted):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Blocklisted content",
			"this release is the same download as one that failed before, listed again; pick another")
	case errors.Is(err, ErrDownloadClientRefused):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Refused by the download client",
			strings.TrimPrefix(err.Error(), ErrDownloadClientRefused.Error()+": "))
	case errors.Is(err, ErrCandidateDecisionNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such decision")
	case errors.Is(err, sql.ErrNoRows):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
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
		indexerErrors[index] = gen.IndexerSearchError{
			IndexerId: indexerError.IndexerID, IndexerName: indexerError.IndexerName,
			Error: indexerError.Error, TooSlow: indexerError.TooSlow,
		}
	}
	return gen.SearchItemResult{TierUsed: result.TierUsed, Decisions: decisions, IndexerErrors: indexerErrors}
}

func acquisitionView(acquisition Acquisition) gen.Acquisition {
	view := gen.Acquisition{
		Id: acquisition.ID, DownloaderId: acquisition.DownloaderID,
		DownloadClientRef: acquisition.DownloadClientRef,
		Protocol:          gen.AcquisitionProtocol(acquisition.Protocol), State: acquisition.State,
		CreatedAt: acquisition.CreatedAt, UpdatedAt: acquisition.UpdatedAt,
	}
	if acquisition.ClientCategory != "" {
		category := acquisition.ClientCategory
		view.ClientCategory = &category
	}
	return view
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
		Protocol: gen.CandidateDecisionProtocol(decision.Protocol),
		Source:   gen.CandidateDecisionSource(decision.Source), DecidedAt: decision.DecidedAt,
		IndexerName: &decision.IndexerName, Blocklisted: &decision.Blocklisted, NoClient: &decision.NoClient,
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
