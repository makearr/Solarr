package tv

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Searching is TV's part of a search a person asks for, the shared one: a season, or one episode of it, as in Sonarr.
type Searching struct {
	Acquire Grab
	Conns   func(ctx context.Context) ([]judge.Conn, error)
	Clock   clock.Clock
}

func (s Searching) shared() works.Searching {
	return works.Searching{Acquire: s.Acquire.shared(), Conns: s.Conns, Clock: s.Clock, Name: "tv", Work: "season", Part: episodeOf}
}

// SearchWanted implements media.Searching.
func (s Searching) SearchWanted(ctx context.Context, search media.WantedSearch) (judge.Result, error) {
	return s.shared().SearchWanted(ctx, search)
}

// GrabWanted implements media.Searching.
func (s Searching) GrabWanted(ctx context.Context, search media.WantedSearch) (judge.Result, dispatch.Best, error) {
	return s.shared().GrabWanted(ctx, search)
}

// episodeOf reads the episode a part ref names ("s1:e5").
func episodeOf(ref string) (int, error) {
	var season, episode int
	if _, err := fmt.Sscanf(ref, "s%d:e%d", &season, &episode); err != nil || episode <= 0 {
		return 0, fmt.Errorf("tv: search: %q is not an episode", ref)
	}
	return episode, nil
}
