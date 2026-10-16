package movie

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Searching is the movie module's part of a search a person asks for: the movie, as in Radarr.
type Searching struct {
	Acquire Grab
	Conns   func(ctx context.Context) ([]judge.Conn, error)
	Clock   clock.Clock
}

// SearchWanted implements media.Searching.
func (s Searching) SearchWanted(ctx context.Context, search media.WantedSearch) (judge.Result, error) {
	conns, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, err
	}
	return s.Acquire.Search.SearchItem(ctx, search.ItemID, conns, s.Clock.Now())
}

// GrabWanted implements media.Searching.
func (s Searching) GrabWanted(ctx context.Context, search media.WantedSearch) (judge.Result, dispatch.Best, error) {
	conns, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, dispatch.Best{}, err
	}
	result, err := s.Acquire.SearchAndSend(ctx, search.ItemID, conns, dispatch.GrabUser, s.Clock.Now())
	return result.Search, result.Best, err
}

func (s Searching) prepare(ctx context.Context, search media.WantedSearch) ([]judge.Conn, error) {
	if len(search.Terms) > 0 {
		return nil, fmt.Errorf("%w: a movie is searched by its own name", media.ErrInvalidTerms)
	}
	if search.Part != "" {
		return nil, media.ErrWholeItemOnly
	}
	conns, err := s.Conns(ctx)
	if err != nil {
		return nil, fmt.Errorf("movie: search: indexers: %w", err)
	}
	if len(conns) == 0 {
		return nil, media.ErrNoIndexers
	}
	return conns, nil
}
