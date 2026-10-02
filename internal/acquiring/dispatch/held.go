package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/priority"
)

// heldBatch bounds how many held releases one pass looks at; the rest wait for the next poll.
const heldBatch = 50

// HoldIfFull holds detail instead of sending it when its client already has MaxQueued of Solarr's
// downloads (ADR-57): once sent, the client decides the order, not Solarr's priority. A user's or an
// RSS grab goes regardless when the client says so.
func (d Dispatch) HoldIfFull(
	ctx context.Context, itemID int64, detail library.DecisionDetail, origin GrabOrigin, at time.Time,
) (bool, error) {
	if d.Ready == nil {
		return false, nil
	}
	downloaders, err := d.Downloaders(ctx)
	if err != nil {
		return false, err
	}
	downloader, found := firstMatchingProtocol(downloaders, detail.ReleaseCandidate.Protocol)
	if !found || downloader.alwaysSent(origin) {
		return false, nil
	}
	if room, err := d.hasRoom(ctx, downloader); err != nil || room {
		return false, err
	}
	rank, err := d.heldPriority(ctx, itemID, origin, at)
	if err != nil {
		return false, err
	}
	return true, d.Ready.Hold(ctx, library.ReadyEntry{
		CandidateID: detail.ReleaseCandidate.ID, DesiredItemID: itemID, Priority: rank,
		QueuedAt: at.Unix(), ExpiresAt: detail.ReleaseCandidate.ExpiresAt, Origin: string(origin),
	})
}

// heldPriority orders held releases user, RSS, new, backfill (ADR-36's ladder); within one, the
// longest waiting goes first.
func (d Dispatch) heldPriority(ctx context.Context, itemID int64, origin GrabOrigin, at time.Time) (int64, error) {
	trigger := priority.TriggerUserRequest
	switch origin {
	case GrabUser:
	case GrabRSS:
		trigger = priority.TriggerRSS
	default:
		released, err := d.Desired.ReleaseDate(ctx, itemID)
		if err != nil {
			return 0, err
		}
		var releasedAt *time.Time
		if parsed, ok := priority.ParseReleaseDate(released); ok {
			releasedAt = &parsed
		}
		trigger = priority.ClassifyTrigger(releasedAt, at, priority.DefaultNewReleaseDays)
	}
	return priority.Compute(priority.DefaultWeights(), priority.Input{Trigger: trigger, QueuedSince: at}, at), nil
}

func (d Dispatch) hasRoom(ctx context.Context, downloader DownloaderConn) (bool, error) {
	if downloader.MaxQueued <= 0 {
		return true, nil
	}
	queued, err := d.Acquisitions.QueuedOn(ctx, downloader.ID)
	return queued < downloader.MaxQueued, err
}

// SendHeld sends held releases, highest priority first, while their clients have room. Each is
// checked again first (section 10.5): still monitored, not blocklisted, nothing already on its way.
func (d Dispatch) SendHeld(ctx context.Context, at time.Time) (int, error) {
	if d.Ready == nil {
		return 0, nil
	}
	if err := d.Ready.PurgeExpired(ctx, at.Unix()); err != nil {
		return 0, err
	}
	entries, err := d.Ready.Next(ctx, at.Unix(), heldBatch)
	if err != nil || len(entries) == 0 {
		return 0, err
	}
	downloaders, err := d.Downloaders(ctx)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, entry := range entries {
		detail, sendable, err := d.stillSendable(ctx, entry, at)
		if err != nil {
			return sent, err
		}
		if !sendable {
			if err := d.Ready.Remove(ctx, entry.CandidateID); err != nil {
				return sent, err
			}
			continue
		}
		downloader, found := firstMatchingProtocol(downloaders, detail.ReleaseCandidate.Protocol)
		if !found {
			continue // kept: a client for it may be enabled again
		}
		if room, err := d.hasRoom(ctx, downloader); err != nil {
			return sent, err
		} else if !room {
			continue
		}
		_, err = d.Send(ctx, entry.DesiredItemID, detail.Decision.ID, at)
		// Declined by the client: dropped, and the album searched afresh on its next turn.
		if err != nil && !errors.Is(err, library.ErrAcquisitionAlreadyInFlight) && !errors.Is(err, download.ErrRefused) &&
			!errors.Is(err, ErrContentBlocklisted) {
			return sent, fmt.Errorf("music: send held release %d: %w", entry.CandidateID, err)
		}
		if err := d.Ready.Remove(ctx, entry.CandidateID); err != nil {
			return sent, err
		}
		if err == nil {
			sent++
		}
	}
	return sent, nil
}

// stillSendable finds the accepted decision behind a held release and checks it may still go.
func (d Dispatch) stillSendable(ctx context.Context, entry library.ReadyEntry, at time.Time) (library.DecisionDetail, bool, error) {
	item, err := d.Desired.Get(ctx, entry.DesiredItemID)
	if errors.Is(err, sql.ErrNoRows) {
		return library.DecisionDetail{}, false, nil // the album was removed meanwhile
	}
	if err != nil || item.Monitored == 0 {
		return library.DecisionDetail{}, false, err
	}
	if _, inFlight, err := d.Acquisitions.InFlightForItem(ctx, entry.DesiredItemID); err != nil || inFlight {
		return library.DecisionDetail{}, false, err
	}
	details, err := d.Candidates.ListDecisionsForItem(ctx, entry.DesiredItemID, DecisionLookupLimit)
	if err != nil {
		return library.DecisionDetail{}, false, err
	}
	for _, detail := range details {
		if detail.ReleaseCandidate.ID != entry.CandidateID || detail.Decision.Outcome != "ACCEPT" {
			continue
		}
		if d.Blocklist != nil {
			blocked, err := d.Blocklist.IsBlocked(ctx, &detail.ReleaseCandidate.IndexerID, detail.ReleaseCandidate.GUID, "", at.Unix())
			if err != nil || blocked {
				return library.DecisionDetail{}, false, err
			}
		}
		return detail, true, nil
	}
	return library.DecisionDetail{}, false, nil
}
