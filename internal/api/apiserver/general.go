package apiserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

func (s *Server) generalSettings() gen.GeneralSettings {
	snapshot := s.auth.Settings.Snapshot()
	hashing, previews, gaps := snapshot.VideoPerceptualHash(), snapshot.VideoPreviews(), snapshot.HuntGaps()
	mode := gen.GeneralSettingsSolarrMode(snapshot.SolarrMode())
	return gen.GeneralSettings{VideoHashing: &hashing, VideoPreviews: &previews, HuntOldMissing: &gaps, SolarrMode: &mode}
}

// GetGeneralSettings answers the instance-wide switches of Settings -> General.
// Every screen reads it (FileThumb.tsx needs the thumbnail switch), so it stays open to any signed-in account.
func (s *Server) GetGeneralSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, s.generalSettings())
}

// PatchGeneralSettings changes the switches the body names. Administrator only.
func (s *Server) PatchGeneralSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.GeneralSettings
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a JSON object") {
		return
	}
	if body.VideoHashing != nil {
		if err := s.auth.Settings.SetVideoPerceptualHash(r.Context(), s.auth.Clock, *body.VideoHashing); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	if body.VideoPreviews != nil {
		if err := s.auth.Settings.SetVideoPreviews(r.Context(), s.auth.Clock, *body.VideoPreviews); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	if body.HuntOldMissing != nil {
		if err := s.auth.Settings.SetHuntGaps(r.Context(), s.auth.Clock, *body.HuntOldMissing); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	if body.SolarrMode != nil {
		err := s.auth.Settings.SetSolarrMode(r.Context(), s.auth.Clock, settings.SolarrMode(*body.SolarrMode))
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the mode", "Nothing was changed."))
			return
		}
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, s.generalSettings())
}

// VideoPictures starts the sweeps that hash videos, and with previews make their pictures, whatever the settings say.
type VideoPictures interface {
	Generate(ctx context.Context, withPreviews bool) error
}

// GenerateVideoPreviews starts hashing and picture-making for every video still lacking them. Administrator only.
func (s *Server) GenerateVideoPreviews(w http.ResponseWriter, r *http.Request) {
	s.generateVideoPictures(w, r, true)
}

// GenerateVideoHashes starts hashing for every video still lacking a hash. Administrator only.
func (s *Server) GenerateVideoHashes(w http.ResponseWriter, r *http.Request) {
	s.generateVideoPictures(w, r, false)
}

func (s *Server) generateVideoPictures(w http.ResponseWriter, r *http.Request, withPreviews bool) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.videoPictures == nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "video pictures are not available here")
		return
	}
	if err := s.videoPictures.Generate(r.Context(), withPreviews); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// UnreadableFiles makes the files the probe gave up on due again.
type UnreadableFiles interface {
	// Retry answers how many files will be read again; the sweep that reads them is queued.
	Retry(ctx context.Context) (int64, error)
}

// RetryUnreadableFiles has the probe read again every file it left alone because ffprobe could not read it. Administrator only.
func (s *Server) RetryUnreadableFiles(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.unreadableFiles == nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "reading files again is not available here")
		return
	}
	count, err := s.unreadableFiles.Retry(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.UnreadableFilesRetry{Count: count})
}
