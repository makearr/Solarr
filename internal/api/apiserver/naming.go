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
)

// NamingSchemes is each media type's naming declaration, which lives below apiserver
// (ADR-23); cmd/solarr/adapters bridges it.
type NamingSchemes interface {
	Scheme(mediaType string) (NamingScheme, bool)
	// Examples renders the scheme's samples under profile; problems say why a save would fail.
	Examples(mediaType string, profile settings.NamingProfile) (examples []NamingExample, problems []string)
}

// NamingScheme is what a media type declares about naming, in apiserver's plain shape.
type NamingScheme struct {
	Slots   []NamingSlot
	Tokens  []NamingToken
	Presets []NamingPreset
	// Default is the naming a library has before it is first saved.
	Default settings.NamingProfile
}

// NamingSlot is one template a media type names files with.
type NamingSlot struct{ Key, Label, Help string }

// NamingToken is one {token}, explained for the token builder.
type NamingToken struct{ Spelling, Group, Example, Help string }

// NamingPreset is a named set of templates.
type NamingPreset struct {
	Name    string
	Formats map[string]string
}

// NamingExample is one rendered sample path.
type NamingExample struct{ Label, Path string }

func (s *Server) routeGetLibraryNaming(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.GetLibraryNaming(w, r, id)
	}
}

func (s *Server) routeSetLibraryNaming(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.SetLibraryNaming(w, r, id)
	}
}

func (s *Server) routePreviewLibraryNaming(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.PreviewLibraryNaming(w, r, id)
	}
}

func (s *Server) routeRenameLibrary(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.categoryIDParam(w, r); ok {
		s.RenameLibrary(w, r, id)
	}
}

func (s *Server) categoryIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a library id must be a number")
		return 0, false
	}
	return id, true
}

// GetLibraryNaming answers a library's naming with its scheme and examples.
func (s *Server) GetLibraryNaming(w http.ResponseWriter, r *http.Request, id int64) {
	category, scheme, ok := s.namingContext(w, r, id)
	if !ok {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.libraryNamingView(category, scheme))
}

// SetLibraryNaming saves a library's naming; the media type's validator refuses what it
// cannot render, every problem at once.
func (s *Server) SetLibraryNaming(w http.ResponseWriter, r *http.Request, id int64) {
	category, scheme, ok := s.namingContext(w, r, id)
	if !ok {
		return
	}
	profile, ok := s.decodeNaming(w, r)
	if !ok {
		return
	}
	if err := s.auth.Settings.SetCategoryNaming(r.Context(), id, profile); err != nil {
		if errors.Is(err, settings.ErrInvalidNaming) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The naming has problems", err.Error())
			return
		}
		writeError(w, s.logger, err)
		return
	}
	// The request's Access is frozen at its start (ADR-27), so the answer is what was saved.
	category.Naming = &profile
	writeJSON(w, s.logger, http.StatusOK, s.libraryNamingView(category, scheme))
}

// PreviewLibraryNaming renders examples for naming that is being edited and not yet saved.
func (s *Server) PreviewLibraryNaming(w http.ResponseWriter, r *http.Request, id int64) {
	category, _, ok := s.namingContext(w, r, id)
	if !ok {
		return
	}
	profile, ok := s.decodeNaming(w, r)
	if !ok {
		return
	}
	examples, problems := s.namingSchemes.Examples(category.MediaType, profile)
	writeJSON(w, s.logger, http.StatusOK, gen.NamingPreview{
		Examples: namingExamplesView(examples), Problems: nonNilStrings(problems),
	})
}

