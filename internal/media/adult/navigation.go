package adult

import (
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
)

// Level keys, stable across releases: URLs and last-visited preferences reference them.
const (
	LevelScenes     = "scenes"
	LevelPerformers = "performers"
	LevelStudios    = "studios"
)

// Levels are the ADR-54 declaration, as Whisparr lays a library out: the scenes, their performers and their studios.
func Levels() []model.Level {
	return []model.Level{
		{Key: LevelScenes, Label: "Scenes", Entity: model.LevelEntityWork, DefaultSort: "title"},
		{Key: LevelPerformers, Label: "Performers", Entity: model.LevelEntityPerson, DefaultSort: "name", Role: RolePerformer},
		{Key: LevelStudios, Label: "Studios", Entity: model.LevelEntityOrganisation, DefaultSort: "name"},
	}
}

// facets are a scene's own fields beyond the base ones.
func facets() []media.Facet {
	return []media.Facet{
		{
			Key: "year", Label: "Year", Entity: model.LevelEntityWork, Kind: media.FacetNumber,
			Source: media.FacetSource{Kind: media.SourceColumn, Name: "year"}, Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: FieldStatus, Label: "Status", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
			Source:  media.FacetSource{Kind: media.SourceField, Name: FieldStatus},
			Choices: []media.Choice{{Key: string(StatusAnnounced), Label: "Announced"}, {Key: string(StatusReleased), Label: "Released"}},
			Column:  true, Sortable: true, Filterable: true,
		},
		{
			Key: FieldRelease, Label: "Release date", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceField, Name: FieldRelease}, Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: "studio", Label: "Studio", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceOrganisation, Name: "studio"}, Column: true, Sortable: true, Filterable: true,
		},
		{
			Key: "performer", Label: "Performers", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceCredit, Name: RolePerformer}, Column: true, Filterable: true,
		},
		{
			Key: "genre", Label: "Tags", Entity: model.LevelEntityWork, Kind: media.FacetList,
			Source: media.FacetSource{Kind: media.SourceGenreTree, Name: GenreTree.Key}, Sortable: true, Filterable: true,
		},
		{
			Key: "runtime", Label: "Length", Entity: model.LevelEntityWork, Kind: media.FacetDuration,
			Source: media.FacetSource{Kind: media.SourceColumn, Name: "runtime_ms"}, Column: true, Sortable: true,
		},
		{
			Key: FieldCode, Label: "Code", Entity: model.LevelEntityWork, Kind: media.FacetText,
			Source: media.FacetSource{Kind: media.SourceField, Name: FieldCode}, Filterable: true,
		},
	}
}

// Quality profile names a library of each kind is suggested.
var (
	sceneProfiles = []string{"Adult: 1080p preferred", "Adult: 720p to 1080p", "Adult: any quality"}
	filmProfiles  = []string{"Adult: 1080p preferred", "Adult: any quality"}
)

// kinds are what a library of the type is for: scenes (Stash's and Whisparr's unit) or whole films.
func kinds() []media.LibraryKind {
	return []media.LibraryKind{
		{Key: "scenes", Label: "Scenes", Help: "Single scenes, filed by studio.", QualityProfiles: sceneProfiles},
		{Key: "movies", Label: "Movies", Help: "Whole films, filed by studio.", QualityProfiles: filmProfiles},
	}
}
