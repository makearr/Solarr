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
		CutoffProfileId: category.QualityCutoffID,
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
		if len(view.Profiles) > 0 {
			first := view.Profiles[0]
			if fits, known, reason := profileFits(snapshot, first.Id, targetFacts(*target)); known && !fits {
				view.Warnings = append(view.Warnings, fmt.Sprintf(
					"The target does not fall in %s, the first profile: %s.", first.Name, reason))
			}
		}
	}
	return view
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

// profileFits is quality.ProfileFits, kept under its old name for this package's callers.
func profileFits(snapshot *settings.Snapshot, profileID int64, facts score.Facts) (fits, known bool, reason string) {
	return quality.ProfileFits(snapshot, profileID, facts)
}
