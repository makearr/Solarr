package tv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
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

func (g Grab) sendBest(ctx context.Context, itemID int64, found ItemSearchResult, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	headroom, gated, err := g.headroom(ctx, itemID, origin)
	if err != nil {
		return GrabResult{Search: found}, fmt.Errorf("tv: grab item %d: %w", itemID, err)
	}
	refused, noRoom := 0, 0
	var refusals []string
	for _, detail := range found.Decisions {
		if detail.Decision.Outcome != string(model.OutcomeAccept) {
			continue
		}
		if size := detail.ReleaseCandidate.SizeBytes; gated && size != nil && *size > headroom {
			noRoom++
			continue
		}
		if held, err := g.Dispatch.HoldIfFull(ctx, itemID, detail, origin, at); err != nil {
			return GrabResult{Search: found}, fmt.Errorf("tv: grab item %d: %w", itemID, err)
		} else if held {
			return GrabResult{Search: found, Held: true}, nil
		}
		sent, err := g.Dispatch.Send(ctx, itemID, detail.Decision.ID, at)
		switch {
		case errors.Is(err, library.ErrAcquisitionAlreadyInFlight):
			return GrabResult{Search: found, AlreadyInFlight: true}, nil
		case errors.Is(err, dispatch.ErrNoDownloaderForProtocol):
			continue
		case errors.Is(err, download.ErrRefused), errors.Is(err, dispatch.ErrContentBlocklisted):
			refused++
			refusals = append(refusals, detail.ReleaseCandidate.Title+": "+err.Error())
			continue
		case err != nil:
			return GrabResult{Search: found, Refused: refused, Refusals: refusals}, fmt.Errorf("tv: grab item %d: %w", itemID, err)
		}
		return GrabResult{Search: found, Sent: &sent, Refused: refused, NoRoom: noRoom, Refusals: refusals}, nil
	}
	return GrabResult{Search: found, Refused: refused, NoRoom: noRoom, Refusals: refusals}, nil
}

// headroom is how much an automatic grab may bring in (ADR-12: free space is a hard precondition); a person's own
// grab is never gated.
func (g Grab) headroom(ctx context.Context, itemID int64, origin dispatch.GrabOrigin) (int64, bool, error) {
	if origin == dispatch.GrabUser || g.Search.Settings == nil {
		return 0, false, nil
	}
	item, err := g.Search.Desired.Get(ctx, itemID)
	if err != nil {
		return 0, false, err
	}
	bytes, gated := g.Search.Settings.Snapshot().AcquisitionHeadroom(item.CategoryID)
	if !gated {
		return 0, false, nil
	}
	inFlight, err := g.Dispatch.Acquisitions.InFlightBytes(ctx, item.CategoryID)
	if err != nil {
		return 0, false, err
	}
	return bytes - inFlight, true, nil
}
