package works

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Searching is a media type's part of a search a person asks for: the whole item, or one part of it when the type
// reads parts.
type Searching struct {
	Acquire Grab
	Conns   func(ctx context.Context) ([]judge.Conn, error)
	Clock   clock.Clock
	// Name prefixes errors ("tv"); Work is what a search looks for ("season"), Name when empty.
	Name, Work string
	// Part reads the part a search is narrowed to (an episode's ref); nil searches whole items only, as Radarr does.
	Part func(ref string) (int, error)
}

// SearchWanted implements media.Searching.
func (s Searching) SearchWanted(ctx context.Context, search media.WantedSearch) (judge.Result, error) {
	part, conns, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, err
	}
	return s.Acquire.Search.SearchPart(ctx, search.ItemID, part, conns, s.Clock.Now())
}

// GrabWanted implements media.Searching.
func (s Searching) GrabWanted(ctx context.Context, search media.WantedSearch) (judge.Result, dispatch.Best, error) {
	part, conns, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, dispatch.Best{}, err
	}
	result, err := s.Acquire.SearchPartAndSend(ctx, search.ItemID, part, conns, dispatch.GrabUser, s.Clock.Now())
	return result.Search, result.Best, err
}

func (s Searching) prepare(ctx context.Context, search media.WantedSearch) (int, []judge.Conn, error) {
	work := s.Work
	if work == "" {
		work = s.Name
	}
	if len(search.Terms) > 0 {
		return 0, nil, fmt.Errorf("%w: a %s is searched by its own name", media.ErrInvalidTerms, work)
	}
	part := 0
	switch {
	case search.Part != "" && s.Part == nil:
		return 0, nil, media.ErrWholeItemOnly
	case search.Part != "":
		var err error
		if part, err = s.Part(search.Part); err != nil {
			return 0, nil, err
		}
	}
	conns, err := media.SearchConns(ctx, s.Name, s.Conns)
	return part, conns, err
}
