package apiserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// IncompleteDiscographyRow is an artist with albums of their discography the library does not hold.
type IncompleteDiscographyRow struct {
	PersonID    int64
	Name        string
	Held, Total int64
	Monitored   bool
}

// IncompleteDiscographies answers a library's artists with missing albums, most missing first.
type IncompleteDiscographies interface {
	IncompleteDiscographies(ctx context.Context, categoryID int64, limit int) ([]IncompleteDiscographyRow, error)
}

func (s *Server) routeListIncompleteDiscographies(w http.ResponseWriter, r *http.Request) {
	categoryID, err := strconv.ParseInt(r.URL.Query().Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id must be a number")
		return
	}
	params := gen.ListIncompleteDiscographiesParams{CategoryId: categoryID}
	if parsed, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && parsed >= 1 && parsed <= 200 {
		params.Limit = &parsed
	}
	s.ListIncompleteDiscographies(w, r, params)
}

// ListIncompleteDiscographies answers a library's artists with albums missing; a library the caller cannot read is 404.
func (s *Server) ListIncompleteDiscographies(w http.ResponseWriter, r *http.Request, params gen.ListIncompleteDiscographiesParams) {
	categoryID, limit := params.CategoryId, derefOr(params.Limit, 50)
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(categoryID, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.incompleteDiscographies == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "discovery is not available")
		return
	}
	rows, err := s.incompleteDiscographies.IncompleteDiscographies(r.Context(), categoryID, limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := make([]gen.IncompleteDiscography, len(rows))
	for index, row := range rows {
		out[index] = gen.IncompleteDiscography{PersonId: row.PersonID, Name: row.Name, Held: row.Held, Total: row.Total, Monitored: row.Monitored}
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}
