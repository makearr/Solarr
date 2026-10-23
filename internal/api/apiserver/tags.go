package apiserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// ListTags answers the tag tree, one taxonomy's when asked for one.
func (s *Server) ListTags(w http.ResponseWriter, r *http.Request, params gen.ListTagsParams) {
	if s.workEditor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "tags are not available")
		return
	}
	rows, err := s.workEditor.TagTree(r.Context(), derefOr(params.Taxonomy, "*"))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	nodes := make([]gen.TagNode, len(rows))
	for i, row := range rows {
		nodes[i] = tagNode(row)
	}
	writeJSON(w, s.logger, http.StatusOK, nodes)
}

func tagNode(row TagRow) gen.TagNode {
	node := gen.TagNode{Id: row.ID, Name: row.Name, Taxonomy: row.Taxonomy, Source: row.Source, Works: row.Works, Aliases: row.Aliases}
	if node.Aliases == nil {
		node.Aliases = []string{}
	}
	if row.ParentID != 0 {
		node.ParentId = &row.ParentID
	}
	return node
}

// CreateTag adds a tag; tags are shared by every library, so only an administrator may.
func (s *Server) CreateTag(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) || !s.tagsAvailable(w) {
		return
	}
	var body gen.CreateTagJSONRequestBody
	if !s.decodeTagBody(w, r, &body) || !s.tagNamed(w, body.Name) {
		return
	}
	row, err := s.workEditor.CreateTag(r.Context(), strings.TrimSpace(body.Name), body.Taxonomy, derefOr(body.ParentId, 0))
	if err != nil {
		s.writeWorkEditError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, tagNode(row))
}

// EditTag renames a tag and places it in its taxonomy's tree; administrator only.
func (s *Server) EditTag(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.tagsAvailable(w) {
		return
	}
	var body gen.EditTagJSONRequestBody
	if !s.decodeTagBody(w, r, &body) || !s.tagNamed(w, body.Name) {
		return
	}
	if err := s.workEditor.EditTag(r.Context(), id, strings.TrimSpace(body.Name), derefOr(body.ParentId, 0)); err != nil {
		s.writeWorkEditError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MergeTag folds one tag into another; administrator only.
func (s *Server) MergeTag(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.tagsAvailable(w) {
		return
	}
	var body gen.MergeTagJSONRequestBody
	if !s.decodeTagBody(w, r, &body) {
		return
	}
	if err := s.workEditor.MergeTag(r.Context(), id, body.IntoId); err != nil {
		s.writeWorkEditError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteTag removes a tag from every work; administrator only.
func (s *Server) DeleteTag(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.tagsAvailable(w) {
		return
	}
	if err := s.workEditor.DeleteTag(r.Context(), id); err != nil {
		s.writeWorkEditError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// withTagID reads the path's tag id before the handler runs.
func (s *Server) withTagID(handler func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if id, ok := s.workIDParam(w, r); ok {
			handler(w, r, id)
		}
	}
}

// tagNamed refuses a blank tag name with 400.
func (s *Server) tagNamed(w http.ResponseWriter, name string) bool {
	if strings.TrimSpace(name) == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a tag needs a name")
		return false
	}
	return true
}

func (s *Server) tagsAvailable(w http.ResponseWriter) bool {
	if s.workEditor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "tags are not available")
		return false
	}
	return true
}

func (s *Server) decodeTagBody(w http.ResponseWriter, r *http.Request, body any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return false
	}
	return true
}

// parseTagChips reads a list's tag chips, "12,-7": tags a work must carry and tags it must not.
func parseTagChips(chips string) (carry, without []int64, err error) {
	for field := range strings.SplitSeq(chips, ",") {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		id, err := strconv.ParseInt(field, 10, 64)
		if err != nil || id == 0 {
			return nil, nil, ErrInvalidChoice
		}
		if id < 0 {
			without = append(without, -id)
		} else {
			carry = append(carry, id)
		}
	}
	return carry, without, nil
}
