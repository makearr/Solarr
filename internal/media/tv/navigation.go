package tv

import "github.com/makearr/Solarr/internal/identify/model"

// Level keys, stable across releases: URLs and last-visited preferences reference them.
const (
	LevelSeries = "series"
)

// Navigation is TV's ADR-54 declaration. Seasons and episodes live inside the series page, as in Sonarr; Wanted and
// People are added when their screens exist.
type Navigation struct{}

// Levels implements model.NavigationLevels.
func (Navigation) Levels() []model.Level {
	return []model.Level{
		{Key: LevelSeries, Label: "Series", Entity: model.LevelEntityWork, DefaultSort: "title"},
	}
}
