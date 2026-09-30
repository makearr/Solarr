package apiserver

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// AlbumRefresher fetches one album's metadata again in the background: Radarr's per-item Refresh.
type AlbumRefresher interface {
	RefreshAlbum(ctx context.Context, workID int64) (duplicate bool, err error)
}

func (s *Server) routeRefreshWork(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a work id must be a number"); err == nil {
		s.RefreshWork(w, r, id)
	}
}

// RefreshWork is an album's "Refresh metadata"; anyone who may see the album, like the artist's.
func (s *Server) RefreshWork(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.albumRefresher == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "album refresh is not available")
		return
	}
	if !s.workPermitted(w, r, access, id, settings.PermissionRead) {
		return
	}
	duplicate, err := s.albumRefresher.RefreshAlbum(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.CategorySearchEnqueued{Duplicate: duplicate})
}
