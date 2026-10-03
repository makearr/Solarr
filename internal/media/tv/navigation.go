package tv

import "github.com/makearr/Solarr/internal/identify/model"

// Level keys, stable across releases: URLs and last-visited preferences reference them.
const (
	LevelSeries   = "series"
	LevelPeople   = "people"
	LevelNetworks = "networks"
	LevelPass     = "seasonpass"
)

// Navigation is TV's ADR-54 declaration. Seasons and episodes live inside the series page, as in Sonarr.
type Navigation struct{}

// Levels implements model.NavigationLevels.
func (Navigation) Levels() []model.Level {
	return []model.Level{
		{Key: LevelSeries, Label: "Series", Entity: model.LevelEntityWork, DefaultSort: "title"},
		{Key: LevelPeople, Label: "People", Entity: model.LevelEntityPerson, DefaultSort: "name"},
		{Key: LevelNetworks, Label: "Networks", Entity: model.LevelEntityOrganisation, DefaultSort: "name"},
		{Key: LevelPass, Label: "Season pass", Entity: model.LevelEntityWork, DefaultSort: "title"},
	}
}
