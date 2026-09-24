package apiserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// ArtistActions are the artist page's bulk verbs (asked for 2026-09-19). A missing person answers
// an error wrapping sql.ErrNoRows.
type ArtistActions interface {
	// SearchMissing searches the artist's monitored, incomplete albums in the background.
	SearchMissing(ctx context.Context, personID int64) (duplicate bool, err error)
	// MonitorAll monitors every album of the artist's discography and answers how many.
	MonitorAll(ctx context.Context, personID int64) (albums int64, err error)
}

func (s *Server) routeSearchPersonMissing(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a person id must be a number"); err == nil {
		s.SearchPersonMissing(w, r, id)
	}
}

func (s *Server) routeMonitorPersonAll(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a person id must be a number"); err == nil {
		s.MonitorPersonAll(w, r, id)
	}
}

// SearchPersonMissing is the artist page's "Search all missing"; any signed-in user, like a library search.
func (s *Server) SearchPersonMissing(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.artistActionsAvailable(w) {
		return
	}
	duplicate, err := s.artistActions.SearchMissing(r.Context(), id)
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
