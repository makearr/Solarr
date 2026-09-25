package music

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
)

// Grab is Lidarr's automatic search: search one album, then send its best accepted release to a
// download client. Interactive search is Search alone, with a person choosing what to send.
type Grab struct {
	Search   Search
	Dispatch Dispatch
}

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult struct {
	Search ItemSearchResult
	Sent   *library.Acquisition
	// AlreadyInFlight means a download for the album was on its way, so nothing was searched.
	AlreadyInFlight bool
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the
// best accepted release. Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(ctx context.Context, itemID int64, indexers []IndexerConn, at time.Time) (GrabResult, error) {
	if _, inFlight, err := g.Dispatch.Acquisitions.InFlightForItem(ctx, itemID); err != nil {
		return GrabResult{}, fmt.Errorf("music: grab item %d: %w", itemID, err)
	} else if inFlight {
		return GrabResult{AlreadyInFlight: true}, nil
	}
	found, err := g.Search.SearchItem(ctx, itemID, indexers, at)
	if err != nil {
		return GrabResult{Search: found}, err
	}
	// SearchItem persists best first.
	for _, detail := range found.Decisions {
		if detail.Decision.Outcome != string(model.OutcomeAccept) {
			continue
		}
		sent, err := g.Dispatch.Send(ctx, itemID, detail.Decision.ID, at)
		if errors.Is(err, library.ErrAcquisitionAlreadyInFlight) {
			return GrabResult{Search: found, AlreadyInFlight: true}, nil
		}
		// A torrent with no torrent client yet: the next best release may still be sendable.
		if errors.Is(err, ErrNoDownloaderForProtocol) {
			continue
		}
		if err != nil {
			return GrabResult{Search: found}, fmt.Errorf("music: grab item %d: %w", itemID, err)
		}
		return GrabResult{Search: found, Sent: &sent}, nil
	}
	return GrabResult{Search: found}, nil
}
