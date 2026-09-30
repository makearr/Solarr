package music

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
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
	// Held means the best release waits for room in its client's queue (ADR-57's HELD).
	Held bool
	// Refused counts releases the client declined before one was taken, or none was.
	Refused int
	// NoRoom counts releases passed over because the library has no root with space for them.
	NoRoom int
	// Refusals says why each refused release was refused, in the order they were tried.
	Refusals []string
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the
// best accepted release, or holds it when origin may wait. Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(
	ctx context.Context, itemID int64, indexers []IndexerConn, origin GrabOrigin, at time.Time,
) (GrabResult, error) {
	if _, inFlight, err := g.Dispatch.Acquisitions.InFlightForItem(ctx, itemID); err != nil {
		return GrabResult{}, fmt.Errorf("music: grab item %d: %w", itemID, err)
	} else if inFlight {
		return GrabResult{AlreadyInFlight: true}, nil
	}
	found, err := g.Search.SearchItem(ctx, itemID, indexers, at)
	if err != nil {
		return GrabResult{Search: found}, err
	}
	return g.sendBest(ctx, itemID, found, origin, at)
}

// JudgeFeedAndSend is RSS sync's grab: releases a feed listed are judged against a wanted album,
// and the best accepted one is sent as an RSS grab. Nothing is searched.
func (g Grab) JudgeFeedAndSend(
	ctx context.Context, itemID int64, conn IndexerConn, raws []indexer.RawItem, at time.Time,
) (GrabResult, error) {
	if _, inFlight, err := g.Dispatch.Acquisitions.InFlightForItem(ctx, itemID); err != nil {
		return GrabResult{}, fmt.Errorf("music: rss grab item %d: %w", itemID, err)
	} else if inFlight {
		return GrabResult{AlreadyInFlight: true}, nil
	}
	found, err := g.Search.JudgeFeed(ctx, itemID, conn, raws, at)
	if err != nil {
		return GrabResult{Search: found}, err
	}
	return g.sendBest(ctx, itemID, found, GrabRSS, at)
}

// sendBest sends found's best accepted release, or holds it when origin may wait; SearchItem
// persists best first. Finding nothing acceptable is not an error.
func (g Grab) sendBest(ctx context.Context, itemID int64, found ItemSearchResult, origin GrabOrigin, at time.Time) (GrabResult, error) {
	headroom, gated, err := g.headroom(ctx, itemID, origin)
	if err != nil {
		return GrabResult{Search: found}, fmt.Errorf("music: grab item %d: %w", itemID, err)
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
		if held, err := g.Dispatch.holdIfFull(ctx, itemID, detail, origin, at); err != nil {
			return GrabResult{Search: found}, fmt.Errorf("music: grab item %d: %w", itemID, err)
		} else if held {
			return GrabResult{Search: found, Held: true}, nil
		}
		sent, err := g.Dispatch.Send(ctx, itemID, detail.Decision.ID, at)
		if errors.Is(err, library.ErrAcquisitionAlreadyInFlight) {
			return GrabResult{Search: found, AlreadyInFlight: true}, nil
		}
		// A torrent with no torrent client yet: the next best release may still be sendable.
		if errors.Is(err, ErrNoDownloaderForProtocol) {
			continue
		}
		// The client declined this one (a duplicate, a broken NZB): Lidarr moves on to the next.
		if errors.Is(err, download.ErrRefused) || errors.Is(err, ErrContentBlocklisted) {
			refused++
			refusals = append(refusals, detail.ReleaseCandidate.Title+": "+err.Error())
			continue
		}
		if err != nil {
			return GrabResult{Search: found, Refused: refused, Refusals: refusals}, fmt.Errorf("music: grab item %d: %w", itemID, err)
		}
		return GrabResult{Search: found, Sent: &sent, Refused: refused, NoRoom: noRoom, Refusals: refusals}, nil
	}
	return GrabResult{Search: found, Refused: refused, NoRoom: noRoom, Refusals: refusals}, nil
}

// headroom is how much an automatic grab for itemID may bring in (ADR-12: free space is a hard
// precondition). A person's own grab is never gated, nor is a library with no measured root.
func (g Grab) headroom(ctx context.Context, itemID int64, origin GrabOrigin) (bytes int64, gated bool, err error) {
	if origin == GrabUser || g.Search.Settings == nil {
		return 0, false, nil
	}
	item, err := g.Search.Desired.Get(ctx, itemID)
	if err != nil {
		return 0, false, err
	}
	bytes, gated = g.Search.Settings.Snapshot().AcquisitionHeadroom(item.CategoryID)
	if !gated {
		return 0, false, nil
	}
	// Free space is measured every 6 hours; what is already on its way has not been subtracted yet.
	inFlight, err := g.Dispatch.Acquisitions.InFlightBytes(ctx, item.CategoryID)
	if err != nil {
		return 0, false, err
	}
	return bytes - inFlight, true, nil
}
