package tv

import "github.com/makearr/Solarr/internal/media/knownfor"

type (
	// PersonSource is the TMDB client as a person's page needs it.
	PersonSource = knownfor.PersonSource
	// Profiling is TV's part of a person's page: what TMDB knows them for.
	Profiling = knownfor.Profiling
)
