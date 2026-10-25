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
	writeJSON(w, s.logger, http.StatusOK, s.fileManagementView(category))
}

// SetLibraryFileManagement saves them; every problem is reported at once and nothing is saved.
func (s *Server) SetLibraryFileManagement(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
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
		UnmonitorDeleted: body.UnmonitorDeleted, MergeArtistAliases: body.MergeArtistAliases, SetPermissions: body.SetPermissions,
		ChmodFolder: body.ChmodFolder, ChownGroup: body.ChownGroup, Tagging: category.FileManagement.Tagging,
		ExtraFiles: category.FileManagement.ExtraFiles,
	}
	if body.ImportExtraFiles != nil {
		wanted.ExtraFiles.Import = body.ImportExtraFiles
	}
	if body.ExtraFileExtensions != nil {
		wanted.ExtraFiles.Extensions = *body.ExtraFileExtensions
	}
	wanted.MediaServerFiles = category.FileManagement.MediaServerFiles
	if body.MediaServerFiles != nil {
		wanted.MediaServerFiles = *body.MediaServerFiles
	}
	wanted.IgnoreNewSeasons = category.FileManagement.IgnoreNewSeasons
	if body.MonitorNewSeasons != nil {
		wanted.IgnoreNewSeasons = !*body.MonitorNewSeasons
	}
	wanted.SharedFileFallback = category.FileManagement.SharedFileFallback
	if body.SharedFileFallback != nil {
		wanted.SharedFileFallback = settings.SharedFileFallback(*body.SharedFileFallback)
	}
	wanted.EpisodeTitleRequired = category.FileManagement.EpisodeTitleRequired
	if body.EpisodeTitleRequired != nil {
		wanted.EpisodeTitleRequired = settings.EpisodeTitleRequired(*body.EpisodeTitleRequired)
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
	if wanted.MediaServerFiles && !category.FileManagement.MediaServerFiles && s.scheduler != nil {
		if err := s.scheduler.RunNow(r.Context(), mediaServerFilesTaskKey, false); err != nil {
			s.logger.InfoContext(r.Context(), "jellyfin/kodi files after switching them on not started", "error", err)
		}
	}
	category.FileManagement = wanted
	writeJSON(w, s.logger, http.StatusOK, s.fileManagementView(category))
}

// mediaServerFilesTaskKey is the registered pass writing Jellyfin and Kodi files.
const mediaServerFilesTaskKey = "media_server_files"

func (s *Server) fileManagementView(category settings.Category) gen.FileManagement {
	f := category.FileManagement
	var defaults settings.ExtraFiles
	if info, found := s.mediaTypeInfo(category.MediaType); found {
		defaults = info.ExtraFiles
	}
	extrasOn, extensions := f.ExtraFiles.Resolve(defaults.Import != nil && *defaults.Import, defaults.Extensions)
	if extensions == nil {
		extensions = []string{}
	}
	fallback := gen.FileManagementSharedFileFallback(f.SharedFileFallback)
	titleRequired := gen.FileManagementEpisodeTitleRequired(f.EpisodeTitleRequired)
	if titleRequired == "" {
		titleRequired = gen.FileManagementEpisodeTitleRequired(settings.TitleRequiredAlways)
	}
	monitorNew := !f.IgnoreNewSeasons
	return gen.FileManagement{
		ImportExtraFiles: &extrasOn, ExtraFileExtensions: &extensions, MediaServerFiles: &f.MediaServerFiles,
		SharedFileFallback: &fallback, MonitorNewSeasons: &monitorNew, EpisodeTitleRequired: &titleRequired,
		PropersRepacks:     gen.FileManagementPropersRepacks(f.PropersRepacks),
		RescanAfterRefresh: gen.FileManagementRescanAfterRefresh(f.RescanAfterRefresh),
		ChangeFileDate:     gen.FileManagementChangeFileDate(f.ChangeFileDate), RecycleRetentionDays: f.RecycleRetentionDays,
		DeleteEmptyFolders: f.DeleteEmptyFolders, CreateEmptyArtistFolders: f.CreateEmptyArtistFolders,
		UnmonitorDeleted: f.UnmonitorDeleted, MergeArtistAliases: f.MergeArtistAliases, SetPermissions: f.SetPermissions,
		ChmodFolder: f.ChmodFolder, ChownGroup: f.ChownGroup,
	}
}

func (s *Server) routeGetLibraryTagging(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.GetLibraryTagging(w, r, id)
	}
}

func (s *Server) routeSetLibraryTagging(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.SetLibraryTagging(w, r, id)
	}
}

// GetLibraryTagging answers what the tag writer puts into a library's files.
func (s *Server) GetLibraryTagging(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, taggingView(category.FileManagement.Tagging))
}

// tagWritebackTaskKey is the tag writer's key in the scheduler (cmd/solarr/app/scheduler.go).
const tagWritebackTaskKey = "tag_writeback"

// cutoffTaskKey is the scheduler's quality cutoff check (cmd/solarr/app/scheduler.go).
const cutoffTaskKey = "cutoff"

// SetLibraryTagging saves it, keeping the rest of File management, and starts the tag writer.
func (s *Server) SetLibraryTagging(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body gen.Tagging
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected the tagging settings")
		return
	}
	wanted := category.FileManagement
	wanted.Tagging = settings.Tagging{
		WriteIDs: body.WriteIds, ReplaceIDs: body.ReplaceIds, WriteMetadata: body.WriteMetadata, EmbedCover: body.EmbedCover, Navidrome: body.Navidrome,
	}
	if err := s.auth.Settings.SetCategoryFileManagement(r.Context(), id, wanted); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.scheduler != nil {
		if err := s.scheduler.RunNow(r.Context(), tagWritebackTaskKey, false); err != nil {
			s.logger.InfoContext(r.Context(), "tag writer after a tagging change not started", "error", err)
		}
	}
	writeJSON(w, s.logger, http.StatusOK, taggingView(wanted.Tagging))
}

func taggingView(t settings.Tagging) gen.Tagging {
	return gen.Tagging{WriteIds: t.WriteIDs, ReplaceIds: t.ReplaceIDs, WriteMetadata: t.WriteMetadata, EmbedCover: t.EmbedCover, Navidrome: t.Navidrome}
}
