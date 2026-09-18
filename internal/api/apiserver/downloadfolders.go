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

// DownloadFolderChecker inspects a candidate download folder -- read-only,
// unlike RootChecker's own write probe (section 5.1): a download folder is
// never written to by Solarr, only watched.
type DownloadFolderChecker interface {
	// Validate refuses a path that cannot be a download folder at all.
	Validate(path string) error
	// Readable reports whether this process can list the folder's contents.
	Readable(path string) error
}

// ListDownloadFolders answers every configured download folder.
func (s *Server) ListDownloadFolders(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	folders, err := s.auth.Settings.DownloadFolders(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.DownloadFolder, len(folders))
	for index, folder := range folders {
		views[index] = downloadFolderView(folder)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// AddDownloadFolder watches a new folder. Administrator only. The path is
// probed for existence and readability the same way AddRootFolder probes
// writability -- but unlike a root folder, an unreadable download folder is
// still accepted and flagged, matching AddRootFolder's own "reported, never
// refused" ownership stance: there is nothing to chown, it is someone
// else's client writing there.
func (s *Server) AddDownloadFolder(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.AddDownloadFolderJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with a path")
		return
	}

	readable, reason := true, ""
	if s.downloadRoots != nil {
		if err := s.downloadRoots.Validate(body.Path); err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "The folder cannot be used", err.Error())
			return
		}
		if err := s.downloadRoots.Readable(body.Path); err != nil {
			readable, reason = false, err.Error()
			s.logger.Warn("download folder is not readable by this process", "path", body.Path, "detail", reason)
		}
	}

	request := settings.DownloadFolderRequest{Path: body.Path, Readable: readable, ReadableReason: reason, CategoryID: body.CategoryId}
	if body.Label != nil {
		request.Label = *body.Label
	}

	created, err := s.auth.Settings.CreateDownloadFolder(r.Context(), request)
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The download folder has problems", problems.Report("the download folder", "Nothing was created."))
			return
		}
		if errors.Is(err, settings.ErrNotFound) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such category")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, downloadFolderView(created))
}

// routeDeleteDownloadFolder parses the id path parameter, mirroring routeDrainRootFolder.
func (s *Server) routeDeleteDownloadFolder(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a download folder id must be a number")
		return
	}
	s.DeleteDownloadFolder(w, r, id)
}

// DeleteDownloadFolder stops watching a folder. Administrator only.
func (s *Server) DeleteDownloadFolder(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteDownloadFolder(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routeListDownloadFolderContents parses the id path parameter, mirroring routeDrainRootFolder.
func (s *Server) routeListDownloadFolderContents(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a download folder id must be a number")
		return
	}
	s.ListDownloadFolderContents(w, r, id)
}

// ListDownloadFolderContents reads a folder's own top-level entries live,
// every call -- no cache, no comparison against what is already imported
// (stage 5's own job, not built yet).
func (s *Server) ListDownloadFolderContents(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.fsys == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "download folder contents are not available")
		return
	}

	folder, err := s.auth.Settings.GetDownloadFolder(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such download folder")
		return
	}

	entries, err := s.fsys.ReadDir(folder.Path)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.DownloadFolderEntry, len(entries))
	for index, entry := range entries {
		views[index] = gen.DownloadFolderEntry{Name: entry.Name, IsDir: entry.IsDir}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func downloadFolderView(folder settings.DownloadFolder) gen.DownloadFolder {
	view := gen.DownloadFolder{
		Id: folder.ID, Path: folder.Path, CategoryId: folder.CategoryID, Readable: folder.Readable,
	}
	if folder.Label != "" {
		label := folder.Label
		view.Label = &label
	}
	if folder.ReadableReason != "" {
		reason := folder.ReadableReason
		view.ReadableReason = &reason
	}
	return view
}
