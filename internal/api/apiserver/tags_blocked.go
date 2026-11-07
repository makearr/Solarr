package apiserver

import (
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// BlockTag removes a tag from every work and refuses it from then on; administrator only.
func (s *Server) BlockTag(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.tagsAvailable(w) {
		return
	}
	if err := s.workEditor.BlockTag(r.Context(), id, s.wall.Now().Unix()); err != nil {
		s.writeWorkEditError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListBlockedTags answers what was blocked; administrator only.
func (s *Server) ListBlockedTags(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) || !s.tagsAvailable(w) {
		return
	}
	rows, err := s.workEditor.BlockedTags(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.BlockedTag, len(rows))
	for index, row := range rows {
		views[index] = gen.BlockedTag{Name: row.Name, GenreTree: row.GenreTree, BlockedAt: row.BlockedAt}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// UnblockTag lets a blocked tag be created again; administrator only.
func (s *Server) UnblockTag(w http.ResponseWriter, r *http.Request, params gen.UnblockTagParams) {
	if !s.requireAdministrator(w, r) || !s.tagsAvailable(w) {
		return
	}
	if params.Name == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "name is required")
		return
	}
	if err := s.workEditor.UnblockTag(r.Context(), params.Name, derefOr(params.GenreTree, "")); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) routeUnblockTag(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	params := gen.UnblockTagParams{Name: query.Get("name")}
	if tree := query.Get("genre_tree"); tree != "" {
		params.GenreTree = &tree
	}
	s.UnblockTag(w, r, params)
}
