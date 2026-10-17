package music

import "github.com/makearr/Solarr/internal/identify/model"

// Level keys, stable across releases: a URL, a last-visited preference and an
// API call all reference these, so renaming one is a compatibility break.
const (
	LevelArtists = "artists"
	LevelAlbums  = "albums"
	LevelTracks  = "tracks"
	LevelLabels  = "labels"
	// LevelCollections lists the collections made by hand, such as one composer's works.
	LevelCollections = "collections"
)

// Navigation is music's ADR-54 NavigationLevels declaration: Artists, Albums,
// Tracks, Labels, in the order the sidebar shows them.
type Navigation struct{}

// Levels implements model.NavigationLevels.
func (Navigation) Levels() []model.Level {
	return []model.Level{
		{Key: LevelArtists, Label: "Artists", Entity: model.LevelEntityPerson, DefaultSort: "name"},
		{Key: LevelAlbums, Label: "Albums", Entity: model.LevelEntityWork, DefaultSort: "title"},
		{Key: LevelTracks, Label: "Tracks", Entity: model.LevelEntityWorkPart, DefaultSort: "artist"},
		{Key: LevelCollections, Label: "Collections", Entity: model.LevelEntityFranchise, DefaultSort: "name"},
		{Key: LevelLabels, Label: "Labels", Entity: model.LevelEntityOrganisation, DefaultSort: "name"},
	}
}
