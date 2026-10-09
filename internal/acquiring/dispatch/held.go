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
	release := detail.ReleaseCandidate
	size := int64(0)
	if release.SizeBytes != nil {
		size = *release.SizeBytes
	}
	return true, d.Ready.Hold(ctx, library.ReadyEntry{
		CandidateID: release.ID, DesiredItemID: itemID, Priority: rank,
		QueuedAt: at.Unix(), ExpiresAt: release.ExpiresAt, Origin: string(origin),
		IndexerID: release.IndexerID, GUID: release.GUID, Title: release.Title, DownloadURL: release.DownloadURL,
		Protocol: release.Protocol, SizeBytes: size, Score: detail.Decision.Score,
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
		sendable, err := d.stillSendable(ctx, entry, at)
		if err != nil {
			return sent, err
		}
		if !sendable {
			if err := d.Ready.Remove(ctx, entry.CandidateID); err != nil {
				return sent, err
			}
			continue
		}
		downloader, found := firstMatchingProtocol(downloaders, entry.Protocol)
		if !found {
			continue // kept: a client for it may be enabled again
		}
		if room, err := d.hasRoom(ctx, downloader); err != nil {
			return sent, err
		} else if !room {
			continue
		}
		_, err = d.SendRelease(ctx, entry.DesiredItemID, heldRelease(entry), at)
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

// stillSendable checks a held release may still go (section 10.5): still monitored, nothing on its way, not
// blocklisted. It reads only library.db; the release travels with the entry.
func (d Dispatch) stillSendable(ctx context.Context, entry library.ReadyEntry, at time.Time) (bool, error) {
	item, err := d.Desired.Get(ctx, entry.DesiredItemID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // the album was removed meanwhile
	}
	if err != nil || item.Monitored == 0 {
		return false, err
	}
	if _, inFlight, err := d.Acquisitions.InFlightForItem(ctx, entry.DesiredItemID); err != nil || inFlight {
		return false, err
	}
	if d.Blocklist != nil {
		indexerID := entry.IndexerID
		if blocked, err := d.Blocklist.IsBlocked(ctx, &indexerID, entry.GUID, "", at.Unix()); err != nil || blocked {
			return false, err
		}
	}
	return true, nil
}

// heldRelease is the release a held entry carries.
func heldRelease(entry library.ReadyEntry) library.ReleaseCandidate {
	release := library.ReleaseCandidate{
		ID: entry.CandidateID, IndexerID: entry.IndexerID, GUID: entry.GUID, Title: entry.Title,
		DownloadURL: entry.DownloadURL, Protocol: entry.Protocol, ExpiresAt: entry.ExpiresAt,
	}
	if entry.SizeBytes > 0 {
		size := entry.SizeBytes
		release.SizeBytes = &size
	}
	return release
}
