package apiserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// ReadingLists matches a ComicRack reading list to what a comic library holds.
type ReadingLists interface {
	// Preview answers ErrBadReadingList for text that is no reading list.
	Preview(ctx context.Context, categoryID int64, content string) (gen.ReadingListPreview, error)
}

// ErrBadReadingList is text that is no usable reading list.
var ErrBadReadingList = errors.New("apiserver: not a reading list")

// PreviewReadingList matches an uploaded reading list to the library's series and issues, saving nothing.
func (s *Server) PreviewReadingList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.readingLists == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "reading lists are not available")
		return
	}
	if _, err := s.libraryFor(r, id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if !s.decodeBody(w, r, 2*maxImportBody, &body, "expected the reading list's text") {
		return
	}
	preview, err := s.readingLists.Preview(r.Context(), id, body.Content)
	if errors.Is(err, ErrBadReadingList) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Not a reading list", err.Error())
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, preview)
}
