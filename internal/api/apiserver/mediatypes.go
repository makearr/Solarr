package apiserver

import (
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

// MediaTerm is one word of a media type's vocabulary.
type MediaTerm struct {
	One  string
	Many string
}

// MediaRole is one way a person is credited in a media type.
type MediaRole struct {
	Key        string
	Label      string
	Lead       bool
	Followable bool
	PartScoped bool
	Character  bool
}

// MediaTypeInfo is one media type this build can manage.
type MediaTypeInfo struct {
	Key   string
	Label string
	// TaxonomyKey and TaxonomyLabel name the genre tree the type's tags are mapped onto.
	TaxonomyKey, TaxonomyLabel string
	Work                       MediaTerm
	Part                       MediaTerm
	Group                      MediaTerm
	LeadPeople                 MediaTerm
	Organisation               MediaTerm
	Roles                      []MediaRole
	// FollowOptions are what a follow offers in this type's libraries, as the generated shape carries them.
	FollowOptions []gen.FollowOption
	// Facets are the type's own fields, as the generated shape carries them.
	Facets []gen.Facet
	// MonitorOptions are the ways a whole work can be monitored at once.
	MonitorOptions []MediaChoice
	// SearchTerms are the words a person may search with instead of a work's own.
	SearchTerms []MediaChoice
	// WorkImage is the shape of a work's picture: "cover" or "poster".
	WorkImage string
	// WantedGrain is what Wanted lists: "work" or "part".
	WantedGrain string
	// AddKinds are what a library of the type adds, the first being its default.
	AddKinds []MediaChoice
	// CommitsHeldImports says a held import unit is committed by hand.
	CommitsHeldImports bool
	// DuplicatesByName says a duplicate is one recording under any work, matched by name and length.
	DuplicatesByName bool
	// ExtraFiles is the type's "Import Extra Files" for a library that has not chosen; both fields are set.
	ExtraFiles settings.ExtraFiles
	// GuessFloor is the least confidence "Commit anyway" takes a held unit's best guess at; 0 takes any.
	GuessFloor float64
	// Kinds are what a library of the type can be, the first being the default.
	Kinds []LibraryKind
	// FileSettings are the type's own File Management settings, in its words.
	FileSettings []FileSetting
	// ScanMonitor is what a scan monitors of a work it finds.
	ScanMonitor ScanMonitor
	// UnidentifiedRetry is the schedule that retries unidentified files; empty when the type has none.
	UnidentifiedRetry string
	SearchesLibrary   bool
	CoverSources      bool
	ReleaseFilters    bool
}

// LibraryKind is one kind of library of a media type and the defaults a library of it starts with.
type LibraryKind struct {
	Key, Label, Help string
	// QualityProfiles name the profiles the library accepts, best first; one not found is left out.
	QualityProfiles []string
	// Language is the language profile it starts with, found by name or created; nil for none.
	Language *settings.LanguageProfile
}

// kindOf answers the media type's kind named key, its first for "", and false for one it does not have; a type
// without kinds answers the zero kind for "".
func (info MediaTypeInfo) kindOf(key string) (LibraryKind, bool) {
	if len(info.Kinds) == 0 {
		return LibraryKind{}, key == ""
	}
	if key == "" {
		return info.Kinds[0], true
	}
	for _, kind := range info.Kinds {
		if kind.Key == key {
			return kind, true
		}
	}
	return LibraryKind{}, false
}

func kindViews(kinds []LibraryKind) *[]struct {
	Help  *string `json:"help,omitempty"`
	Key   string  `json:"key"`
	Label string  `json:"label"`
} {
	if len(kinds) == 0 {
		return nil
	}
	views := make([]struct {
		Help  *string `json:"help,omitempty"`
		Key   string  `json:"key"`
		Label string  `json:"label"`
	}, len(kinds))
	for i, kind := range kinds {
		views[i].Key, views[i].Label, views[i].Help = kind.Key, kind.Label, strPtr(kind.Help)
	}
	return &views
}

// MediaChoice is one key a media type offers, with its label.
type MediaChoice struct{ Key, Label string }

// FileSetting is one File Management setting as a media type words it.
type FileSetting struct {
	Key, Label, Help string
	Options          []MediaChoice
}

// ScanMonitor is the library setting a type's scan monitors by, with its values in the type's words.
type ScanMonitor struct {
	Setting, Label string
	Options        []MediaChoice
}

// MediaTypeCatalog answers which media types have a module, so a handler never names one.
type MediaTypeCatalog interface {
	List() []MediaTypeInfo
	Known(mediaType string) bool
}

// ListMediaTypes answers the media types a library can be created for.
func (s *Server) ListMediaTypes(w http.ResponseWriter, _ *http.Request) {
	out := []gen.MediaTypeInfo{}
	if s.mediaTypes != nil {
		for _, info := range s.mediaTypes.List() {
			roles := []gen.MediaRole{}
			for _, role := range info.Roles {
				roles = append(roles, gen.MediaRole{
					Key: role.Key, Label: role.Label, Lead: role.Lead, Followable: role.Followable,
					PartScoped: role.PartScoped, Character: role.Character,
				})
			}
			out = append(out, gen.MediaTypeInfo{
				Key: gen.MediaType(info.Key), Label: info.Label, Roles: roles,
				Taxonomy: gen.Taxonomy{Key: info.TaxonomyKey, Label: info.TaxonomyLabel},
				Work:     termView(info.Work), Part: termView(info.Part), Group: termView(info.Group),
				LeadPeople: termView(info.LeadPeople), Organisation: termView(info.Organisation),
				FollowOptions: nonNilOptions(info.FollowOptions), Facets: nonNilFacets(info.Facets),
				MonitorOptions: choiceViews(info.MonitorOptions), SearchTerms: choiceViews(info.SearchTerms),
				WorkImage: gen.MediaTypeInfoWorkImage(info.WorkImage), WantedGrain: gen.MediaTypeInfoWantedGrain(info.WantedGrain),
				AddKinds: choiceViews(info.AddKinds), CommitsHeldImports: info.CommitsHeldImports, CommitGuessFloor: &info.GuessFloor,
				DuplicatesByName: info.DuplicatesByName, Kinds: kindViews(info.Kinds),
				FileSettings: fileSettingViews(info.FileSettings), ScanMonitor: scanMonitorView(info.ScanMonitor),
				UnidentifiedRetry: strPtr(info.UnidentifiedRetry), SearchesLibrary: info.SearchesLibrary,
				CoverSources: info.CoverSources, ReleaseFilters: info.ReleaseFilters,
			})
		}
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

func choiceViews(options []MediaChoice) []struct {
	Key   string `json:"key"`
	Label string `json:"label"`
} {
	views := make([]struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}, len(options))
	for i, option := range options {
		views[i].Key, views[i].Label = option.Key, option.Label
	}
	return views
}

func fileSettingViews(settings []FileSetting) []struct {
	Help    string                           `json:"help"`
	Key     gen.MediaTypeInfoFileSettingsKey `json:"key"`
	Label   string                           `json:"label"`
	Options *[]gen.MediaChoice               `json:"options,omitempty"`
} {
	views := make([]struct {
		Help    string                           `json:"help"`
		Key     gen.MediaTypeInfoFileSettingsKey `json:"key"`
		Label   string                           `json:"label"`
		Options *[]gen.MediaChoice               `json:"options,omitempty"`
	}, len(settings))
	for i, setting := range settings {
		views[i].Key, views[i].Label, views[i].Help = gen.MediaTypeInfoFileSettingsKey(setting.Key), setting.Label, setting.Help
		if len(setting.Options) > 0 {
			options := mediaChoices(setting.Options)
			views[i].Options = &options
		}
	}
	return views
}

func scanMonitorView(monitor ScanMonitor) struct {
	Label   string                              `json:"label"`
	Options []gen.MediaChoice                   `json:"options"`
	Setting gen.MediaTypeInfoScanMonitorSetting `json:"setting"`
} {
	return struct {
		Label   string                              `json:"label"`
		Options []gen.MediaChoice                   `json:"options"`
		Setting gen.MediaTypeInfoScanMonitorSetting `json:"setting"`
	}{Label: monitor.Label, Options: mediaChoices(monitor.Options), Setting: gen.MediaTypeInfoScanMonitorSetting(monitor.Setting)}
}

func mediaChoices(options []MediaChoice) []gen.MediaChoice {
	views := make([]gen.MediaChoice, len(options))
	for i, option := range options {
		views[i] = gen.MediaChoice{Key: option.Key, Label: option.Label}
	}
	return views
}

func nonNilFacets(facets []gen.Facet) []gen.Facet {
	if facets == nil {
		return []gen.Facet{}
	}
	return facets
}

func nonNilOptions(options []gen.FollowOption) []gen.FollowOption {
	if options == nil {
		return []gen.FollowOption{}
	}
	return options
}

func termView(term MediaTerm) gen.MediaTerm { return gen.MediaTerm{One: term.One, Many: term.Many} }

// refuseUnknownMediaType answers 400 for a media type no module manages; true means it was refused.
func (s *Server) refuseUnknownMediaType(w http.ResponseWriter, mediaType string) bool {
	if s.mediaTypes == nil || s.mediaTypes.Known(mediaType) {
		return false
	}
	writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
		"The category has problems", "media_type "+mediaType+" is not available in this build. Nothing was created.")
	return true
}

// mediaTypeInfo answers one media type's description; false when no module brings it.
func (s *Server) mediaTypeInfo(key string) (MediaTypeInfo, bool) {
	if s.mediaTypes == nil {
		return MediaTypeInfo{}, false
	}
	for _, info := range s.mediaTypes.List() {
		if info.Key == key {
			return info, true
		}
	}
	return MediaTypeInfo{}, false
}
