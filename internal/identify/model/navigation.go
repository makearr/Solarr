package model

// LevelEntity is the data shape a navigation level lists underneath (ADR-7,
// ADR-32): every level is a work, a work_part, a person or an organisation,
// never a second data model of its own.
type LevelEntity string

const (
	// LevelEntityWork means the level lists work rows -- an album, a movie,
	// a series.
	LevelEntityWork LevelEntity = "work"
	// LevelEntityWorkPart means the level lists work_part rows -- a track,
	// an episode, a chapter.
	LevelEntityWorkPart LevelEntity = "work_part"
	// LevelEntityPerson means the level lists person rows -- an artist, an
	// author, a performer.
	LevelEntityPerson LevelEntity = "person"
	// LevelEntityOrganisation means the level lists organisation rows -- a
	// publisher, a studio.
	LevelEntityOrganisation LevelEntity = "organisation"
	// LevelEntityCollection means the level lists collections of parts -- a crossover event across series.
	LevelEntityCollection LevelEntity = "collection"
	// LevelEntityFranchise means the level lists the franchises a provider groups works in -- a movie's collection.
	LevelEntityFranchise LevelEntity = "franchise"
)

// Level is one browsable entry (ADR-54): Key for URLs and APIs, Label for the sidebar; the vocabulary is the media type's, not core's.
type Level struct {
	Key         string
	Label       string
	Entity      LevelEntity
	DefaultSort string
	// Role opens a person level on one credit role, as Narrators beside Authors; empty opens it on the type's lead role.
	Role string
	// Subtypes keeps the level to works of these subtypes (Videos); ExceptSubtypes leaves works of these out (Albums), so the two levels never list the same work.
	Subtypes, ExceptSubtypes []string
}

// NavigationLevels is an ADR-23 extension point: a media type declares what it can be browsed by, and core never names an album or season (Invariant 85).
type NavigationLevels interface {
	Levels() []Level
}
