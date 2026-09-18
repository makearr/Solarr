package apiserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// BlocklistRow is one blocked release, as the interface shows it.
type BlocklistRow struct {
	ID          int64
	IndexerID   *int64
	GUID        string
	ContentHash string
	Title       string
	Reason      string
	Source      string
	ExpiresAt   *time.Time
	CreatedAt   time.Time
}

// Blocklist reads and reverses section 10.11's failure memory. Defined here
// and implemented by the caller, the same separation WorksReader keeps.
type Blocklist interface {
	List(ctx context.Context, afterID int64, pageSize int) ([]BlocklistRow, error)
	// Remove answers a missing id with an error wrapping sql.ErrNoRows.
	Remove(ctx context.Context, id int64) error
}

func (s *Server) routeListBlocklist(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	params := gen.ListBlocklistParams{}
	if raw := query.Get("after"); raw != "" {
		after, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "after must be a number")
			return
		}
		params.After = &after
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListBlocklist(w, r, params)
}

// ListBlocklist answers one page of blocked releases, newest first.
func (s *Server) ListBlocklist(w http.ResponseWriter, r *http.Request, params gen.ListBlocklistParams) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.blocklist == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the blocklist is not available")
		return
	}

	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	rows, err := s.blocklist.List(r.Context(), derefOr(params.After, 0), limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	items := make([]gen.BlocklistEntry, len(rows))
	for i, row := range rows {
		entry := gen.BlocklistEntry{
			Id: row.ID, Title: row.Title, Reason: row.Reason, Source: row.Source,
			CreatedAt: row.CreatedAt, IndexerId: row.IndexerID,
		}
		if row.GUID != "" {
			entry.Guid = &row.GUID
		}
		if row.ContentHash != "" {
			entry.ContentHash = &row.ContentHash
		}
		entry.ExpiresAt = row.ExpiresAt
		items[i] = entry
	}
	body := gen.BlocklistPage{Items: items}
	if len(items) == limit && limit > 0 {
		next := items[len(items)-1].Id
		body.NextCursor = &next
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

func (s *Server) routeRemoveBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a blocklist id must be a number")
		return
	}
	s.RemoveBlocklistEntry(w, r, id)
}

// RemoveBlocklistEntry lets a release be grabbed again.
func (s *Server) RemoveBlocklistEntry(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.blocklist == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the blocklist is not available")
		return
	}
	if err := s.blocklist.Remove(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such blocklist entry")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
