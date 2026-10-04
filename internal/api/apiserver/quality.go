package apiserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/config"
)

func (s *Server) routeGetLibraryQuality(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.GetLibraryQuality(w, r, id)
	}
}

func (s *Server) routeSetLibraryQuality(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.SetLibraryQuality(w, r, id)
	}
}

// GetLibraryQuality answers a library's ordered profiles, cutoff, target and warnings.
func (s *Server) GetLibraryQuality(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.libraryQualityView(category))
}

// SetLibraryQuality saves a library's quality; every problem is reported at once.
func (s *Server) SetLibraryQuality(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body gen.LibraryQualityWrite
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}
	err = s.auth.Settings.SetCategoryQuality(r.Context(), id, body.ProfileIds, body.CutoffProfileId, body.TargetId)
	if err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The quality settings have problems", problems.Report("the quality settings", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	if body.UpgradesAllowed != nil && *body.UpgradesAllowed != category.QualityUpgrades {
		if err := s.auth.Settings.SetCategoryQualityUpgrades(r.Context(), id, *body.UpgradesAllowed); err != nil {
			writeError(w, s.logger, err)
			return
		}
		category.QualityUpgrades = *body.UpgradesAllowed
		// The cutoff check re-reads every album at once, so Cutoff Unmet follows the switch.
		if s.scheduler != nil {
			if err := s.scheduler.RunNow(r.Context(), cutoffTaskKey, false); err != nil {
				s.logger.InfoContext(r.Context(), "cutoff check after an upgrades change not started", "error", err)
			}
		}
	}
	if body.PreferredProtocol != nil || body.ProtocolDelayMinutes != nil {
		protocol, minutes := category.PreferredProtocol, category.ProtocolDelayMinutes
		if body.PreferredProtocol != nil {
			protocol = string(*body.PreferredProtocol)
		}
		if body.ProtocolDelayMinutes != nil {
			minutes = *body.ProtocolDelayMinutes
		}
		if err := s.auth.Settings.SetCategoryProtocolDelay(r.Context(), id, protocol, minutes); err != nil {
			var problems config.Problems
			if errors.As(err, &problems) {
				writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
					"The delay profile has problems", problems.Report("the delay profile", "Nothing was changed."))
				return
			}
			writeError(w, s.logger, err)
			return
		}
		category.PreferredProtocol, category.ProtocolDelayMinutes = protocol, minutes
	}
	// The request's Access is frozen at its start (ADR-27), so the answer is what was saved.
	category.QualityProfileIDs = body.ProfileIds
	category.QualityCutoffID = body.CutoffProfileId
	category.QualityTarget = nil
	if body.TargetId != nil {
		if target, found := s.auth.Settings.Snapshot().QualityTarget(*body.TargetId); found {
			category.QualityTarget = &target
		}
	}
	writeJSON(w, s.logger, http.StatusOK, s.libraryQualityView(category))
}

func (s *Server) libraryQualityView(category settings.Category) gen.LibraryQuality {
	snapshot := s.auth.Settings.Snapshot()
	view := gen.LibraryQuality{
		Profiles: []gen.QualityProfileRef{}, Available: []gen.QualityProfileRef{}, Warnings: []string{},
		CutoffProfileId: category.QualityCutoffID, UpgradesAllowed: category.QualityUpgrades,
		PreferredProtocol: gen.LibraryQualityPreferredProtocol(category.PreferredProtocol), ProtocolDelayMinutes: category.ProtocolDelayMinutes,
	}
	for _, id := range category.QualityProfileIDs {
		if profile, found := snapshot.QualityProfile(id); found {
			view.Profiles = append(view.Profiles, gen.QualityProfileRef{Id: profile.ID, Name: profile.Name})
		}
	}
	for _, profile := range snapshot.QualityProfiles() {
		if profile.MediaType == category.MediaType {
			view.Available = append(view.Available, gen.QualityProfileRef{Id: profile.ID, Name: profile.Name})
		}
	}
	available := []gen.QualityProfileRef{}
	for _, target := range snapshot.QualityTargets() {
		if target.MediaType == category.MediaType {
			available = append(available, gen.QualityProfileRef{Id: target.ID, Name: target.Name})
		}
	}
	view.AvailableTargets = &available
	if target := category.QualityTarget; target != nil {
		shown := qualityTargetView(*target)
		view.Target = &shown
		if warning := targetOutsideProfiles(snapshot, view.Profiles, targetFacts(*target)); warning != "" {
			view.Warnings = append(view.Warnings, warning)
		}
	}
	return view
}

// targetOutsideProfiles warns only when no profile of the library takes the target, in any tier of any
// profile (asked 2026-09-30: AAC is Space conscious' second codec). Empty when one does or none reads.
func targetOutsideProfiles(snapshot *settings.Snapshot, profiles []gen.QualityProfileRef, facts score.Facts) string {
	var reasons []string
	for _, profile := range profiles {
		accepts, known, reason := quality.ProfileAccepts(snapshot, profile.Id, facts)
		if !known {
			continue
		}
		if accepts {
			return ""
		}
		reasons = append(reasons, profile.Name+": "+reason)
	}
	if len(reasons) == 0 {
		return ""
	}
	return fmt.Sprintf("The target falls in none of this library's quality profiles (%s).", strings.Join(reasons, "; "))
}

