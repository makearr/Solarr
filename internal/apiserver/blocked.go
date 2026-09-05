package apiserver

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
)

// BlockedContentLister answers ADR-53 part 2's own hash blocks. Defined here
// and implemented by the caller, so this package never imports the library
// store directly (WorksReader's own doc comment explains the same choice).
type BlockedContentLister interface {
	ListBlockedContent(ctx context.Context, afterID int64, limit int) ([]BlockedContentRow, error)
	RemoveBlockedContent(ctx context.Context, id int64) error
}

// BlockedContentRow is one blocked_content row.
type BlockedContentRow struct {
	ID                                    int64
	OSHash, QuickHash, Chromaprint, Phash string
	SizeBytes                             int64
	Reason                                string
	Note, MediaType, Title, ReleaseGroup  string
	Source                                string
	ExpiresAt                             *time.Time
	CreatedAt                             time.Time
	HitCount                              int64
	LastHitAt                             *time.Time
}

// routeListBlockedContent parses after_id/limit, mirroring routeListUnidentified.
func (s *Server) routeListBlockedContent(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListBlockedContentParams

	if raw := query.Get("after_id"); raw != "" {
		afterID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "after_id must be a number")
			return
		}
		params.AfterId = &afterID
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}

	s.ListBlockedContent(w, r, params)
}

// ListBlockedContent pages through every block, oldest first.
func (s *Server) ListBlockedContent(w http.ResponseWriter, r *http.Request, params gen.ListBlockedContentParams) {
	if s.blockedContent == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "blocked content is not available")
		return
	}

	limit := 50
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	afterID := int64(0)
	if params.AfterId != nil {
		afterID = *params.AfterId
	}

	rows, err := s.blockedContent.ListBlockedContent(r.Context(), afterID, limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	views := make([]gen.BlockedContentEntry, len(rows))
	for i, row := range rows {
		views[i] = blockedContentView(row)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// routeRemoveBlockedContent parses the id path parameter, mirroring routeDeleteCategory.
func (s *Server) routeRemoveBlockedContent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.RemoveBlockedContent(w, r, id)
}

// RemoveBlockedContent reverses a block. Administrator only.
func (s *Server) RemoveBlockedContent(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.blockedContent == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "blocked content is not available")
		return
	}
	if err := s.blockedContent.RemoveBlockedContent(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func blockedContentView(row BlockedContentRow) gen.BlockedContentEntry {
	view := gen.BlockedContentEntry{
		Id: &row.ID, SizeBytes: &row.SizeBytes, Reason: (*gen.BlockedContentEntryReason)(&row.Reason),
		Title: &row.Title, ReleaseGroup: &row.ReleaseGroup, Source: (*gen.BlockedContentEntrySource)(&row.Source),
		CreatedAt: &row.CreatedAt, HitCount: &row.HitCount,
	}
	if row.OSHash != "" {
		view.Oshash = &row.OSHash
	}
	if row.QuickHash != "" {
		view.QuickHash = &row.QuickHash
	}
	if row.Chromaprint != "" {
		view.Chromaprint = &row.Chromaprint
	}
	if row.Phash != "" {
		view.Phash = &row.Phash
	}
	if row.Note != "" {
		view.Note = &row.Note
	}
	if row.MediaType != "" {
		view.MediaType = &row.MediaType
	}
	view.ExpiresAt = row.ExpiresAt
	view.LastHitAt = row.LastHitAt
	return view
}
