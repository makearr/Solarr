package apiserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// VanishedFiles is a mass vanish a full scan held back instead of marking (section 10.4).
type VanishedFiles interface {
	// Held answers, per root folder id, how many files are held; a root holding none is absent.
	Held(ctx context.Context) (map[int64]int64, error)
	// Accept marks what the held scan found gone as missing, answering how many paths it marked; ErrNothingHeld when
	// the root holds nothing.
	Accept(ctx context.Context, rootFolderID int64) (int, error)
}

// ErrNothingHeld is an accept for a root whose last full scan held nothing back.
var ErrNothingHeld = errors.New("apiserver: nothing is held for this root folder")

func (s *Server) routeAcceptVanishedFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a root folder id must be a number")
		return
	}
	s.AcceptVanishedFiles(w, r, id)
}

// AcceptVanishedFiles marks the files a held full scan found gone as missing. Administrator only.
func (s *Server) AcceptVanishedFiles(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.vanishedFiles == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "scans are not available")
		return
	}
	folder, found := s.auth.Settings.Snapshot().RootFolder(id)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such root folder")
		return
	}
	marked, err := s.vanishedFiles.Accept(r.Context(), id)
	if errors.Is(err, ErrNothingHeld) {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Nothing to accept", err.Error())
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.logger.InfoContext(r.Context(), "vanished files accepted as missing", "root_folder_id", id, "marked", marked)
	writeJSON(w, s.logger, http.StatusOK, s.rootFolderView(folder))
}

// heldVanished is best effort: a root list that renders without the figure beats one that fails.
func (s *Server) heldVanished(ctx context.Context) map[int64]int64 {
	if s.vanishedFiles == nil {
		return nil
	}
	held, err := s.vanishedFiles.Held(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "held vanished files unavailable", "error", err)
		return nil
	}
	return held
}
