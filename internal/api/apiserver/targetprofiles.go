package apiserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

func targetProfileView(profile settings.TargetProfile) gen.TargetProfile {
	view := gen.TargetProfile{
		Id: &profile.ID, Name: profile.Name, MediaTypes: profile.MediaTypes, Description: &profile.Description,
		Resolution: &profile.Resolution, VideoCodec: &profile.VideoCodec, EncoderPreset: &profile.EncoderPreset, EncoderTune: &profile.EncoderTune,
		Deinterlace: &profile.Deinterlace, AudioCodec: &profile.AudioCodec, AudioChannels: &profile.AudioChannels,
		AudioBitrateKbps: profile.AudioBitrateKbps, BitrateKbps: profile.BitrateKbps, RecommendedTarget: &profile.RecommendedTarget, Builtin: &profile.Builtin,
	}
	for into, value := range map[**float32]float64{&view.FrameRate: profile.FrameRate, &view.MaxFrameRate: profile.MaxFrameRate, &view.Crf: profile.CRF} {
		if value > 0 {
			number := float32(value)
			*into = &number
		}
	}
	if profile.SampleRateHz > 0 {
		view.SampleRateHz = &profile.SampleRateHz
	}
	if profile.BitDepth > 0 {
		view.BitDepth = &profile.BitDepth
	}
	return view
}

func targetProfileFromBody(body gen.TargetProfile) settings.TargetProfile {
	return settings.TargetProfile{
		Name: strings.TrimSpace(body.Name), MediaTypes: lowerAll(body.MediaTypes), Description: strings.TrimSpace(derefOr(body.Description, "")),
		Resolution: strings.ToLower(strings.TrimSpace(derefOr(body.Resolution, ""))), VideoCodec: strings.ToLower(strings.TrimSpace(derefOr(body.VideoCodec, ""))),
		FrameRate: float64(derefOr(body.FrameRate, 0)), MaxFrameRate: float64(derefOr(body.MaxFrameRate, 0)), CRF: float64(derefOr(body.Crf, 0)),
		EncoderPreset: strings.ToLower(strings.TrimSpace(derefOr(body.EncoderPreset, ""))), EncoderTune: strings.ToLower(strings.TrimSpace(derefOr(body.EncoderTune, ""))),
		Deinterlace: derefOr(body.Deinterlace, false), AudioCodec: strings.ToLower(strings.TrimSpace(derefOr(body.AudioCodec, ""))),
		AudioChannels: strings.TrimSpace(derefOr(body.AudioChannels, "")), AudioBitrateKbps: body.AudioBitrateKbps,
		SampleRateHz: derefOr(body.SampleRateHz, 0), BitDepth: derefOr(body.BitDepth, 0), BitrateKbps: body.BitrateKbps,
		RecommendedTarget: strings.TrimSpace(derefOr(body.RecommendedTarget, "")),
	}
}

// ListTargetProfiles answers every content profile.
func (s *Server) ListTargetProfiles(w http.ResponseWriter, _ *http.Request) {
	profiles := s.auth.Settings.Snapshot().TargetProfiles()
	views := make([]gen.TargetProfile, len(profiles))
	for i, profile := range profiles {
		views[i] = targetProfileView(profile)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func (s *Server) readTargetProfile(w http.ResponseWriter, r *http.Request) (settings.TargetProfile, bool) {
	var body gen.TargetProfile
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return settings.TargetProfile{}, false
	}
	return targetProfileFromBody(body), true
}

// CreateTargetProfile adds a content profile. Administrator only.
func (s *Server) CreateTargetProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	profile, ok := s.readTargetProfile(w, r)
	if !ok {
		return
	}
	created, err := s.auth.Settings.CreateTargetProfile(r.Context(), profile)
	if err != nil {
		s.writeTargetError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, targetProfileView(created))
}

func (s *Server) routeUpdateTargetProfile(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "an id must be a number"); err == nil {
		s.UpdateTargetProfile(w, r, id)
	}
}

// UpdateTargetProfile replaces a content profile. Administrator only.
func (s *Server) UpdateTargetProfile(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	profile, ok := s.readTargetProfile(w, r)
	if !ok {
		return
	}
	profile.ID = id
	if err := s.auth.Settings.UpdateTargetProfile(r.Context(), profile); err != nil {
		s.writeTargetError(w, err)
		return
	}
	updated, _ := s.auth.Settings.Snapshot().TargetProfile(id)
	writeJSON(w, s.logger, http.StatusOK, targetProfileView(updated))
}

func (s *Server) routeDeleteTargetProfile(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "an id must be a number"); err == nil {
		s.DeleteTargetProfile(w, r, id)
	}
}

// DeleteTargetProfile removes a content profile. Administrator only.
func (s *Server) DeleteTargetProfile(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteTargetProfile(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
