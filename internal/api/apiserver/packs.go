package apiserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
)

// PackLister answers ADR-50's own detected packs. Defined here and
// implemented by the caller (cmd/medianexus), the same reasoning every
// other library-backed interface in this package already gives --
// apiserver never imports internal/db/library directly.
type PackLister interface {
	ListPacks(ctx context.Context, afterID int64, limit int) ([]PackRow, error)
	GetPack(ctx context.Context, id int64) (PackRow, []PackItemRow, error)
}

// PackRow is one pack row.
type PackRow struct {
	ID                         int64
	Kind, DetectedFrom         string
	Confidence                 float64
	Title                      string
	AcquisitionID, SessionID   *int64
	MemberCount, ResolvedCount int64
	CreatedAt                  time.Time
}

// PackItemRow is one pack_item row.
type PackItemRow struct {
	Ord                             int64
	WorkID, EditionID, ImportUnitID *int64
	State                           string
}

// routeListPacks parses the after_id/limit query parameters, mirroring routeListBlockedContent.
func (s *Server) routeListPacks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListPacksParams

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
	s.ListPacks(w, r, params)
}

// ListPacks pages through every detected pack, oldest first.
func (s *Server) ListPacks(w http.ResponseWriter, r *http.Request, params gen.ListPacksParams) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.packs == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "packs are not available")
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

	rows, err := s.packs.ListPacks(r.Context(), afterID, limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.Pack, len(rows))
	for i, row := range rows {
		views[i] = packView(row)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// routeGetPack parses the id path parameter, mirroring routeGetImportSession.
func (s *Server) routeGetPack(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a pack id must be a number")
		return
	}
	s.GetPack(w, r, id)
}

// GetPack answers one pack and its own members.
func (s *Server) GetPack(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.packs == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "packs are not available")
		return
	}

	row, items, err := s.packs.GetPack(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such pack")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.PackItem, len(items))
	for i, item := range items {
		views[i] = packItemView(item)
	}
	writeJSON(w, s.logger, http.StatusOK, gen.PackDetail{Pack: packView(row), Items: views})
}

func packView(row PackRow) gen.Pack {
	view := gen.Pack{
		Id: row.ID, Kind: gen.PackKind(row.Kind), DetectedFrom: gen.PackDetectedFrom(row.DetectedFrom),
		Confidence: float32(row.Confidence), Title: row.Title, AcquisitionId: row.AcquisitionID,
		SessionId: row.SessionID, MemberCount: row.MemberCount, ResolvedCount: row.ResolvedCount,
		CreatedAt: row.CreatedAt,
	}
	return view
}

func packItemView(item PackItemRow) gen.PackItem {
	return gen.PackItem{
		Ord: item.Ord, WorkId: item.WorkID, EditionId: item.EditionID,
		ImportUnitId: item.ImportUnitID, State: gen.PackItemState(item.State),
	}
}
