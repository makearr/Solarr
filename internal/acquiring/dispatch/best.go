package dispatch

import (
	"context"
	"errors"
	"slices"
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
	// TooBigAfterFailure counts releases passed over for being far larger than one of the item's that just failed.
	TooBigAfterFailure int
	// AlsoSent counts the further releases sent in the same turn, for a set whose parts they hold apart from the first.
	AlsoSent int
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
	// RetryCeiling is the largest release an automatic retry may send after a failure; 0 means no failure to guard against.
	RetryCeiling int64
}

// maxSendsPerTurn bounds the downloads of one item started in one turn.
const maxSendsPerTurn = 5

func partsOverlap(a, b []string) bool {
	return slices.ContainsFunc(a, func(part string) bool { return slices.Contains(b, part) })
}

// retryGrowthFactor is how many times larger than a failed release the next automatic one may be.
const retryGrowthFactor = 3

// retryWindow is how long a failed release keeps limiting the size of the next automatic one.
const retryWindow = 24 * time.Hour

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
	var firstFetchFailure error
	var sentParts []string
	// Indexers that refused the account while a release was fetched: their other releases would only be refused alike.
	refusedIndexers := map[int64]bool{}
	for _, detail := range decisions {
		if detail.Decision.Outcome != string(model.OutcomeAccept) || refusedIndexers[detail.ReleaseCandidate.IndexerID] {
			continue
		}
		parts := d.partsOf(ctx, itemID, detail.ReleaseCandidate)
		// After the first send, only a release of parts nothing sent holds is worth another download.
		if best.Sent != nil && (len(parts) == 0 || partsOverlap(parts, sentParts)) {
			continue
		}
		if size := detail.ReleaseCandidate.SizeBytes; room.Gated && size != nil && *size > room.Bytes {
			best.NoRoom++
			continue
		}
		if size := detail.ReleaseCandidate.SizeBytes; room.RetryCeiling > 0 && size != nil && *size > room.RetryCeiling {
			best.TooBigAfterFailure++
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
		case errors.Is(err, ErrDownloadsPaused):
			return best, nil
		case errors.Is(err, library.ErrAcquisitionAlreadyInFlight):
			best.AlreadyInFlight = true
			// A release of parts may clash with one already on its way while another does not.
			if len(parts) == 0 {
				return best, nil
			}
			continue
		// A torrent with no torrent client yet: the next best release may still be sendable.
		case errors.Is(err, ErrNoDownloaderForProtocol):
			continue
		// The client declined this one (a duplicate, a broken NZB): the next is tried, as Lidarr does.
		case errors.Is(err, download.ErrRefused), errors.Is(err, ErrContentBlocklisted):
			best.Refused++
			best.Refusals = append(best.Refusals, detail.ReleaseCandidate.Title+": "+err.Error())
			continue
		// The indexer no longer has it: kept out for a day, and the next is tried.
		case errors.Is(err, ErrReleaseUnavailable):
			if blockErr := d.blocklistUnavailable(ctx, itemID, detail.ReleaseCandidate, at); blockErr != nil {
				return best, blockErr
			}
			best.Refused++
			best.Refusals = append(best.Refusals, detail.ReleaseCandidate.Title+": "+err.Error())
			continue
		// Fetching failed this time: the next is tried, and the failure is reported if nothing else is sent.
		case errors.Is(err, ErrReleaseFetchFailed):
			var notRelease *download.NotAReleaseError
			if errors.As(err, &notRelease) && notRelease.AccountRefused() {
				refusedIndexers[detail.ReleaseCandidate.IndexerID] = true
			}
			if firstFetchFailure == nil {
				firstFetchFailure = err
			}
			best.Refusals = append(best.Refusals, detail.ReleaseCandidate.Title+": "+err.Error())
			continue
		case err != nil:
			return best, err
		}
		if best.Sent == nil {
			best.Sent = &sent
		} else {
			best.AlsoSent++
		}
		sentParts = append(sentParts, parts...)
		// A release of the whole item, or the cap, ends the turn.
		if len(parts) == 0 || len(sentParts) == 0 || best.AlsoSent+1 >= maxSendsPerTurn {
			return best, nil
		}
	}
	if best.Sent != nil {
		return best, nil
	}
	return best, firstFetchFailure
}

// unavailableMemory is how long a release its indexer stopped serving stays out; a day lets a re-upload through.
const unavailableMemory = 24 * time.Hour

func (d Dispatch) blocklistUnavailable(ctx context.Context, itemID int64, release library.ReleaseCandidate, at time.Time) error {
	if d.Blocklist == nil || release.GUID == "" {
		return nil
	}
	expires := at.Add(unavailableMemory).Unix()
	_, err := d.Blocklist.Create(ctx, library.NewBlocklistEntry{
		IndexerID: &release.IndexerID, GUID: release.GUID, Title: release.Title, DesiredItemID: &itemID,
		Reason: library.BlocklistDownloadFailed, DetailJSON: `{"cause":"unavailable"}`, Source: library.BlocklistSourceAuto,
		ExpiresAt: &expires, CreatedAt: at.Unix(),
	})
	return err
}
