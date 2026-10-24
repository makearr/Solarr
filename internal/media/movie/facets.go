package movie

import (
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/screen"
)

// facets are a movie's own fields beyond the base ones.
var facets = []media.Facet{
	{
		Key: "year", Label: "Year", Entity: model.LevelEntityWork, Kind: media.FacetNumber,
		Source: media.FacetSource{Kind: media.SourceColumn, Name: "year"}, Column: true, Sortable: true, Filterable: true,
	},
	{
		Key: "status", Label: "Status", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
		Source:  media.FacetSource{Kind: media.SourceField, Name: "status"},
		Choices: []media.Choice{{Key: string(StatusAnnounced), Label: "Announced"}, {Key: string(StatusInCinemas), Label: "In cinemas"}, {Key: string(StatusReleased), Label: "Released"}},
		Column:  true, Sortable: true, Filterable: true,
	},
	{
		Key: FieldMinimumAvailability, Label: "Minimum availability", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
		Source:  media.FacetSource{Kind: media.SourceField, Name: FieldMinimumAvailability},
		Choices: availabilityChoices, Editable: true, RefreshesWork: true,
	},
	{
		Key: "studio", Label: "Studio", Entity: model.LevelEntityWork, Kind: media.FacetText,
		Source: media.FacetSource{Kind: media.SourceOrganisation, Name: "studio"}, Column: true, Sortable: true, Filterable: true,
	},
	{
		Key: "genre", Label: "Genre", Entity: model.LevelEntityWork, Kind: media.FacetList,
		Source: media.FacetSource{Kind: media.SourceGenreTree, Name: screen.GenreTree.Key}, Sortable: true, Filterable: true,
	},
	{
		Key: "runtime", Label: "Runtime", Entity: model.LevelEntityWork, Kind: media.FacetDuration,
		Source: media.FacetSource{Kind: media.SourceColumn, Name: "runtime_ms"}, Column: true, Sortable: true,
	},
	{
		Key: "original_language", Label: "Original language", Entity: model.LevelEntityWork, Kind: media.FacetText,
		Source: media.FacetSource{Kind: media.SourceColumn, Name: "original_language"}, Filterable: true,
	},
}

var availabilityChoices = []media.Choice{
	{Key: string(AvailableAnnounced), Label: "Announced"},
	{Key: string(AvailableInCinemas), Label: "In cinemas"},
	{Key: string(AvailableReleased), Label: "Released"},
}
