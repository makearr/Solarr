package dispatch

import (
	"context"
	"errors"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
)

// Best is what sending the best of an item's decided releases came to.
type Best struct {
	Sent *library.Acquisition
	// AlreadyInFlight means a download for the item was on its way, so nothing was sent.
	AlreadyInFlight bool
	// Held means the best release waits for room in its client's queue (ADR-57's HELD).
	Held bool
	// Refused counts releases the client declined before one was taken, or none was.
	Refused int
	// NoRoom counts releases passed over because the library has no root with space for them.
	NoRoom int
	// Refusals says why each refused release was refused, in the order they were tried.
	Refusals []string
	// AboveRating means the work exceeds its library's maximum rating, so an automatic grab sent nothing.
	AboveRating bool
}

// Headroom is how much an automatic grab may bring into a library (ADR-12: free space is a hard precondition).
// Gated false means nothing limits it: a person's own grab, or a library with no measured root.
type Headroom struct {
	Bytes int64
	Gated bool
}

// HeadroomFor is free the library's free space less what is already on its way; free space is measured every six
// hours, so what is in flight has not been subtracted from it yet.
func (d Dispatch) HeadroomFor(ctx context.Context, categoryID int64, origin GrabOrigin, free int64, measured bool) (Headroom, error) {
	if origin == GrabUser || !measured {
		return Headroom{}, nil
	}
	inFlight, err := d.Acquisitions.InFlightBytes(ctx, categoryID)
	if err != nil {
		return Headroom{}, err
	}
	return Headroom{Bytes: free - inFlight, Gated: true}, nil
}

// SendBest sends the best accepted release of decisions, which are best first, or holds it when origin may wait.
// One the client declines or that has no client for its protocol is passed over for the next. Finding nothing
// acceptable is not an error.
func (d Dispatch) SendBest(
	ctx context.Context, itemID int64, decisions []library.DecisionDetail, room Headroom, origin GrabOrigin, at time.Time,
) (Best, error) {
	var best Best
	for _, detail := range decisions {
		if detail.Decision.Outcome != string(model.OutcomeAccept) {
			continue
		}
		if size := detail.ReleaseCandidate.SizeBytes; room.Gated && size != nil && *size > room.Bytes {
			best.NoRoom++
			continue
		}
		if held, err := d.HoldIfFull(ctx, itemID, detail, origin, at); err != nil {
			return best, err
		} else if held {
			best.Held = true
			return best, nil
		}
		sent, err := d.SendRelease(ctx, itemID, detail.ReleaseCandidate, at)
		switch {
		case errors.Is(err, library.ErrAcquisitionAlreadyInFlight):
			best.AlreadyInFlight = true
			return best, nil
		// A torrent with no torrent client yet: the next best release may still be sendable.
		case errors.Is(err, ErrNoDownloaderForProtocol):
			continue
		// The client declined this one (a duplicate, a broken NZB): the next is tried, as Lidarr does.
		case errors.Is(err, download.ErrRefused), errors.Is(err, ErrContentBlocklisted):
			best.Refused++
			best.Refusals = append(best.Refusals, detail.ReleaseCandidate.Title+": "+err.Error())
			continue
		case err != nil:
			return best, err
		}
		best.Sent = &sent
		return best, nil
	}
	return best, nil
}
