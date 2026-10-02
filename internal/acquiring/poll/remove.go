package poll

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
)

// ErrNotRemovable refuses a download whose files are already moving into the library, or that ended.
var ErrNotRemovable = errors.New("poll: only a queued, downloading, finished or held-for-review download can be removed")

// Remove is Lidarr's "remove from queue": cancelled here first, so a poll never mistakes it for a
// failure, then taken out of the client with its files. With blocklist the release is never grabbed
// again and the album is searched for another, as after a failure.
func (j *Job) Remove(ctx context.Context, acquisitionID int64, blocklist bool) error {
	acquisition, err := j.config.Acquisitions.Get(ctx, acquisitionID)
	if err != nil {
		return err
	}
	switch {
	case acquisition.State == StateQueued, acquisition.State == StateDownloading, acquisition.State == StateCompleted:
	case acquisition.State == StateImporting && acquisition.LastError == WaitingForReview:
	default:
		return ErrNotRemovable
	}
	now := j.config.Clock.Now().Unix()
	detail, historyDetail := "removed from the queue", ""
	if blocklist && (acquisition.CandidateGUID != "" || acquisition.ContentHash != "") {
		if _, err := j.config.Blocklist.Create(ctx, library.NewBlocklistEntry{
			IndexerID: acquisition.IndexerID, GUID: acquisition.CandidateGUID, ContentHash: acquisition.ContentHash,
			Title:         acquisition.CandidateTitle,
			DesiredItemID: acquisition.DesiredItemID, Reason: library.BlocklistManual, Source: library.BlocklistSourceUser,
			CreatedAt: now,
		}); err != nil {
			return err
		}
		detail, historyDetail = "removed from the queue and blocklisted", "blocklisted"
	}
	if err := j.config.Acquisitions.SetState(ctx, acquisition.ID, StateCancelled, detail, now); err != nil {
		return err
	}
	j.config.History(ctx, download.HistoryRemoved, acquisition, historyDetail)

	clients, err := j.config.Clients(ctx)
	if err != nil {
		return err
	}
	if client, ok := clients[acquisition.DownloaderID]; ok {
		if err := client.Client.Remove(ctx, acquisition.DownloadClientRef, true); err != nil {
			return fmt.Errorf("remove from the download client: %w", err)
		}
	}
	if blocklist && acquisition.DesiredItemID != nil {
		if err := j.config.Search(ctx, *acquisition.DesiredItemID); err != nil {
			j.config.Logger.Warn("search after removing a download did not run",
				slog.Int64("desired_item_id", *acquisition.DesiredItemID), slog.Any("error", err))
		}
	}
	return nil
}

// SetPaused pauses or resumes Solarr's own downloads in one client that are still downloading,
// never the client itself: other apps share it (section 10.9, only its own categories).
func (j *Job) SetPaused(ctx context.Context, downloaderID int64, paused bool) error {
	clients, err := j.config.Clients(ctx)
	if err != nil {
		return err
	}
	client, found := clients[downloaderID]
	if !found {
		return fmt.Errorf("poll: download client %d is not enabled", downloaderID)
	}
	active, err := j.config.Acquisitions.ListActive(ctx)
	if err != nil {
		return err
	}
	var refs []string
	for _, acquisition := range active {
		if acquisition.DownloaderID == downloaderID &&
			(acquisition.State == StateQueued || acquisition.State == StateDownloading) {
			refs = append(refs, acquisition.DownloadClientRef)
		}
	}
	if paused {
		return client.Client.Pause(ctx, refs)
	}
	return client.Client.Resume(ctx, refs)
}
