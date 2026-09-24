package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// GetImportSettings answers ADR-48's own commit-line threshold, the same
// direct-settings.Cache shape GetNetworkSettings already uses -- import.
// auto_commit_confidence is a scalar policy value (notes.md §3), not
// behind its own adapter interface.
func (s *Server) GetImportSettings(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.importSettingsView())
}

// UpdateImportSettings changes the commit line. Administrator only, written
// wholesale like the network and cache settings (ADR-44).
func (s *Server) UpdateImportSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateImportSettingsJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected auto_commit_confidence")
		return
	}

	if err := s.auth.Registry.LookupAll(settings.KeyImportAutoCommitConfidence, settings.KeyImportTransferLadder,
		settings.KeyImportRenameOnImport, settings.KeyImportExtraFiles, settings.KeyImportExtraFileExtensions); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if err := s.auth.Settings.SetImportAutoCommitConfidence(r.Context(), clock.NewSystem(), float64(body.AutoCommitConfidence)); err != nil {
		s.writeImportSettingsError(w, err)
		return
	}
	// Absent means "say nothing about the ladder", so an older client that
	// only knows the commit line cannot silently reset the order.
	if body.TransferLadder != nil {
		ladder := make([]string, 0, len(*body.TransferLadder))
		for _, method := range *body.TransferLadder {
			ladder = append(ladder, string(method))
		}
		if err := s.auth.Settings.SetImportTransferLadder(r.Context(), clock.NewSystem(), ladder); err != nil {
			s.writeImportSettingsError(w, err)
			return
		}
	}
	// Absent means "say nothing about renaming", the same shape the ladder
	// above keeps.
	if body.RenameOnImport != nil {
		if err := s.auth.Settings.SetImportRenameOnImport(r.Context(), clock.NewSystem(), *body.RenameOnImport); err != nil {
			s.writeImportSettingsError(w, err)
			return
		}
	}
	if body.ExtraFiles != nil || body.ExtraFileExtensions != nil {
		extras := s.auth.Settings.Snapshot().ImportExtraFiles()
		if body.ExtraFiles != nil {
			extras.Enabled = *body.ExtraFiles
		}
		if body.ExtraFileExtensions != nil {
			extras.Extensions = *body.ExtraFileExtensions
		}
		if err := s.auth.Settings.SetImportExtraFiles(r.Context(), clock.NewSystem(), extras); err != nil {
			s.writeImportSettingsError(w, err)
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, s.importSettingsView())
}

// writeImportSettingsError reports ADR-44 validation problems as a 400 and
// anything else as itself.
func (s *Server) writeImportSettingsError(w http.ResponseWriter, err error) {
	var problems config.Problems
	if errors.As(err, &problems) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"The import settings have problems", problems.Report("the import settings", "Nothing was changed."))
		return
	}
	writeError(w, s.logger, err)
}

func (s *Server) importSettingsView() gen.ImportSettings {
	snapshot := s.auth.Settings.Snapshot()
	stored := snapshot.ImportTransferLadder()
	ladder := make([]gen.TransferMethod, 0, len(stored))
	for _, method := range stored {
		ladder = append(ladder, gen.TransferMethod(method))
	}
	rename := snapshot.ImportRenameOnImport()
	extras := snapshot.ImportExtraFiles()
	return gen.ImportSettings{
		AutoCommitConfidence: float32(snapshot.ImportAutoCommitConfidence()),
		TransferLadder:       &ladder,
		RenameOnImport:       &rename,
		ExtraFiles:           &extras.Enabled,
		ExtraFileExtensions:  &extras.Extensions,
	}
}
