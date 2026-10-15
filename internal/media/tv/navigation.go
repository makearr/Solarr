package tv

import "github.com/makearr/Solarr/internal/identify/model"

// Level keys, stable across releases: URLs and last-visited preferences reference them.
const (
	// LevelCrossovers lists the events across series and the episodes listed under two (T23).
	LevelCrossovers = "crossovers"
	LevelSeries     = "series"
	LevelPeople     = "people"
	LevelNetworks   = "networks"
)

// Navigation is TV's ADR-54 declaration. Seasons and episodes live inside the series page, as in Sonarr.
type Navigation struct{}

// Levels implements model.NavigationLevels.
func (Navigation) Levels() []model.Level {
	return []model.Level{
		{Key: LevelSeries, Label: "Series", Entity: model.LevelEntityWork, DefaultSort: "title"},
		{Key: LevelPeople, Label: "Cast", Entity: model.LevelEntityPerson, DefaultSort: "name"},
		{Key: LevelNetworks, Label: "Networks", Entity: model.LevelEntityOrganisation, DefaultSort: "name"},
		{Key: LevelCrossovers, Label: "Crossovers", Entity: model.LevelEntityCollection, DefaultSort: "name"},
	}
}
