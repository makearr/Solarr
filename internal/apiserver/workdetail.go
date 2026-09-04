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
)

// WorkDetailReader reads one work's full detail (WorksReader's own doc
// comment explains why this package never imports the library store).
type WorkDetailReader interface {
	// GetWorkDetail answers a missing id with an error wrapping sql.ErrNoRows.
	GetWorkDetail(ctx context.Context, id int64) (WorkDetailRow, error)
}

// ExternalIDRow is one provider's id for a work, edition or part.
type ExternalIDRow struct {
	Provider   string
	ExternalID string
}

// EditionRow is one edition of a work.
type EditionRow struct {
	ID              int64
	Kind            string
	Label           string
	Year            *int64
	Format          *string
	CatalogueNumber *string
	Barcode         *string
	RuntimeMs       *int64
	IsDefault       bool
	ExternalIDs     []ExternalIDRow
}

// PartRow is one part (track, episode, chapter, ...) of a work.
type PartRow struct {
	ID          int64
	MediaRef    string
	SortKey     string
	Title       *string
	RuntimeMs   *int64
	Date        *string
	Monitored   bool
	ExternalIDs []ExternalIDRow
}

// CreditRow is one work-level credit, the person's own name included so a
// client never needs a second round-trip just to display it.
type CreditRow struct {
	PersonID   int64
	PersonName string
	Role       string
}

// WantedRow is the work's own album-level SET desired_item, when one exists.
type WantedRow struct {
	ID            int64
	Monitored     bool
	RequiredCount *int64
	PresentCount  int64
	State         string
}

// WorkDetailRow is a work's full detail. RootFolderIDs is not rendered --
// GetWork uses it to run ListWorks' ADR-27 visibility check against one item.
type WorkDetailRow struct {
	ID            int64
	MediaType     string
	Title         string
	SortTitle     string
	Year          *int64
	RuntimeMs     *int64
	Language      *string
	Description   *string
	OnDisk        bool
	AddedAt       time.Time
	UpdatedAt     time.Time
	ExternalIDs   []ExternalIDRow
	Editions      []EditionRow
	Parts         []PartRow
	Credits       []CreditRow
	Genres        []string
	RootFolderIDs []int64
	// Wanted is nil when nothing has ever wanted this album.
	Wanted *WantedRow
}

// WorksMonitor toggles monitored state at the album and part level (section
// 19.6). Defined here and implemented by the caller, same reasoning as
// WorksReader's own doc comment.
type WorksMonitor interface {
	// SetWorkMonitored ensures the album-level SET item exists when
	// monitored is true (creating it on first use), or unmonitors an
	// existing one when false -- a no-op if none exists yet. Answers a
	// missing work id with an error wrapping sql.ErrNoRows.
	SetWorkMonitored(ctx context.Context, workID int64, monitored bool) (WantedRow, error)
	// SetPartMonitored toggles one part's monitored flag and recomputes
	// every album item over the work. Answers a missing work or part id
	// with an error wrapping sql.ErrNoRows.
	SetPartMonitored(ctx context.Context, workID, partID int64, monitored bool) error
}

// routeGetWork parses the id path parameter, mirroring routeUser: a malformed
// id answers 400 the same way everywhere, before any lookup runs.
func (s *Server) routeGetWork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}
	s.GetWork(w, r, id)
}

// GetWork answers a work's structural detail; no scoring or Decision content
// yet (stage 4). A work under no readable root folder answers 404, same as missing.
func (s *Server) GetWork(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	if s.workDetail == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the library is not available")
		return
	}

	row, err := s.workDetail.GetWorkDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	rootFolderIDs, err := s.visibleRootFolders(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !sharesAnID(rootFolderIDs, row.RootFolderIDs) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return
	}

	writeJSON(w, s.logger, http.StatusOK, buildWorkDetailView(row))
}

// routeSetWorkMonitored parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeSetWorkMonitored(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}
	s.SetWorkMonitored(w, r, id)
}

// SetWorkMonitored toggles a work's album-level monitored state. The
// request body is decoded here, not by a generated wrapper, same as
// SubscribePerson/ImportSettings (ADR-31).
func (s *Server) SetWorkMonitored(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workDetail == nil || s.worksMonitor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "monitoring is not available")
		return
	}

	var body gen.SetWorkMonitoredJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	if !s.workVisible(w, r, access, id) {
		return
	}

	wanted, err := s.worksMonitor.SetWorkMonitored(r.Context(), id, body.Monitored)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildWantedView(wanted))
}

