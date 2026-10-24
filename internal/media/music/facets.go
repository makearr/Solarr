package music

import (
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
)

// facets are an album's own fields beyond the base ones.
var facets = []media.Facet{
	{
		Key: "year", Label: "Year", Entity: model.LevelEntityWork, Kind: media.FacetNumber,
		Source: media.FacetSource{Kind: media.SourceColumn, Name: "year"}, Column: true, Sortable: true, Filterable: true,
	},
	{
		Key: "primary_type", Label: "Type", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
		Source:  media.FacetSource{Kind: media.SourceColumn, Name: "subtype"},
		Choices: []media.Choice{{Key: "Album", Label: "Album"}, {Key: "EP", Label: "EP"}, {Key: "Single", Label: "Single"}, {Key: "Broadcast", Label: "Broadcast"}, {Key: "Other", Label: "Other"}},
		Column:  true, Sortable: true, Filterable: true,
	},
	{
		Key: "label", Label: "Label", Entity: model.LevelEntityWork, Kind: media.FacetText,
		Source: media.FacetSource{Kind: media.SourceOrganisation, Name: RoleLabel}, Column: true, Sortable: true, Filterable: true,
	},
	{
		Key: "genre", Label: "Genre", Entity: model.LevelEntityWork, Kind: media.FacetList,
		Source: media.FacetSource{Kind: media.SourceGenreTree, Name: GenreTree.Key}, Sortable: true, Filterable: true,
	},
}
