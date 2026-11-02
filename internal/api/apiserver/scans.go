package apiserver

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// LibraryIdentifier asks the providers again about a library's waiting files, answering the folders queued.
type LibraryIdentifier interface {
	IdentifyLibraryAgain(ctx context.Context, categoryID int64) (int, error)
}

// ScanReader lists the library scans under way.
type ScanReader interface {
	RunningScans(ctx context.Context) ([]gen.ScanRun, error)
}

func (s *Server) routeIdentifyLibraryAgain(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.IdentifyLibraryAgain(w, r, id)
	}
}

// IdentifyLibraryAgain queues a rescan of the folders of one library's waiting files.
func (s *Server) IdentifyLibraryAgain(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.libraryIdentifier == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "identifying files is not available")
		return
	}
	folders, err := s.libraryIdentifier.IdentifyLibraryAgain(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, struct {
		Folders int `json:"folders"`
	}{folders})
}

// ListScans answers each library scan under way.
func (s *Server) ListScans(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.scans == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "scan progress is not available")
		return
	}
	scans, err := s.scans.RunningScans(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Scans []gen.ScanRun `json:"scans"`
	}{scans})
}
