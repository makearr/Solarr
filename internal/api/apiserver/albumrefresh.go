package apiserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// ErrNotRefreshable reports a work whose media type cannot be read again from its sources.
var ErrNotRefreshable = errors.New("apiserver: this media type cannot refresh a work")

// WorkRefresher reads one work again from its sources: Radarr's per-item Refresh, for every media type.
type WorkRefresher interface {
	RefreshWork(ctx context.Context, workID int64) (duplicate bool, err error)
}

func (s *Server) routeRefreshWork(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a work id must be a number"); err == nil {
		s.RefreshWork(w, r, id)
	}
}

// RefreshWork is a work's "Refresh"; request on its library, since a series' refresh also adds its new episodes.
func (s *Server) RefreshWork(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workRefresher == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "refresh is not available")
		return
	}
	if !s.workPermitted(w, r, access, id, settings.PermissionRequest) {
		return
	}
	duplicate, err := s.workRefresher.RefreshWork(r.Context(), id)
	if errors.Is(err, ErrNotRefreshable) {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not refreshable", "this kind of work has no source to refresh from")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.CategorySearchEnqueued{Duplicate: duplicate})
}
