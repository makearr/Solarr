package tv

import (
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/screen"
)

// facets are a series' own fields beyond the base ones.
var facets = []media.Facet{
	{
		Key: "year", Label: "Year", Entity: model.LevelEntityWork, Kind: media.FacetNumber,
		Source: media.FacetSource{Kind: media.SourceColumn, Name: "year"}, Column: true, Sortable: true, Filterable: true,
	},
	{
		Key: "status", Label: "Status", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
		Source:  media.FacetSource{Kind: media.SourceField, Name: "status"},
		Choices: []media.Choice{{Key: string(StatusContinuing), Label: "Continuing"}, {Key: string(StatusEnded), Label: "Ended"}, {Key: string(StatusUpcoming), Label: "Upcoming"}},
		Column:  true, Sortable: true, Filterable: true,
	},
	{
		Key: "series_type", Label: "Series type", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
		Source:  media.FacetSource{Kind: media.SourceField, Name: "series_type"},
		Choices: []media.Choice{{Key: string(SeriesStandard), Label: "Standard"}, {Key: string(SeriesDaily), Label: "Daily"}, {Key: string(SeriesAnime), Label: "Anime"}},
		Column:  true, Filterable: true, Editable: true,
	},
	{
		Key: "air_time_zone", Label: "Air time zone", Entity: model.LevelEntityWork, Kind: media.FacetEnum,
		Source:  media.FacetSource{Kind: media.SourceField, Name: FieldAirTimeZone},
		Choices: zoneChoices(), Editable: true, RefreshesWork: true,
	},
	{
		Key: "network", Label: "Network", Entity: model.LevelEntityWork, Kind: media.FacetText,
		Source: media.FacetSource{Kind: media.SourceOrganisation, Name: "network"}, Column: true, Sortable: true, Filterable: true,
	},
	{
		Key: "genre", Label: "Genre", Entity: model.LevelEntityWork, Kind: media.FacetList,
		Source: media.FacetSource{Kind: media.SourceGenreTree, Name: screen.GenreTree.Key}, Sortable: true, Filterable: true,
	},
	{
		Key: "runtime", Label: "Runtime", Entity: model.LevelEntityWork, Kind: media.FacetDuration,
		Source: media.FacetSource{Kind: media.SourceColumn, Name: "runtime_ms"}, Column: true,
	},
}
