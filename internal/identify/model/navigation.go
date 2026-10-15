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
)

// Level is one browsable entry in a library's tree (ADR-54). Key is the
// stable identifier a URL and an API call use; Label is shown in the
// sidebar. Core renders Levels() without ever learning what an album or a
// season is -- the vocabulary is entirely the media type's.
type Level struct {
	Key         string
	Label       string
	Entity      LevelEntity
	DefaultSort string
}

// NavigationLevels is an ADR-23 extension point, alongside SetCoverage and
// AvailabilityWindow: a media type declares what it can be browsed by. Core
// renders the declaration and never names an album, a season or an issue
// (Invariant 85).
type NavigationLevels interface {
	Levels() []Level
}
