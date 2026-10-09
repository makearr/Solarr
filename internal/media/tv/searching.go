package tv

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Searching is TV's part of a search a person asks for: a season, or one episode of it, as in Sonarr.
type Searching struct {
	Acquire Grab
	Conns   func(ctx context.Context) ([]judge.Conn, error)
	Clock   clock.Clock
}

// SearchWanted implements media.Searching.
func (s Searching) SearchWanted(ctx context.Context, search media.WantedSearch) (judge.Result, error) {
	episode, conns, now, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, err
	}
	return s.Acquire.Search.SearchEpisode(ctx, search.ItemID, episode, conns, now)
}

// GrabWanted implements media.Searching.
func (s Searching) GrabWanted(ctx context.Context, search media.WantedSearch) (judge.Result, dispatch.Best, error) {
	episode, conns, now, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, dispatch.Best{}, err
	}
	result, err := s.Acquire.SearchEpisodeAndSend(ctx, search.ItemID, episode, conns, dispatch.GrabUser, now)
	return result.Search, dispatch.Best{
		Sent: result.Sent, AlreadyInFlight: result.AlreadyInFlight, Held: result.Held,
		Refused: result.Refused, NoRoom: result.NoRoom, Refusals: result.Refusals,
	}, err
}

// prepare reads the episode a part narrows the search to, zero for the whole season.
func (s Searching) prepare(ctx context.Context, search media.WantedSearch) (int, []judge.Conn, time.Time, error) {
	if len(search.Terms) > 0 {
		return 0, nil, time.Time{}, fmt.Errorf("%w: a season is searched by its own name", media.ErrInvalidTerms)
	}
	episode := 0
	if search.Part != "" {
		var season int
		if _, err := fmt.Sscanf(search.Part, "s%d:e%d", &season, &episode); err != nil || episode <= 0 {
			return 0, nil, time.Time{}, fmt.Errorf("tv: search: %q is not an episode", search.Part)
		}
	}
	conns, err := s.Conns(ctx)
	if err != nil {
		return 0, nil, time.Time{}, fmt.Errorf("tv: search: indexers: %w", err)
	}
	if len(conns) == 0 {
		return 0, nil, time.Time{}, media.ErrNoIndexers
	}
	return episode, conns, s.Clock.Now(), nil
}
