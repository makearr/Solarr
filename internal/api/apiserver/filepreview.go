package apiserver

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/platform/previewstore"
)

// GetFilePreview serves a video's single frame or thumbnail sheet straight off disk, to whoever may read the file.
func (s *Server) GetFilePreview(w http.ResponseWriter, r *http.Request, _ int64, _ string) {
	s.routeGetFilePreview(w, r)
}

func (s *Server) routeGetFilePreview(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	if kind != previewstore.Frame && kind != previewstore.Sheet {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a preview is frame or sheet")
		return
	}
	file, _, ok := s.playbackFile(w, r)
	if !ok {
		return
	}
	if s.previewsDir == "" {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no preview for this file")
		return
	}
	target := previewstore.Path(s.previewsDir, file.MediaObjectID, kind)
	opened, err := s.fsys.Open(target)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no preview for this file")
		return
	}
	defer func() { _ = opened.Close() }()
	var modified time.Time
	if info, err := s.fsys.Stat(target); err == nil {
		modified = info.ModTime
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", modified, opened)
}
