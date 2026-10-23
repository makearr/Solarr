package dispatch

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

// Automatic is the tail every media type's automatic grab shares: unless a download for the item is already on its
// way, search (or judge a feed), then send the best accepted release, or hold it when the origin may wait. A media type
// supplies only its own search.
type Automatic struct {
	Dispatch Dispatch
	// FreeSpace answers the free bytes of a library's roots and whether they are measured; nil leaves automatic grabs
	// ungated (ADR-12).
	FreeSpace func(categoryID int64) (free int64, measured bool)
	// Name prefixes errors: "movie", "tv".
	Name string
}

// Run searches itemID and sends the best release. search answers the decisions it recorded, best first.
func (a Automatic) Run(
	ctx context.Context, itemID int64, origin GrabOrigin, at time.Time, search func(ctx context.Context) ([]library.DecisionDetail, error),
) (Best, error) {
	if _, inFlight, err := a.Dispatch.Acquisitions.InFlightForItem(ctx, itemID); err != nil {
		return Best{}, fmt.Errorf("%s: grab item %d: %w", a.Name, itemID, err)
	} else if inFlight {
		return Best{AlreadyInFlight: true}, nil
	}
	if held, err := a.aboveRating(ctx, itemID, origin); err != nil || held {
		return Best{AboveRating: held}, err
	}
	decisions, err := search(ctx)
	if err != nil {
		return Best{}, err
	}
	return a.Send(ctx, itemID, decisions, origin, at)
}

// Send sends the best accepted of decisions, which are best first, within what the library has room for.
func (a Automatic) Send(ctx context.Context, itemID int64, decisions []library.DecisionDetail, origin GrabOrigin, at time.Time) (Best, error) {
	if held, err := a.aboveRating(ctx, itemID, origin); err != nil || held {
		return Best{AboveRating: held}, err
	}
	room, err := a.headroom(ctx, itemID, origin)
	if err != nil {
		return Best{}, fmt.Errorf("%s: grab item %d: %w", a.Name, itemID, err)
	}
	best, err := a.Dispatch.SendBest(ctx, itemID, decisions, room, origin, at)
	if err != nil {
		return best, fmt.Errorf("%s: grab item %d: %w", a.Name, itemID, err)
	}
	return best, nil
}

// aboveRating says an automatic grab is for a work its library's maximum rating does not admit; a person's own grab
// is never held.
func (a Automatic) aboveRating(ctx context.Context, itemID int64, origin GrabOrigin) (bool, error) {
	if origin == GrabUser || a.Dispatch.Admits == nil {
		return false, nil
	}
	categoryID, age, err := a.Dispatch.Desired.ItemRating(ctx, itemID)
	if err != nil {
		return false, fmt.Errorf("%s: grab item %d: %w", a.Name, itemID, err)
	}
	return !a.Dispatch.Admits(categoryID, age), nil
}

// headroom is how much an automatic grab for itemID may bring in; a person's own grab is never gated.
func (a Automatic) headroom(ctx context.Context, itemID int64, origin GrabOrigin) (Headroom, error) {
	if origin == GrabUser || a.FreeSpace == nil {
		return Headroom{}, nil
	}
	item, err := a.Dispatch.Desired.Get(ctx, itemID)
	if err != nil {
		return Headroom{}, err
	}
	free, measured := a.FreeSpace(item.CategoryID)
	return a.Dispatch.HeadroomFor(ctx, item.CategoryID, origin, free, measured)
}
