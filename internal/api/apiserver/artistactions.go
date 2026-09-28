package apiserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// ArtistActions are the artist page's bulk verbs (asked for 2026-09-19). A missing person answers
// an error wrapping sql.ErrNoRows.
type ArtistActions interface {
	// SearchMissing searches the artist's monitored, incomplete albums in the background.
	// SearchMissing searches the artist's missing albums in each of categoryIDs.
	SearchMissing(ctx context.Context, personID int64, categoryIDs []int64) (duplicate bool, err error)
	// Refresh fetches the artist's metadata again in the background.
	Refresh(ctx context.Context, personID int64) (duplicate bool, err error)
	// MonitorAll monitors every album of the artist's discography and answers how many.
	MonitorAll(ctx context.Context, personID int64) (albums int64, err error)
}

func (s *Server) routeSearchPersonMissing(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a person id must be a number"); err == nil {
		s.SearchPersonMissing(w, r, id)
	}
}

func (s *Server) routeRefreshPerson(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a person id must be a number"); err == nil {
		s.RefreshPerson(w, r, id)
	}
}

// RefreshPerson is the artist page's "Refresh metadata"; any signed-in user, like a search.
func (s *Server) RefreshPerson(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.artistActionsAvailable(w) {
		return
	}
	duplicate, err := s.artistActions.Refresh(r.Context(), id)
	if err != nil {
		s.writeArtistActionError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.CategorySearchEnqueued{Duplicate: duplicate})
}

func (s *Server) routeMonitorPersonAll(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a person id must be a number"); err == nil {
		s.MonitorPersonAll(w, r, id)
	}
}

// SearchPersonMissing is the artist page's "Search all missing": it sends downloads, so it searches
// only the libraries the caller may request in, and none is 403 (2026-09-28).
func (s *Server) SearchPersonMissing(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.artistActionsAvailable(w) {
		return
	}
	categoryIDs := access.VisibleCategoryIDs(settings.PermissionRequest)
	if len(categoryIDs) == 0 {
		writeError(w, s.logger, fmt.Errorf("%w: no library allows requests", authz.ErrInsufficientPermission))
		return
	}
	duplicate, err := s.artistActions.SearchMissing(r.Context(), id, categoryIDs)
	if err != nil {
		s.writeArtistActionError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.CategorySearchEnqueued{Duplicate: duplicate})
}

// MonitorPersonAll monitors the artist's whole discography. Administrator only: it is a bulk change.
func (s *Server) MonitorPersonAll(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.artistActionsAvailable(w) {
		return
	}
	albums, err := s.artistActions.MonitorAll(r.Context(), id)
	if err != nil {
		s.writeArtistActionError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]int64{"albums": albums})
}

func (s *Server) artistActionsAvailable(w http.ResponseWriter) bool {
	if s.artistActions == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "artist actions are not available")
		return false
	}
	return true
}

func (s *Server) writeArtistActionError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person")
		return
	}
	writeError(w, s.logger, err)
}
