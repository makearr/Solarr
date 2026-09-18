package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// routeListImportLists parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeListImportLists(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a library id must be a number")
		return
	}
	s.ListImportLists(w, r, id)
}

// ListImportLists answers one library's own import lists (ADR-38, todo
// 4.16). Scaffolding only -- nothing syncs against one yet.
func (s *Server) ListImportLists(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}

	lists, err := s.auth.Settings.ImportLists(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.ImportList, len(lists))
	for index, list := range lists {
		views[index] = importListView(list)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// routeCreateImportList parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeCreateImportList(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a library id must be a number")
		return
	}
	s.CreateImportList(w, r, id)
}

// CreateImportList adds an import list to a library. Administrator only,
// matching every other settings-writing endpoint (CreateCategory,
// CreateExclusionPattern).
func (s *Server) CreateImportList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.CreateImportListJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	created, err := s.auth.Settings.CreateImportList(r.Context(), id, importListRequestFromWrite(body))
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The import list has problems", problems.Report("the import list", "Nothing was created."))
			return
		}
		if errors.Is(err, settings.ErrNotFound) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such library")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, importListView(created))
}

// routeUpdateImportList parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeUpdateImportList(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.UpdateImportList(w, r, id)
}

// UpdateImportList replaces an import list's fields wholesale. Administrator only.
func (s *Server) UpdateImportList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateImportListJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	updated, err := s.auth.Settings.UpdateImportList(r.Context(), id, importListRequestFromWrite(body))
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The import list has problems", problems.Report("the import list", "Nothing was changed."))
			return
		}
		if errors.Is(err, settings.ErrNotFound) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such import list")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, importListView(updated))
}

// routeDeleteImportList parses the id path parameter, mirroring routeDeleteCategory.
func (s *Server) routeDeleteImportList(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return
	}
	s.DeleteImportList(w, r, id)
}

// DeleteImportList removes an import list. Administrator only.
func (s *Server) DeleteImportList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteImportList(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func importListView(list settings.ImportList) gen.ImportList {
	view := gen.ImportList{
		Id: list.ID, CategoryId: list.CategoryID, Name: list.Name, Type: list.Type,
		CadenceHours: int(list.CadenceHours), Monitor: list.Monitor,
		QualityProfileId: list.QualityProfileID, Exclusions: list.Exclusions, Enabled: list.Enabled,
	}
	if list.URL != "" {
		view.Url = &list.URL
	}
	return view
}

func importListRequestFromWrite(body gen.ImportListWrite) settings.ImportListRequest {
	request := settings.ImportListRequest{
		Name: body.Name, Type: body.Type, CadenceHours: int64(body.CadenceHours),
		QualityProfileID: body.QualityProfileId, Monitor: true, Enabled: true,
	}
	if body.Url != nil {
		request.URL = *body.Url
	}
	if body.Exclusions != nil {
		request.Exclusions = *body.Exclusions
	}
	if body.Monitor != nil {
		request.Monitor = *body.Monitor
	}
	if body.Enabled != nil {
		request.Enabled = *body.Enabled
	}
	return request
}
