package apiserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// DiscoverRefresher starts the refresh of a library's recommendations, or of every enabled library with 0, and drops a deleted library's.
type DiscoverRefresher interface {
	RefreshDiscover(ctx context.Context, categoryID int64) error
	ForgetDiscover(ctx context.Context, categoryID int64) error
	// MoreDiscover reads the next provider page of a library's lists (all of them with an empty listType) into the pool.
	MoreDiscover(ctx context.Context, categoryID int64, listType string) (added int, exhausted bool, err error)
}

// DiscoverAutoAddPreviewer answers what auto-add would do with a library's unsaved settings, without doing it.
type DiscoverAutoAddPreviewer interface {
	PreviewDiscoverAutoAdd(ctx context.Context, one settings.LibraryDiscover) ([]DiscoverPreviewItem, error)
}

// DiscoverPreviewItem is one pool item with auto-add's verdict.
type DiscoverPreviewItem struct {
	Title      string
	Year       int64
	Type       string
	Rating     float64
	Votes      int64
	WouldAdd   bool
	Rejections []DiscoverPreviewRejection
}

// DiscoverPreviewRejection is one reason auto-add holds an item back, with the value that caused it.
type DiscoverPreviewRejection struct{ Reason, Value string }

func (s *Server) discoverGlobal() gen.DiscoverGlobalSettings {
	global := s.auth.Settings.Snapshot().DiscoverGlobal()
	return gen.DiscoverGlobalSettings{
		Enabled: global.Enabled, ExcludeGenres: nonNil(global.ExcludeGenres), ExcludeNetworks: nonNil(global.ExcludeNetworks),
		ClassicAgeYears: int(global.ClassicAgeYears), MinRating: global.MinRating, MinVotes: global.MinVotes, AutoAddPerDay: int(global.AutoAddPerDay), AutoAddSearch: global.AutoAddSearch,
	}
}

func (s *Server) discoverLibrary(one settings.LibraryDiscover) gen.LibraryDiscoverSettings {
	out := gen.LibraryDiscoverSettings{
		CategoryId: one.CategoryID, Enabled: one.Enabled, TotalAmount: int(one.TotalAmount), OriginalLanguages: nonNil(one.OriginalLanguages),
		LanguagesSet: one.LanguagesSet, ExcludeGenres: nonNil(one.ExcludeGenres), ExcludeNetworks: nonNil(one.ExcludeNetworks), DisabledKinds: nonNil(one.DisabledKinds),
	}
	name, mediaType := one.Name, one.MediaType
	out.Name, out.MediaType = &name, &mediaType
	for _, setting := range one.Types {
		out.Types = append(out.Types, gen.DiscoverTypeSettings{Type: gen.DiscoverTypeSettingsType(setting.Type), Enabled: setting.Enabled, Amount: int(setting.Amount), AutoAdd: setting.AutoAdd})
	}
	return out
}

func (s *Server) discoverSettings(ctx context.Context) (gen.DiscoverSettings, error) {
	libraries, err := s.auth.Settings.LibraryDiscovers(ctx)
	if err != nil {
		return gen.DiscoverSettings{}, err
	}
	out := gen.DiscoverSettings{Global: s.discoverGlobal(), Libraries: []gen.LibraryDiscoverSettings{}}
	for _, one := range libraries {
		out.Libraries = append(out.Libraries, s.discoverLibrary(one))
	}
	return out, nil
}

