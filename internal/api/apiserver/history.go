package apiserver

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// HistoryRow is one download history entry, as it was known when it happened.
type HistoryRow struct {
	ID int64
	At time.Time
	// Kind is "grabbed", "imported" or "failed".
	Kind         string
	WorkID       *int64
	CategoryID   int64
	Album        string
	Artist       string
	ReleaseTitle string
	Indexer      string
	Client       string
	Detail       string
}

// History reads download history, newest first; a zero workID means every album. Who may see
// which entry is the server's call, not the reader's.
type History interface {
	List(ctx context.Context, workID, beforeID int64, limit int) ([]HistoryRow, error)
}

func (s *Server) routeListHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	params := gen.ListHistoryParams{}
	if raw := query.Get("after"); raw != "" {
		after, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "after must be a number")
			return
		}
		params.After = &after
	}
	if raw := query.Get("work_id"); raw != "" {
		workID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "work_id must be a number")
			return
		}
		params.WorkId = &workID
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListHistory(w, r, params)
}

// ListHistory answers one page of download history the caller may see, newest first.
func (s *Server) ListHistory(w http.ResponseWriter, r *http.Request, params gen.ListHistoryParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.history == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "history is not available")
		return
	}

	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	rows, err := s.history.List(r.Context(), derefOr(params.WorkId, 0), derefOr(params.After, 0), limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	items := make([]gen.HistoryEntry, 0, len(rows))
	for _, row := range rows {
		if !canReadHistory(access, row.CategoryID) {
			continue
		}
		items = append(items, gen.HistoryEntry{
			Id: row.ID, At: row.At, Kind: gen.HistoryEntryKind(row.Kind), ReleaseTitle: row.ReleaseTitle, WorkId: row.WorkID,
			Album: strPtr(row.Album), Artist: strPtr(row.Artist), Indexer: strPtr(row.Indexer),
			Client: strPtr(row.Client), Detail: strPtr(row.Detail),
		})
	}
	body := gen.HistoryPage{Items: items}
	// The cursor follows what was read, not what was shown, so hidden entries never end paging early.
	if len(rows) == limit && limit > 0 {
		next := rows[len(rows)-1].ID
		body.NextCursor = &next
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

// canReadHistory: an entry is its library's to read; one with none (its album since removed) is
// an administrator's.
func canReadHistory(access authz.Access, categoryID int64) bool {
	if categoryID == 0 {
		return access.Role() == settings.RoleAdmin
	}
	return access.CanSee(categoryID)
}
