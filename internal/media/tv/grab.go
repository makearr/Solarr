package tv

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
)

// Grab is Sonarr's automatic search: search one season, then send its best accepted release to a download client.
// Interactive search is Search alone, with a person choosing what to send.
type Grab struct {
	Search   Search
	Dispatch dispatch.Dispatch
}

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult struct {
	Search          ItemSearchResult
	Sent            *library.Acquisition
	AlreadyInFlight bool
	// Held means the best release waits for room in its client's queue (ADR-57's HELD).
	Held bool
	// Refused counts releases the client declined before one was taken, or none was.
	Refused int
	// NoRoom counts releases passed over because the library has no root with space for them.
	NoRoom   int
	Refusals []string
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the best accepted release,
// or holds it when origin may wait. Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(ctx context.Context, itemID int64, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	return g.SearchEpisodeAndSend(ctx, itemID, 0, conns, origin, at)
}

// SearchEpisodeAndSend is SearchAndSend for one episode of the season; zero is what the season is missing.
func (g Grab) SearchEpisodeAndSend(ctx context.Context, itemID int64, episode int, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	if _, inFlight, err := g.Dispatch.Acquisitions.InFlightForItem(ctx, itemID); err != nil {
		return GrabResult{}, fmt.Errorf("tv: grab item %d: %w", itemID, err)
	} else if inFlight {
		return GrabResult{AlreadyInFlight: true}, nil
	}
	found, err := g.Search.SearchEpisode(ctx, itemID, episode, conns, at)
	if err != nil {
		return GrabResult{Search: found}, err
	}
	return g.sendBest(ctx, itemID, found, origin, at)
}

// sendBest sends found's best accepted release, or holds it when origin may wait; the search persists best first.
func (g Grab) sendBest(ctx context.Context, itemID int64, found ItemSearchResult, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	room, err := g.headroom(ctx, itemID, origin)
	if err != nil {
		return GrabResult{Search: found}, fmt.Errorf("tv: grab item %d: %w", itemID, err)
	}
	best, err := g.Dispatch.SendBest(ctx, itemID, found.Decisions, room, origin, at)
	result := GrabResult{
		Search: found, Sent: best.Sent, AlreadyInFlight: best.AlreadyInFlight, Held: best.Held,
		Refused: best.Refused, NoRoom: best.NoRoom, Refusals: best.Refusals,
	}
	if err != nil {
		return result, fmt.Errorf("tv: grab item %d: %w", itemID, err)
	}
	return result, nil
}

// headroom is how much an automatic grab for itemID may bring in; a person's own grab is never gated.
func (g Grab) headroom(ctx context.Context, itemID int64, origin dispatch.GrabOrigin) (dispatch.Headroom, error) {
	if origin == dispatch.GrabUser || g.Search.Settings == nil {
		return dispatch.Headroom{}, nil
	}
	item, err := g.Search.Desired.Get(ctx, itemID)
	if err != nil {
		return dispatch.Headroom{}, err
	}
	free, measured := g.Search.Settings.Snapshot().AcquisitionHeadroom(item.CategoryID)
	return g.Dispatch.HeadroomFor(ctx, item.CategoryID, origin, free, measured)
}
