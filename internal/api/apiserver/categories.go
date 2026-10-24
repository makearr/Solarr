package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
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
			Id: category.ID, MediaType: category.MediaType, Name: category.Name, Kind: strPtr(category.Kind),
			Works: &works, Files: &files,
		}
		if category.LibraryID != 0 {
			views[index].LibraryId = &category.LibraryID
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

	if s.refuseUnknownMediaType(w, body.MediaType) {
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

	info, _ := s.mediaTypeInfo(body.MediaType)
	kind, known := info.kindOf(derefOr(body.Kind, ""))
	if !known {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Unknown kind",
			fmt.Sprintf("a %s library has no kind %q", body.MediaType, derefOr(body.Kind, "")))
		return
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

	if kind.Key != "" {
		if created, err = s.applyKind(r.Context(), created, kind); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}

	works, files := s.categoryCounts(r.Context(), created.ID)
	writeJSON(w, s.logger, http.StatusCreated, gen.LibraryCategory{
		Id: created.ID, MediaType: created.MediaType, Name: created.Name, Kind: strPtr(created.Kind),
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

// routeGetCategory parses the id path parameter, mirroring routeDeleteCategory.
func (s *Server) routeGetCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a category id must be a number")
		return
	}
	s.GetCategory(w, r, id)
}

// GetCategory answers one category's full settings-side detail --
// LibraryCategory (ListCategories) stays lean for the many navigation
// callers that never need any of this. Administrator only. Reads through
// Access rather than the raw snapshot (archtest's own
// TestHandlersCannotBypassCategoryFiltering forbids the latter in every
// apiserver handler, without exception).
func (s *Server) GetCategory(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(id, settings.PermissionManage)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, categorySettingsView(category))
}

// routeUpdateCategory parses the id path parameter, mirroring routeDeleteCategory.
func (s *Server) routeUpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a category id must be a number")
		return
	}
	s.UpdateCategory(w, r, id)
}

// UpdateCategory renames/reprioritises a category (todo 4.6's own remaining
// gap: creation and deletion existed, editing did not) and sets its default
// quality/language profile and monitor-discovered policy, all through one
// settings.Cache.UpdateCategory call. Administrator only. Deliberately not
// three separate settings.Cache writes plus a final Access-based read for
// the response: Access is frozen for a request's lifetime (ADR-27), so a
// handler that wrote through it and then read the same Access back would
// see the category as it looked when the request began, not what it had
// just written -- found live verifying this exact endpoint (2026-09-12).
// media_type never changes -- it is identity, not policy.
func (s *Server) UpdateCategory(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateCategoryJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	monitorDiscovered := settings.MonitorDiscoveredMonitored
	if body.MonitorDiscovered != nil {
		monitorDiscovered = string(*body.MonitorDiscovered)
	}
	// A further media type of a library keeps the library's name and priority; those are changed on the library.
	library := s.auth.Settings.Snapshot().LibraryOf(id)
	if categories := s.auth.Settings.Snapshot().LibraryCategories(id); len(categories) > 0 && library != id {
		body.Name, body.PriorityRank = categories[0].Name, categories[0].PriorityRank
	}
	updated, err := s.auth.Settings.UpdateCategory(r.Context(), id, body.Name, body.PriorityRank,
		body.QualityProfileId, body.LanguageProfileId, monitorDiscovered)
	if err == nil && body.TvMonitor != nil {
		updated, err = s.auth.Settings.SetCategoryTVMonitor(r.Context(), id, string(*body.TvMonitor))
	}
	if err == nil && body.RatingLimit != nil {
		if info, _ := s.mediaTypeInfo(updated.MediaType); !info.Ratings {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "No ratings",
				fmt.Sprintf("a %s library has no ratings to limit", updated.MediaType))
			return
		}
		_, err = s.auth.Settings.SetCategoryRatingLimit(r.Context(), library, body.RatingLimit.MaxAge, body.RatingLimit.AllowUnrated)
		if err == nil {
			err = s.auth.Settings.SyncLibrary(r.Context(), library)
		}
		for _, category := range s.auth.Settings.Snapshot().LibraryCategories(library) {
			if err == nil && s.replanning != nil {
				err = s.replanning.MarkLibraryDirty(r.Context(), category.ID)
			}
		}
		updated = categoryOf(s.auth.Settings.Snapshot().LibraryCategories(id), id, updated)
	}
	if err == nil && body.Kind != nil {
		info, _ := s.mediaTypeInfo(updated.MediaType)
		if _, known := info.kindOf(*body.Kind); !known || *body.Kind == "" && len(info.Kinds) > 0 {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Unknown kind",
				fmt.Sprintf("a %s library has no kind %q", updated.MediaType, *body.Kind))
			return
		}
		updated, err = s.auth.Settings.SetCategoryKind(r.Context(), id, *body.Kind)
	}
	if err == nil {
		err = s.auth.Settings.SyncLibrary(r.Context(), library)
		updated = categoryOf(s.auth.Settings.Snapshot().LibraryCategories(id), id, updated)
	}
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The category has problems", problems.Report("the category", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, categorySettingsView(updated))
}

