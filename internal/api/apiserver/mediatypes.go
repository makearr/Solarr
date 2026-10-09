package apiserver

import (
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
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
	Key          string
	Label        string
	Work         MediaTerm
	Part         MediaTerm
	Group        MediaTerm
	LeadPeople   MediaTerm
	Organisation MediaTerm
	Roles        []MediaRole
	// FollowOptions are what a follow offers in this type's libraries, as the generated shape carries them.
	FollowOptions []gen.FollowOption
	// Facets are the type's own fields, as the generated shape carries them.
	Facets []gen.Facet
	// MonitorOptions are the ways a whole work can be monitored at once.
	MonitorOptions []MediaChoice
	// SearchTerms are the words a person may search with instead of a work's own.
	SearchTerms []MediaChoice
}

// MediaChoice is one key a media type offers, with its label.
type MediaChoice struct{ Key, Label string }

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
				Work: termView(info.Work), Part: termView(info.Part), Group: termView(info.Group),
				LeadPeople: termView(info.LeadPeople), Organisation: termView(info.Organisation),
				FollowOptions: nonNilOptions(info.FollowOptions), Facets: nonNilFacets(info.Facets),
				MonitorOptions: choiceViews(info.MonitorOptions), SearchTerms: choiceViews(info.SearchTerms),
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