// RenameLibrary is Lidarr's "apply to library": a rename on every root the library uses.
func (s *Server) RenameLibrary(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.renameStarter == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "renaming is not available")
		return
	}
	if _, err := s.libraryFor(r, id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	var body gen.RenameLibraryJSONRequestBody
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDrainBody)).Decode(&body); err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "the request body could not be read")
			return
		}
	}
	var workID int64
	if body.WorkId != nil {
		workID = *body.WorkId
	}
	renames, err := s.renameStarter.StartLibraryRename(r.Context(), id, workID, body.Preview != nil && *body.Preview)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.LibraryRenamePlan{Roots: make([]gen.RootRenamePlan, len(renames))}
	for i, rename := range renames {
		view.Roots[i] = gen.RootRenamePlan{RootFolderId: rename.RootFolderID, Plan: drainPlanView(rename.Plan)}
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// namingContext is the checks every naming endpoint shares: administrator, a known library,
// and a media type that declares a naming scheme.
func (s *Server) namingContext(
	w http.ResponseWriter, r *http.Request, id int64,
) (settings.Category, NamingScheme, bool) {
	if !s.requireAdministrator(w, r) {
		return settings.Category{}, NamingScheme{}, false
	}
	if s.namingSchemes == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "naming is not available")
		return settings.Category{}, NamingScheme{}, false
	}
	category, err := s.libraryFor(r, id)
	if err != nil {
		writeError(w, s.logger, err)
		return settings.Category{}, NamingScheme{}, false
	}
	scheme, found := s.namingSchemes.Scheme(category.MediaType)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound,
			"Not found", category.MediaType+" has no naming scheme yet")
		return settings.Category{}, NamingScheme{}, false
	}
	return category, scheme, true
}

// libraryFor is the library as the caller's Access sees it: hidden answers not found (ADR-27).
func (s *Server) libraryFor(r *http.Request, id int64) (settings.Category, error) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		return settings.Category{}, err
	}
	return access.Category(id, settings.PermissionManage)
}

func (s *Server) decodeNaming(w http.ResponseWriter, r *http.Request) (settings.NamingProfile, bool) {
	var body gen.NamingSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return settings.NamingProfile{}, false
	}
	return settings.NamingProfile{
		RenameFiles: body.RenameFiles, ReplaceIllegalCharacters: body.ReplaceIllegalCharacters,
		ColonReplacement: string(body.ColonReplacement), Formats: body.Formats,
	}, true
}

func (s *Server) libraryNamingView(category settings.Category, scheme NamingScheme) gen.LibraryNaming {
	profile := scheme.Default
	if category.Naming != nil {
		profile = *category.Naming
	}
	examples, _ := s.namingSchemes.Examples(category.MediaType, profile)
	view := gen.LibraryNaming{
		Naming:    namingSettingsView(profile),
		IsDefault: category.Naming == nil,
		Examples:  namingExamplesView(examples),
		Scheme: gen.NamingScheme{
			Slots:   make([]gen.NamingSlot, len(scheme.Slots)),
			Tokens:  make([]gen.NamingToken, len(scheme.Tokens)),
			Presets: make([]gen.NamingPreset, len(scheme.Presets)),
		},
	}
	for i, slot := range scheme.Slots {
		view.Scheme.Slots[i] = gen.NamingSlot{Key: slot.Key, Label: slot.Label, Help: slot.Help}
	}
	for i, token := range scheme.Tokens {
		view.Scheme.Tokens[i] = gen.NamingToken{
			Spelling: token.Spelling, Group: token.Group, Example: token.Example, Help: token.Help,
		}
	}
	for i, preset := range scheme.Presets {
		view.Scheme.Presets[i] = gen.NamingPreset{Name: preset.Name, Formats: preset.Formats}
	}
	return view
}

func namingSettingsView(profile settings.NamingProfile) gen.NamingSettings {
	return gen.NamingSettings{
		RenameFiles:              profile.RenameFiles,
		ReplaceIllegalCharacters: profile.ReplaceIllegalCharacters,
		ColonReplacement:         gen.NamingSettingsColonReplacement(profile.ColonReplacement),
		Formats:                  profile.Formats,
	}
}

func namingExamplesView(examples []NamingExample) []gen.NamingExample {
	view := make([]gen.NamingExample, len(examples))
	for i, example := range examples {
		view[i] = gen.NamingExample{Label: example.Label, Path: example.Path}
	}
	return view
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