func (s *Server) routeAddLibraryType(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.workIDParam(w, r); ok {
		s.AddLibraryType(w, r, id)
	}
}

// AddLibraryType lets a library hold a further media type; administrator only.
func (s *Server) AddLibraryType(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.AddLibraryTypeJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	if _, known := s.mediaTypeInfo(body.MediaType); !known {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Unknown media type", fmt.Sprintf("no media type %q", body.MediaType))
		return
	}
	member, err := s.auth.Settings.AddLibraryType(r.Context(), id, body.MediaType)
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The library cannot hold that", problems.Report("the media type", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, categorySettingsView(member))
}

func tvMonitorView(policy string) *gen.CategorySettingsTvMonitor {
	if policy == "" {
		return nil
	}
	typed := gen.CategorySettingsTvMonitor(policy)
	return &typed
}

func categorySettingsView(c settings.Category) gen.CategorySettings {
	return gen.CategorySettings{
		Id: c.ID, MediaType: c.MediaType, Name: c.Name, PriorityRank: c.PriorityRank,
		QualityProfileId: c.QualityProfileID, LanguageProfileId: c.LanguageProfileID,
		MonitorDiscovered: gen.CategorySettingsMonitorDiscovered(c.MonitorDiscovered),
		TvMonitor:         tvMonitorView(c.TVMonitor),
		Kind:              strPtr(c.Kind),
		RatingLimit:       gen.RatingLimit{MaxAge: c.MaxAge, AllowUnrated: c.AllowUnrated},
	}
}

// applyKind gives a library just added its kind and the kind's defaults: its quality profiles, best first, and its
// language profile, created under the kind's name when no profile has it.
func (s *Server) applyKind(ctx context.Context, category settings.Category, kind LibraryKind) (settings.Category, error) {
	updated, err := s.auth.Settings.SetCategoryKind(ctx, category.ID, kind.Key)
	if err != nil {
		return category, err
	}
	snapshot := s.auth.Settings.Snapshot()
	var profileIDs []int64
	for _, name := range kind.QualityProfiles {
		for _, profile := range snapshot.QualityProfiles() {
			if profile.Name == name && profile.MediaType == category.MediaType {
				profileIDs = append(profileIDs, profile.ID)
			}
		}
	}
	if len(profileIDs) > 0 {
		if err := s.auth.Settings.SetCategoryQuality(ctx, category.ID, profileIDs, nil, nil); err != nil {
			return updated, err
		}
	}
	if kind.Language != nil {
		languageID := int64(0)
		for _, profile := range snapshot.LanguageProfiles() {
			if profile.Name == kind.Language.Name {
				languageID = profile.ID
			}
		}
		if languageID == 0 {
			created, err := s.auth.Settings.CreateLanguageProfile(ctx, *kind.Language)
			if err != nil {
				return updated, err
			}
			languageID = created.ID
		}
		if err := s.auth.Settings.SetCategoryLanguageProfile(ctx, category.ID, &languageID); err != nil {
			return updated, err
		}
	}
	return updated, nil
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
	s.scanAttachedRoot(r.Context(), body.RootFolderId)
	writeJSON(w, s.logger, http.StatusOK, s.rootFolderView(folder))
}

// scanTaskKey is the library scan's key in the scheduler (cmd/solarr/app/scheduler.go).
const scanTaskKey = "scan"

// scanAttachedRoot starts the library scan now: a new folder otherwise showed nothing until the
// six-hourly scan (found 2026-09-28). Best-effort; a scan already queued is logged, not an error.
func (s *Server) scanAttachedRoot(ctx context.Context, rootFolderID int64) {
	if s.scheduler == nil {
		return
	}
	if err := s.scheduler.RunNow(ctx, scanTaskKey, false); err != nil {
		s.logger.WarnContext(ctx, "scan after attaching a root folder not started",
			"root_folder_id", rootFolderID, "error", err)
	}
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

// Replanning has every item of a library planned again. Satisfied by library.Desired.
type Replanning interface {
	MarkLibraryDirty(ctx context.Context, categoryID int64) error
}

// categoryOf is the category id among a library's, or fallback when it is not there.
func categoryOf(categories []settings.Category, id int64, fallback settings.Category) settings.Category {
	for _, category := range categories {
		if category.ID == id {
			return category
		}
	}
	return fallback
}
