package movie

import "github.com/makearr/Solarr/internal/identify/model"

// Level keys, stable across releases: URLs and last-visited preferences reference them.
const (
	LevelMovies      = "movies"
	LevelCollections = "collections"
	LevelPeople      = "people"
	LevelStudios     = "studios"
)

// Navigation is the movie module's ADR-54 declaration, as Radarr lays its library out.
type Navigation struct{}

// Levels implements model.NavigationLevels.
func (Navigation) Levels() []model.Level {
	return []model.Level{
		{Key: LevelMovies, Label: "Movies", Entity: model.LevelEntityWork, DefaultSort: "title"},
		{Key: LevelCollections, Label: "Collections", Entity: model.LevelEntityCollection, DefaultSort: "name"},
		{Key: LevelPeople, Label: "People", Entity: model.LevelEntityPerson, DefaultSort: "name"},
		{Key: LevelStudios, Label: "Studios", Entity: model.LevelEntityOrganisation, DefaultSort: "name"},
	}
}
