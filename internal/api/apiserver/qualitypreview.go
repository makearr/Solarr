package apiserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// QualityChange is a library's quality as a person proposes it, not yet saved.
type QualityChange struct {
	ProfileIDs      []int64
	CutoffProfileID *int64
	TargetID        *int64
	TargetAuto      bool
	UpgradesAllowed *bool
}

// QualityChangePreview counts a library's items by whether a quality change makes them upgradable. A reason with
// Available false says why nothing was counted.
type QualityChangePreview struct {
	Available                                                               bool
	Reason                                                                  string
	Items, UpgradableNow, UpgradableAfter, BecomeUpgradable, StopUpgradable int64
}

// QualityPreviewer judges a library's files under a proposed quality. Defined here, implemented by the caller.
type QualityPreviewer func(ctx context.Context, categoryID int64, change QualityChange) (QualityChangePreview, error)

func (s *Server) routePreviewLibraryQuality(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.PreviewLibraryQuality(w, r, id)
	}
}

// PreviewLibraryQuality answers how many items a library's proposed quality would make upgradable, saving nothing.
func (s *Server) PreviewLibraryQuality(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if _, err := s.libraryFor(r, id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.qualityPreview == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the quality preview is not available")
		return
	}
	var body gen.LibraryQualityWrite
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	preview, err := s.qualityPreview(r.Context(), id, QualityChange{
		ProfileIDs: body.ProfileIds, CutoffProfileID: body.CutoffProfileId, TargetID: body.TargetId,
		TargetAuto: derefOr(body.TargetAuto, false), UpgradesAllowed: body.UpgradesAllowed,
	})
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.LibraryQualityPreview{
		Available: preview.Available, Items: preview.Items, UpgradableNow: preview.UpgradableNow,
		UpgradableAfter: preview.UpgradableAfter, BecomeUpgradable: preview.BecomeUpgradable, StopBeingUpgradable: preview.StopUpgradable,
	}
	if preview.Reason != "" {
		view.Reason = &preview.Reason
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}
