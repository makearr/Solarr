package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

func (s *Server) routeGetLibraryFileManagement(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.GetLibraryFileManagement(w, r, id)
	}
}

func (s *Server) routeSetLibraryFileManagement(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.SetLibraryFileManagement(w, r, id)
	}
}

// GetLibraryFileManagement answers a library's File Management and Permissions.
func (s *Server) GetLibraryFileManagement(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, fileManagementView(category.FileManagement))
}

// SetLibraryFileManagement saves them; every problem is reported at once and nothing is saved.
func (s *Server) SetLibraryFileManagement(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if _, err := s.libraryFor(r, id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body gen.FileManagement
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected the file management settings")
		return
	}
	wanted := settings.FileManagement{
		PropersRepacks: settings.PropersRepacks(body.PropersRepacks), RescanAfterRefresh: settings.RescanAfterRefresh(body.RescanAfterRefresh),
		ChangeFileDate: settings.ChangeFileDate(body.ChangeFileDate), RecycleRetentionDays: body.RecycleRetentionDays,
		DeleteEmptyFolders: body.DeleteEmptyFolders, CreateEmptyArtistFolders: body.CreateEmptyArtistFolders,
		UnmonitorDeleted: body.UnmonitorDeleted, SetPermissions: body.SetPermissions,
		ChmodFolder: body.ChmodFolder, ChownGroup: body.ChownGroup,
	}
	if err := s.auth.Settings.SetCategoryFileManagement(r.Context(), id, wanted); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The file management has problems", problems.Report("file management", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, fileManagementView(wanted))
}

func fileManagementView(f settings.FileManagement) gen.FileManagement {
	return gen.FileManagement{
		PropersRepacks:     gen.FileManagementPropersRepacks(f.PropersRepacks),
		RescanAfterRefresh: gen.FileManagementRescanAfterRefresh(f.RescanAfterRefresh),
		ChangeFileDate:     gen.FileManagementChangeFileDate(f.ChangeFileDate), RecycleRetentionDays: f.RecycleRetentionDays,
		DeleteEmptyFolders: f.DeleteEmptyFolders, CreateEmptyArtistFolders: f.CreateEmptyArtistFolders,
		UnmonitorDeleted: f.UnmonitorDeleted, SetPermissions: f.SetPermissions,
		ChmodFolder: f.ChmodFolder, ChownGroup: f.ChownGroup,
	}
}
