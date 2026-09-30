package apiserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
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
	var target *settings.QualityTarget
	if body.Target != nil {
		target = &settings.QualityTarget{
			AudioCodec:  strings.ToLower(strings.TrimSpace(body.Target.AudioCodec)),
			Container:   strings.ToLower(strings.TrimSpace(body.Target.Container)),
			BitrateKbps: body.Target.BitrateKbps, TolerancePct: body.Target.TolerancePct,
		}
	}
	err = s.auth.Settings.SetCategoryQuality(r.Context(), id, body.ProfileIds, body.CutoffProfileId, target)
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
	// The request's Access is frozen at its start (ADR-27), so the answer is what was saved.
	category.QualityProfileIDs = body.ProfileIds
	category.QualityCutoffID = body.CutoffProfileId
	category.QualityTarget = target
	writeJSON(w, s.logger, http.StatusOK, s.libraryQualityView(category))
}

func (s *Server) libraryQualityView(category settings.Category) gen.LibraryQuality {
	snapshot := s.auth.Settings.Snapshot()
	view := gen.LibraryQuality{
		Profiles: []gen.QualityProfileRef{}, Available: []gen.QualityProfileRef{}, Warnings: []string{},
		CutoffProfileId: category.QualityCutoffID, UpgradesAllowed: category.QualityUpgrades,
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
	if target := category.QualityTarget; target != nil {
		view.Target = &gen.QualityTarget{
			AudioCodec: target.AudioCodec, Container: target.Container,
			BitrateKbps: target.BitrateKbps, TolerancePct: target.TolerancePct,
		}
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

// targetFacts is the target as the file facts a profile judges.
func targetFacts(target settings.QualityTarget) score.Facts {
	facts := score.Facts{File: score.FileFacts{
		AudioCodec: target.AudioCodec, Container: target.Container, Lossless: target.BitrateKbps == nil,
	}}
	if target.BitrateKbps != nil {
		facts.File.AudioBitrate = *target.BitrateKbps * 1000
		facts.File.OverallBitrate = facts.File.AudioBitrate
	}
	return facts
}
