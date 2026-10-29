// Package importlist reads the lists a library adds works from (ADR-38): AniList and TMDB lists, previewed before they
// are enabled and synced on their own cadence through each media type's add. documentation/import-lists.md.
package importlist

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/config"
)

// FieldKind is how a list type's setting is entered.
type FieldKind string

// The kinds of setting a list type asks for.
const (
	FieldText FieldKind = "text"
	// FieldChoice takes one of Choices; FieldChoices any number, stored comma-separated.
	FieldChoice  FieldKind = "choice"
	FieldChoices FieldKind = "choices"
	FieldYear    FieldKind = "year"
)

// Field is one setting a list type asks for, rendered by the web interface as declared.
type Field struct {
	Key, Label, Help string
	Kind             FieldKind
	Choices          []media.Choice
	Required         bool
	Default          string
}

// Options are a list's settings by field key, as stored.
type Options map[string]string

// Source reads the entries of one list type for a list's options.
type Source interface {
	Entries(ctx context.Context, options Options) ([]media.ListEntry, error)
}

// Kind is one list type: what it is called, what it asks for and where it reads from.
type Kind struct {
	Type        media.ListType
	Label, Help string
	Fields      []Field
	Source      Source
}

// Catalogue is every list type this build reads.
type Catalogue struct{ kinds []Kind }

// NewCatalogue declares the list types over their providers' clients.
func NewCatalogue(aniList AniListLists, tmdb TMDBLists, now func() Season) Catalogue {
	return Catalogue{kinds: []Kind{
		{Type: media.ListAniListSeason, Label: "AniList season", Help: "The anime of one season, most popular first.",
			Fields: seasonFields, Source: AniListSeason{Client: aniList, Now: now}},
		{Type: media.ListAniListUser, Label: "AniList user list", Help: "What an AniList user has on their list, by status.",
			Fields: userFields, Source: AniListUser{Client: aniList}},
		{Type: media.ListTMDBList, Label: "TMDB list", Help: "A public TMDB list; its films are left out of a TV library.",
			Fields: tmdbListFields, Source: TMDBList{Client: tmdb}},
		{Type: media.ListTMDBTrending, Label: "TMDB trending shows", Help: "The shows trending on TMDB.",
			Fields: trendingFields, Source: TMDBTrending{Client: tmdb}},
		{Type: media.ListTMDBPopular, Label: "TMDB popular shows", Help: "The most popular shows on TMDB.",
			Source: TMDBPopular{Client: tmdb}},
	}}
}

// Kind answers one list type.
func (c Catalogue) Kind(listType media.ListType) (Kind, bool) {
	index := slices.IndexFunc(c.kinds, func(kind Kind) bool { return kind.Type == listType })
	if index < 0 {
		return Kind{}, false
	}
	return c.kinds[index], true
}

// Kinds answers the list types named, in the catalogue's order.
func (c Catalogue) Kinds(types []media.ListType) []Kind {
	var kinds []Kind
	for _, kind := range c.kinds {
		if slices.Contains(types, kind.Type) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// Validate checks a list's type is one the library takes and its options are what the type asks for.
func (c Catalogue) Validate(accepted []media.ListType, listType media.ListType, options Options) config.Problems {
	kind, known := c.Kind(listType)
	if !known || !slices.Contains(accepted, listType) {
		return config.Problems{{Path: "type", Value: string(listType), Expectation: "a list type this library takes"}}
	}
	var problems config.Problems
	for key := range options {
		if !slices.ContainsFunc(kind.Fields, func(field Field) bool { return field.Key == key }) {
			problems = append(problems, config.Problem{Path: "options." + key, Expectation: "a setting of " + kind.Label})
		}
	}
	for _, field := range kind.Fields {
		if problem, bad := field.check(options[field.Key]); bad {
			problems = append(problems, problem)
		}
	}
	slices.SortFunc(problems, func(a, b config.Problem) int { return strings.Compare(a.Path, b.Path) })
	return problems
}

func (f Field) check(value string) (config.Problem, bool) {
	path := "options." + f.Key
	value = strings.TrimSpace(value)
	if value == "" {
		return config.Problem{Path: path, Expectation: "not empty"}, f.Required
	}
	switch f.Kind {
	case FieldChoice, FieldChoices:
		for _, picked := range strings.Split(value, ",") {
			if !slices.ContainsFunc(f.Choices, func(choice media.Choice) bool { return choice.Key == strings.TrimSpace(picked) }) {
				return config.Problem{Path: path, Value: picked, Expectation: "one of the choices offered"}, true
			}
		}
	case FieldYear:
		var year int
		if _, err := fmt.Sscan(value, &year); err != nil || year < 1900 || year > 2200 {
			return config.Problem{Path: path, Value: value, Expectation: "a year"}, true
		}
	case FieldText:
		if f.Key == tmdbListField {
			if _, ok := TMDBListID(value); !ok {
				return config.Problem{Path: path, Value: value, Expectation: "a TMDB list number or its themoviedb.org link"}, true
			}
		}
	}
	return config.Problem{}, false
}

// option answers a setting, its field's default when unset.
func option(options Options, fields []Field, key string) string {
	if value := strings.TrimSpace(options[key]); value != "" {
		return value
	}
	for _, field := range fields {
		if field.Key == key {
			return field.Default
		}
	}
	return ""
}

// DecodeOptions reads stored options; an empty column is none.
func DecodeOptions(stored string) (Options, error) {
	options := Options{}
	if strings.TrimSpace(stored) == "" {
		return options, nil
	}
	if err := json.Unmarshal([]byte(stored), &options); err != nil {
		return nil, fmt.Errorf("importlist: options: %w", err)
	}
	return options, nil
}

// EncodeOptions stores options with blank values dropped, keys sorted, so equal settings store alike.
func EncodeOptions(options Options) string {
	kept := Options{}
	for key, value := range options {
		if value = strings.TrimSpace(value); value != "" {
			kept[key] = value
		}
	}
	if len(kept) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(kept) // a map of strings always encodes
	return string(encoded)
}
