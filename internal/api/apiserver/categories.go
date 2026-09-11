package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// ListCategories returns every category the caller may read (ADR-27), for
// navigation rather than access management -- GetMe's categories carry a
// permission field for that purpose, this is deliberately narrower.
func (s *Server) ListCategories(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	visible := access.Categories(settings.PermissionRead)
	views := make([]gen.LibraryCategory, len(visible))
	for index, category := range visible {
		works, files := s.categoryCounts(r.Context(), category.ID)
		views[index] = gen.LibraryCategory{
			Id: category.ID, MediaType: category.MediaType, Name: category.Name,
			Works: &works, Files: &files,
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// CreateCategory adds a category (administrator only) -- the manual half of
// what POST /settings/import's category entries already do, so standing up a
// category no longer needs a hand-written YAML document.
func (s *Server) CreateCategory(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.CreateCategoryJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	priorityRank := body.PriorityRank
	if priorityRank == nil {
		access, err := authz.FromContext(r.Context())
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		next := nextCategoryPriorityRank(access)
		priorityRank = &next
	}

	created, err := s.auth.Settings.CreateCategory(r.Context(), body.MediaType, body.Name, *priorityRank)
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The category has problems", problems.Report("the category", "Nothing was created."))
			return
		}
		writeError(w, s.logger, err)
		return
	}

	works, files := s.categoryCounts(r.Context(), created.ID)
	writeJSON(w, s.logger, http.StatusCreated, gen.LibraryCategory{
		Id: created.ID, MediaType: created.MediaType, Name: created.Name,
		Works: &works, Files: &files,
	})
}

// nextCategoryPriorityRank places an omitted rank after every existing
// category, so creating one from the interface never requires the caller to
// already understand ADR-36's ordering. Reads through access (an
// administrator, since CreateCategory is admin-only) rather than the raw
// snapshot -- archtest's own TestHandlersCannotBypassCategoryFiltering
// forbids the latter in every apiserver handler, without exception, so
// staying consistent here is cheaper than arguing this call site is safe.
func nextCategoryPriorityRank(access authz.Access) int64 {
	var highest int64
	for _, category := range access.Categories(settings.PermissionRead) {
		if category.PriorityRank > highest {
			highest = category.PriorityRank
		}
	}
	return highest + 1
}

// routeDeleteCategory parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a category id must be a number")
		return
	}
	s.DeleteCategory(w, r, id)
}

// DeleteCategory removes a category (administrator only), refused with the
// references that still hold it (Invariant 41, settings.ErrInUse via
// writeError).
func (s *Server) DeleteCategory(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteCategory(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routeAttachRootFolderToCategory parses the id path parameter, mirroring
// routeDeleteCategory.
func (s *Server) routeAttachRootFolderToCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a category id must be a number")
		return
	}
	s.AttachRootFolderToCategory(w, r, id)
}

// routeDetachRootFolderFromCategory parses the id and rootFolderId path
// parameters, mirroring routeDeleteCategory.
func (s *Server) routeDetachRootFolderFromCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a category id must be a number")
		return
	}
	rootFolderID, err := strconv.ParseInt(chi.URLParam(r, "rootFolderId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a root folder id must be a number")
		return
	}
	s.DetachRootFolderFromCategory(w, r, id, rootFolderID)
}

// AttachRootFolderToCategory puts a root folder into a category (section
// 11.7's "a category spans many roots"). Administrator only. AcceptsNew
// defaults true -- an attach is never a drain (ADR-51's own separate action).
func (s *Server) AttachRootFolderToCategory(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.AttachRootFolderToCategoryJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with root_folder_id")
		return
	}

	acceptsNew := true
	if body.AcceptsNew != nil {
		acceptsNew = *body.AcceptsNew
	}

	err := s.auth.Settings.AttachRootFolder(r.Context(), settings.CategoryRoot{
		CategoryID: id, RootFolderID: body.RootFolderId, Weight: 1, AcceptsNew: acceptsNew,
	})
	if err != nil {
		if errors.Is(err, settings.ErrNotFound) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound,
				"Not found", "the category or the root folder does not exist")
			return
		}
		writeError(w, s.logger, err)
		return
	}

	folder, found := s.auth.Settings.Snapshot().RootFolder(body.RootFolderId)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such root folder")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.rootFolderView(folder))
}

// DetachRootFolderFromCategory takes a root folder out of a category.
// Administrator only.
func (s *Server) DetachRootFolderFromCategory(w http.ResponseWriter, r *http.Request, id int64, rootFolderID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DetachRootFolder(r.Context(), id, rootFolderID); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// categoryCounts reads the "works"/"files" metrics library.CategoryCounters
// reconciles (never a live COUNT, section 21.12). Unavailable or never-reconciled
// answers 0, the same "render without the tile" fallback libraryCounts uses,
// rather than failing the whole category list over one figure.
func (s *Server) categoryCounts(ctx context.Context, categoryID int64) (works, files int) {
	if s.librarySize == nil {
		return 0, 0
	}
	values, err := s.librarySize.Counters(ctx, fmt.Sprintf("category:%d", categoryID))
	if err != nil {
		s.logger.WarnContext(ctx, "category counters unavailable",
			"category_id", categoryID, "error", err)
		return 0, 0
	}
	return int(values["works"]), int(values["files"])
}
