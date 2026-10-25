package movie

import (
	"context"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// NewSearching is the movie module's part of a search a person asks for: the whole movie, as in Radarr.
func NewSearching(acquire Grab, conns func(ctx context.Context) ([]judge.Conn, error), clock clock.Clock) works.Searching {
	return works.Searching{Acquire: acquire, Conns: conns, Clock: clock, Name: "movie"}
}