// GetDiscoverSettings answers Discover's switch, global exclusions and every library's settings. Administrator only.
func (s *Server) GetDiscoverSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	body, err := s.discoverSettings(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

// SetDiscoverSettings saves the switch and the global exclusions. Administrator only.
func (s *Server) SetDiscoverSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.DiscoverGlobalSettings
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a JSON object") {
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyDiscoverEnabled, settings.KeyDiscoverExcludeGenres, settings.KeyDiscoverExcludeNetworks,
		settings.KeyDiscoverClassicYears, settings.KeyDiscoverMinRating, settings.KeyDiscoverMinVotes, settings.KeyDiscoverAutoAddPerDay, settings.KeyDiscoverAutoAddSearch); err != nil {
		writeError(w, s.logger, err)
		return
	}
	global := settings.DiscoverGlobal{
		Enabled: body.Enabled, ExcludeGenres: body.ExcludeGenres, ExcludeNetworks: body.ExcludeNetworks,
		ClassicAgeYears: int64(body.ClassicAgeYears), MinRating: body.MinRating, MinVotes: body.MinVotes, AutoAddPerDay: int64(body.AutoAddPerDay), AutoAddSearch: body.AutoAddSearch,
	}
	if err := s.auth.Settings.SetDiscoverGlobal(r.Context(), s.auth.Clock, global); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the Discover settings", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	if global.Enabled && s.discoverRefresher != nil {
		if err := s.discoverRefresher.RefreshDiscover(r.Context(), 0); err != nil {
			s.logger.Warn("discover refresh not queued", "error", err)
		}
	}
	out, err := s.discoverSettings(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// SetLibraryDiscoverSettings saves one library's Discover settings and refreshes its recommendations. Administrator only.
func (s *Server) SetLibraryDiscoverSettings(w http.ResponseWriter, r *http.Request, categoryID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.LibraryDiscoverSettings
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a JSON object") {
		return
	}
	one := libraryDiscoverOf(categoryID, body, s.auth.Settings.Snapshot().DiscoverGlobal())
	err := s.auth.Settings.SetLibraryDiscover(r.Context(), one)
	var problems config.Problems
	if errors.As(err, &problems) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the Discover settings", "Nothing was changed."))
		return
	}
	if errors.Is(err, settings.ErrNotFound) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such library")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if one.Enabled && s.discoverRefresher != nil {
		if err := s.discoverRefresher.RefreshDiscover(r.Context(), categoryID); err != nil {
			s.logger.Warn("discover refresh not queued", "error", err)
		}
	}
	saved, err := s.auth.Settings.LibraryDiscover(r.Context(), categoryID)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.discoverLibrary(saved))
}

// libraryDiscoverOf reads a library body; classic age and the auto-add gates are the instance-wide ones.
func libraryDiscoverOf(categoryID int64, body gen.LibraryDiscoverSettings, global settings.DiscoverGlobal) settings.LibraryDiscover {
	one := settings.LibraryDiscover{
		CategoryID: categoryID, Enabled: body.Enabled, TotalAmount: int64(body.TotalAmount), ClassicAgeYears: global.ClassicAgeYears, MinRating: global.MinRating,
		MinVotes: global.MinVotes, AutoAddPerDay: global.AutoAddPerDay, AutoAddSearch: global.AutoAddSearch, OriginalLanguages: body.OriginalLanguages,
		LanguagesSet: body.LanguagesSet, ExcludeGenres: body.ExcludeGenres, ExcludeNetworks: body.ExcludeNetworks, DisabledKinds: body.DisabledKinds,
	}
	for _, setting := range body.Types {
		one.Types = append(one.Types, settings.DiscoverTypeSetting{Type: string(setting.Type), Enabled: setting.Enabled, Amount: int64(setting.Amount), AutoAdd: setting.AutoAdd})
	}
	return one
}

// PreviewLibraryDiscoverAutoAdd answers what auto-add would add now with the unsaved settings; nothing is saved or added. Administrator only.
func (s *Server) PreviewLibraryDiscoverAutoAdd(w http.ResponseWriter, r *http.Request, categoryID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.LibraryDiscoverSettings
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a JSON object") {
		return
	}
	if _, err := s.auth.Settings.LibraryDiscover(r.Context(), categoryID); errors.Is(err, settings.ErrNotFound) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such library")
		return
	} else if err != nil {
		writeError(w, s.logger, err)
		return
	}
	one := libraryDiscoverOf(categoryID, body, s.auth.Settings.Snapshot().DiscoverGlobal())
	if problems := settings.ValidateLibraryDiscover(one); len(problems) > 0 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the Discover settings", "Nothing was changed."))
		return
	}
	if s.discoverPreviewer == nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "auto-add preview is not available")
		return
	}
	items, err := s.discoverPreviewer.PreviewDiscoverAutoAdd(r.Context(), one)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := gen.DiscoverAutoAddPreview{Items: []gen.DiscoverAutoAddPreviewItem{}}
	for _, item := range items {
		entry := gen.DiscoverAutoAddPreviewItem{Title: item.Title, Type: item.Type, Rating: item.Rating, Votes: item.Votes, WouldAdd: item.WouldAdd, Rejections: []gen.DiscoverRejection{}}
		if item.Year > 0 {
			year := int(item.Year)
			entry.Year = &year
		}
		for _, rejection := range item.Rejections {
			one := gen.DiscoverRejection{Reason: rejection.Reason}
			if rejection.Value != "" {
				value := rejection.Value
				one.Value = &value
			}
			entry.Rejections = append(entry.Rejections, one)
		}
		if item.WouldAdd {
			out.WouldAdd++
		} else {
			out.HeldBack++
		}
		out.Items = append(out.Items, entry)
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}
