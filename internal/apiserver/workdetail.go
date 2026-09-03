package apiserver

import (
	"context"
	"database/sql"
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
	AddedAt       time.Time
	UpdatedAt     time.Time
	ExternalIDs   []ExternalIDRow
	Editions      []EditionRow
	Parts         []PartRow
	RootFolderIDs []int64
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
	return gen.WorkDetail{
		Id: row.ID, MediaType: row.MediaType, Title: row.Title, SortTitle: row.SortTitle,
		Year: yearOf(row.Year), RuntimeMs: row.RuntimeMs, Language: row.Language,
		AddedAt: row.AddedAt, UpdatedAt: row.UpdatedAt,
		ExternalIds: buildExternalIDViews(row.ExternalIDs),
		Editions:    buildEditionViews(row.Editions),
		Parts:       buildPartViews(row.Parts),
	}
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
