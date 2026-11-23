package apiserver

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// FileSettingPreviews counts the files a File Management option would write.
type FileSettingPreviews interface {
	// Preview answers the count; ErrInvalidChoice for a setting or option the library's type cannot preview.
	Preview(ctx context.Context, categoryID int64, key, option string) (int, error)
}

// PreviewLibraryFileSetting says how many files a setting would write at one option, writing none. Administrator only.
func (s *Server) PreviewLibraryFileSetting(w http.ResponseWriter, r *http.Request, id int64, key string, params gen.PreviewLibraryFileSettingParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.fileSettingPreviews == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "previews are not available")
		return
	}
	if _, err := s.libraryFor(r, id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	files, err := s.fileSettingPreviews.Preview(r.Context(), id, key, params.Option)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Files int `json:"files"`
	}{files})
}