func lowerAll(values []string) []string {
	var out []string
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// targetFacts is the target as the file facts a profile judges.
func targetFacts(target settings.QualityTarget) score.Facts {
	facts := score.Facts{File: score.FileFacts{
		AudioCodec: target.AudioCodec, Extension: target.Container, Lossless: target.BitrateKbps == nil && target.AudioCodec != "",
		Resolution: target.Resolution, VideoCodec: target.VideoCodec, HDR: target.HDR, AudioChannels: target.AudioChannels,
	}}
	if target.BitrateKbps != nil {
		facts.File.AudioBitrate = *target.BitrateKbps * 1000
		facts.File.OverallBitrate = facts.File.AudioBitrate
	}
	return facts
}

// qualityTargetView is a target as the API shows it.
func qualityTargetView(target settings.QualityTarget) gen.QualityTarget {
	view := gen.QualityTarget{
		Id: &target.ID, Name: &target.Name, MediaType: &target.MediaType,
		AudioCodec: target.AudioCodec, Container: target.Container, BitrateKbps: target.BitrateKbps, TolerancePct: target.TolerancePct,
		AudioBitrateKbps: target.AudioBitrateKbps, VideoBitrateKbps: target.VideoBitrateKbps,
	}
	setPtr := func(into **string, value string) {
		if value != "" {
			*into = &value
		}
	}
	setPtr(&view.Resolution, target.Resolution)
	setPtr(&view.VideoCodec, target.VideoCodec)
	setPtr(&view.Hdr, target.HDR)
	setPtr(&view.AudioChannels, target.AudioChannels)
	if len(target.AudioLanguages) > 0 {
		view.AudioLanguages = &target.AudioLanguages
	}
	if len(target.SubtitleLanguages) > 0 {
		view.SubtitleLanguages = &target.SubtitleLanguages
	}
	if target.FrameRate > 0 {
		rate := float32(target.FrameRate)
		view.FrameRate = &rate
	}
	if target.CRF > 0 {
		crf := float32(target.CRF)
		view.Crf = &crf
	}
	return view
}

// qualityTargetFromBody is a request's target as the model; the media type is the caller's to give or keep.
func qualityTargetFromBody(body gen.QualityTarget) settings.QualityTarget {
	return settings.QualityTarget{
		Name: strings.TrimSpace(derefOr(body.Name, "")), MediaType: strings.ToLower(strings.TrimSpace(derefOr(body.MediaType, ""))),
		AudioCodec: strings.ToLower(strings.TrimSpace(body.AudioCodec)), Container: strings.ToLower(strings.TrimSpace(body.Container)),
		BitrateKbps: body.BitrateKbps, TolerancePct: body.TolerancePct,
		Resolution: strings.ToLower(strings.TrimSpace(derefOr(body.Resolution, ""))),
		VideoCodec: strings.ToLower(strings.TrimSpace(derefOr(body.VideoCodec, ""))),
		HDR:        strings.ToLower(strings.TrimSpace(derefOr(body.Hdr, ""))),

		AudioChannels:     strings.TrimSpace(derefOr(body.AudioChannels, "")),
		AudioLanguages:    lowerAll(derefOr(body.AudioLanguages, nil)),
		SubtitleLanguages: lowerAll(derefOr(body.SubtitleLanguages, nil)),
		AudioBitrateKbps:  body.AudioBitrateKbps, VideoBitrateKbps: body.VideoBitrateKbps,
		FrameRate: float64(derefOr(body.FrameRate, 0)), CRF: float64(derefOr(body.Crf, 0)),
	}
}

func (s *Server) ListQualityTargets(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	targets := s.auth.Settings.Snapshot().QualityTargets()
	views := make([]gen.QualityTarget, len(targets))
	for i, target := range targets {
		views[i] = qualityTargetView(target)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// CreateQualityTarget adds a named target. Administrator only.
func (s *Server) CreateQualityTarget(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.QualityTarget
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	created, err := s.auth.Settings.CreateQualityTarget(r.Context(), qualityTargetFromBody(body))
	if err != nil {
		s.writeTargetError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, qualityTargetView(created))
}

func (s *Server) routeUpdateQualityTarget(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "an id must be a number"); err == nil {
		s.UpdateQualityTarget(w, r, id)
	}
}

// UpdateQualityTarget replaces a target; the libraries aiming for it follow. Administrator only.
func (s *Server) UpdateQualityTarget(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.QualityTarget
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	target := qualityTargetFromBody(body)
	target.ID = id
	if err := s.auth.Settings.UpdateQualityTarget(r.Context(), target); err != nil {
		s.writeTargetError(w, err)
		return
	}
	updated, _ := s.auth.Settings.Snapshot().QualityTarget(id)
	writeJSON(w, s.logger, http.StatusOK, qualityTargetView(updated))
}

func (s *Server) routeDeleteQualityTarget(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "an id must be a number"); err == nil {
		s.DeleteQualityTarget(w, r, id)
	}
}

// DeleteQualityTarget removes a target no library aims for. Administrator only.
func (s *Server) DeleteQualityTarget(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteQualityTarget(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeTargetError(w http.ResponseWriter, err error) {
	var problems config.Problems
	if errors.As(err, &problems) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"The quality target has problems", problems.Report("the quality target", "Nothing was changed."))
		return
	}
	writeError(w, s.logger, err)
}
