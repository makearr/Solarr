package book

import (
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
)

// Level keys, stable across releases: URLs and last-visited preferences reference them.
const (
	LevelBooks      = "books"
	LevelAuthors    = "authors"
	LevelSeries     = "series"
	LevelNarrators  = "narrators"
	LevelPublishers = "publishers"
)

// Levels are the format's ADR-54 declaration, as Readarr lays a library out: the books, their authors and series.
func (f Format) Levels() []model.Level {
	levels := []model.Level{
		{Key: LevelBooks, Label: f.Many, Entity: model.LevelEntityWork, DefaultSort: "title"},
		{Key: LevelAuthors, Label: "Authors", Entity: model.LevelEntityPerson, DefaultSort: "name"},
		{Key: LevelSeries, Label: "Series", Entity: model.LevelEntityFranchise, DefaultSort: "name"},
		{Key: LevelPublishers, Label: "Publishers", Entity: model.LevelEntityOrganisation, DefaultSort: "name"},
	}
	if f.ByFolder {
		narrators := model.Level{Key: LevelNarrators, Label: "Narrators", Entity: model.LevelEntityPerson, DefaultSort: "name", Role: RoleNarrator}
		levels = slices.Insert(levels, 2, narrators)
	}
	return levels
}

// facets are a book's own fields beyond the base ones; an audiobook's runtime is its length.
func (f Format) facets() []media.Facet {
	facets := []media.Facet{
		{
			Key: "year", Label: "Year", Entity: model.LevelEntityWork, Kind: media.FacetNumber,
			Source: media.FacetSource{Kind: media.SourceColumn, Name: "year"}, Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: FieldStatus, Label: "Status", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
			Source:  media.FacetSource{Kind: media.SourceField, Name: FieldStatus},
			Choices: []media.Choice{{Key: string(StatusAnnounced), Label: "Announced"}, {Key: string(StatusPublished), Label: "Published"}},
			Column:  true, Sortable: true, Filterable: true,
		},
		{
			Key: FieldSeries, Label: "Series", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceField, Name: FieldSeries}, Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: FieldSeriesPosition, Label: "Position in series", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceField, Name: FieldSeriesPosition}, Column: true,
		},
		{
			Key: "publisher", Label: "Publisher", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceOrganisation, Name: "publisher"}, Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: "genre", Label: "Genre", Entity: model.LevelEntityWork, Kind: media.FacetList,
			Source: media.FacetSource{Kind: media.SourceGenreTree, Name: GenreTree.Key}, Sortable: true, Filterable: true,
		},
		{
			Key: "original_language", Label: "Language", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceColumn, Name: "original_language"}, Filterable: true,
		},
	}
	if f.ByFolder {
		facets = append(facets, media.Facet{
			Key: "narrator", Label: "Narrator", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceCredit, Name: RoleNarrator}, Column: true, Sortable: true, Filterable: true,
		})
		return append(facets, media.Facet{
			Key: "runtime", Label: "Length", Entity: model.LevelEntityWork, Kind: media.FacetDuration,
			Source: media.FacetSource{Kind: media.SourceColumn, Name: "runtime_ms"}, Column: true, Sortable: true,
		})
	}
	return append(facets, media.Facet{
		Key: FieldPages, Label: "Pages", Entity: model.LevelEntityWork, Kind: media.FacetText,
		Source: media.FacetSource{Kind: media.SourceField, Name: FieldPages}, Column: true,
	})
}

// kinds are what a library of the format is for, so a new book is suggested to the right one (plan 10-libraries L5).
func (f Format) kinds() []media.LibraryKind {
	profiles := []string{"Ebook: EPUB preferred", "Ebook: any format"}
	if f.ByFolder {
		profiles = []string{"Audiobook: M4B preferred", "Audiobook: any format"}
	}
	return []media.LibraryKind{
		{Key: "general", Label: f.Many, Help: "Fiction and non-fiction for adults.", QualityProfiles: profiles},
		{
			Key: "kids", Label: "Children's", Help: "Children's and young adult " + lower(f.Many) + ".", QualityProfiles: profiles,
			Rules: kidsRules,
		},
	}
}

// kidsRules suggest the children's library for a book its providers file under a genre of the tree's for the young.
var kidsRules = []settings.LibraryRule{
	{Conditions: []settings.RuleCondition{{Field: settings.RuleGenre, Value: "Children's"}}},
	{Conditions: []settings.RuleCondition{{Field: settings.RuleGenre, Value: "Young Adult"}}},
}

// lower is a word as it reads mid-sentence: "Books" in "children's books".
func lower(word string) string {
	if word == "" {
		return word
	}
	return strings.ToLower(word[:1]) + word[1:]
}