// routeSetPartMonitored parses id and partId, mirroring routeSetWorkMonitored.
func (s *Server) routeSetPartMonitored(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}
	partID, err := strconv.ParseInt(chi.URLParam(r, "partId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a part id must be a number")
		return
	}
	s.SetPartMonitored(w, r, id, partID)
}

// SetPartMonitored toggles one part's monitored state.
func (s *Server) SetPartMonitored(w http.ResponseWriter, r *http.Request, id, partID int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workDetail == nil || s.worksMonitor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "monitoring is not available")
		return
	}

	var body gen.SetPartMonitoredJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	if !s.workVisible(w, r, access, id) {
		return
	}

	if err := s.worksMonitor.SetPartMonitored(r.Context(), id, partID, body.Monitored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such part")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workVisible runs GetWork's own ADR-27 visibility check against id, writing
// a 404 (indistinguishable from missing, Invariant 37) and returning false
// when the work is not visible to the caller -- shared so a mutation can
// never be reached for a work the caller could not even read.
func (s *Server) workVisible(w http.ResponseWriter, r *http.Request, access authz.Access, id int64) bool {
	row, err := s.workDetail.GetWorkDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return false
	}
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	rootFolderIDs, err := s.visibleRootFolders(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	if !sharesAnID(rootFolderIDs, row.RootFolderIDs) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return false
	}
	return true
}

func buildWantedView(row WantedRow) gen.Wanted {
	return gen.Wanted{
		Id: row.ID, Monitored: row.Monitored, RequiredCount: row.RequiredCount,
		PresentCount: row.PresentCount, State: gen.WantedState(row.State),
	}
}

// sharesAnID reports whether a and b have any element in common.
func sharesAnID(a, b []int64) bool {
	seen := make(map[int64]struct{}, len(a))
	for _, id := range a {
		seen[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := seen[id]; ok {
			return true
		}
	}
	return false
}

func buildWorkDetailView(row WorkDetailRow) gen.WorkDetail {
	view := gen.WorkDetail{
		Id: row.ID, MediaType: row.MediaType, Title: row.Title, SortTitle: row.SortTitle,
		Year: yearOf(row.Year), RuntimeMs: row.RuntimeMs, Language: row.Language,
		Description: row.Description, OnDisk: row.OnDisk,
		AddedAt: row.AddedAt, UpdatedAt: row.UpdatedAt,
		ExternalIds: buildExternalIDViews(row.ExternalIDs),
		Editions:    buildEditionViews(row.Editions),
		Parts:       buildPartViews(row.Parts),
		Credits:     buildCreditViews(row.Credits),
		Genres:      row.Genres,
	}
	if row.Wanted != nil {
		wanted := buildWantedView(*row.Wanted)
		view.Wanted = &wanted
	}
	return view
}

func buildCreditViews(rows []CreditRow) []gen.Credit {
	views := make([]gen.Credit, len(rows))
	for index, row := range rows {
		views[index] = gen.Credit{PersonId: row.PersonID, PersonName: row.PersonName, Role: row.Role}
	}
	return views
}

func buildExternalIDViews(rows []ExternalIDRow) []gen.ExternalID {
	views := make([]gen.ExternalID, len(rows))
	for index, row := range rows {
		views[index] = gen.ExternalID{Provider: row.Provider, ExternalId: row.ExternalID}
	}
	return views
}

func buildEditionViews(rows []EditionRow) []gen.Edition {
	views := make([]gen.Edition, len(rows))
	for index, row := range rows {
		year := yearOf(row.Year)
		views[index] = gen.Edition{
			Id: row.ID, Kind: row.Kind, Label: row.Label, Year: year,
			Format: row.Format, CatalogueNumber: row.CatalogueNumber, Barcode: row.Barcode,
			RuntimeMs: row.RuntimeMs, IsDefault: row.IsDefault,
			ExternalIds: buildExternalIDViews(row.ExternalIDs),
		}
	}
	return views
}

func buildPartViews(rows []PartRow) []gen.WorkPart {
	views := make([]gen.WorkPart, len(rows))
	for index, row := range rows {
		views[index] = gen.WorkPart{
			Id: row.ID, MediaRef: row.MediaRef, SortKey: row.SortKey, Title: row.Title,
			RuntimeMs: row.RuntimeMs, Date: row.Date, Monitored: row.Monitored,
			ExternalIds: buildExternalIDViews(row.ExternalIDs),
		}
	}
	return views
}
