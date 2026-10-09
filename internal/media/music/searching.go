package music

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// searchTerms are the words a person may search an album with instead of its own.
var termChoices = []media.Choice{{Key: "artist", Label: "Artist"}, {Key: "album", Label: "Album"}}

// Searching is music's part of a search a person asks for: always a whole album, as in Lidarr.
type Searching struct {
	Acquire  Grab
	Indexers IndexerSource
	Clock    clock.Clock
}

// SearchWanted implements media.Searching.
func (s Searching) SearchWanted(ctx context.Context, search media.WantedSearch) (judge.Result, error) {
	conns, now, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, err
	}
	var custom *SearchTerms
	if artist, album := search.Terms["artist"], search.Terms["album"]; artist != "" || album != "" {
		if artist == "" || album == "" {
			return judge.Result{}, fmt.Errorf("%w: different terms need both an artist and an album", media.ErrInvalidTerms)
		}
		custom = &SearchTerms{Artist: artist, Album: album}
	}
	return s.Acquire.Search.SearchItemWithTerms(ctx, search.ItemID, conns, now, custom)
}

// GrabWanted implements media.Searching.
func (s Searching) GrabWanted(ctx context.Context, search media.WantedSearch) (judge.Result, dispatch.Best, error) {
	conns, now, err := s.prepare(ctx, search)
	if err != nil {
		return judge.Result{}, dispatch.Best{}, err
	}
	result, err := s.Acquire.SearchAndSend(ctx, search.ItemID, conns, dispatch.GrabUser, now)
	return result.Search, dispatch.Best{
		Sent: result.Sent, AlreadyInFlight: result.AlreadyInFlight, Held: result.Held,
		Refused: result.Refused, NoRoom: result.NoRoom, Refusals: result.Refusals,
	}, err
}

func (s Searching) prepare(ctx context.Context, search media.WantedSearch) ([]judge.Conn, time.Time, error) {
	if search.Part != "" {
		return nil, time.Time{}, media.ErrWholeItemOnly
	}
	conns, err := s.Indexers(ctx)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("music: search: indexers: %w", err)
	}
	if len(conns) == 0 {
		return nil, time.Time{}, media.ErrNoIndexers
	}
	return conns, s.Clock.Now(), nil
}
