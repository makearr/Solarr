package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Queue satisfies apiserver.Queue over the download poll's live view of every client.
type Queue struct {
	Poll       *poll.Job
	Ready      library.ReadyQueue
	Candidates library.Candidates
	Clock      clock.Clock
	// Describe names a download's album, artist, library, indexer and client.
	Describe func(ctx context.Context, acquisition library.Acquisition) (download.HistoryData, int64)
}

// heldShown bounds the held releases one queue read lists.
const heldShown = 200

func (a Queue) List(ctx context.Context) (apiserver.QueueSnapshot, error) {
	entries, statuses, err := a.Poll.Queue(ctx)
	if err != nil {
		return apiserver.QueueSnapshot{}, err
	}
	speeds := make(map[int64]int64, len(statuses))
	clients := make([]apiserver.QueueClientRow, len(statuses))
	for i, status := range statuses {
		speeds[status.ID] = status.Status.SpeedBytesPerSec
		if status.Status.Paused {
			speeds[status.ID] = 0
		}
		clients[i] = apiserver.QueueClientRow{
			ID: status.ID, Name: status.Name, Paused: status.Status.Paused,
			SpeedBytesPerSec: status.Status.SpeedBytesPerSec, Items: status.Status.Items,
			SizeLeftBytes: status.Status.SizeLeftBytes,
		}
		if status.Err != nil {
			clients[i].Error = status.Err.Error()
		}
	}
	rows := make([]apiserver.QueueRow, len(entries))
	for i, entry := range entries {
		data, workID := a.Describe(ctx, entry.Acquisition)
		row := apiserver.QueueRow{
			ID: entry.Acquisition.ID, CategoryID: data.CategoryID, Album: data.Album, Artist: data.Artist,
			ReleaseTitle: entry.Acquisition.CandidateTitle, Indexer: data.Indexer, Client: entry.ClientName,
			State: entry.Acquisition.State, Error: entry.Acquisition.LastError,
			AddedAt: time.Unix(entry.Acquisition.CreatedAt, 0).UTC(),
		}
		if workID != 0 {
			row.WorkID = &workID
		}
		if entry.Reported {
			row.ClientState = string(entry.Download.State)
			row.SizeBytes, row.SizeLeftBytes = entry.Download.SizeBytes, entry.Download.SizeLeftBytes
			if speed := speeds[entry.Acquisition.DownloaderID]; entry.Download.State == download.StateDownloading &&
				speed > 0 && row.SizeLeftBytes > 0 {
				eta := row.SizeLeftBytes / speed
				row.ETASeconds = &eta
			}
			if row.Error == "" {
				row.Error = entry.Download.Error
			}
		}
		rows[i] = row
	}
	held, err := a.held(ctx)
	if err != nil {
		return apiserver.QueueSnapshot{}, err
	}
	return apiserver.QueueSnapshot{Rows: rows, Clients: clients, Held: held}, nil
}

func (a Queue) held(ctx context.Context) ([]apiserver.HeldRow, error) {
	entries, err := a.Ready.Next(ctx, a.Clock.Now().Unix(), heldShown)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.HeldRow, 0, len(entries))
	for _, entry := range entries {
		candidate, err := a.Candidates.Get(ctx, entry.CandidateID)
		if err != nil {
			continue // expired and purged meanwhile
		}
		itemID, indexerID := entry.DesiredItemID, candidate.IndexerID
		data, workID := a.Describe(ctx, library.Acquisition{
			DesiredItemID: &itemID, IndexerID: &indexerID, CandidateTitle: candidate.Title,
		})
		row := apiserver.HeldRow{
			CategoryID: data.CategoryID, Album: data.Album, Artist: data.Artist, ReleaseTitle: candidate.Title,
			Indexer: data.Indexer, HeldAt: time.Unix(entry.QueuedAt, 0).UTC(), SizeBytes: candidate.SizeBytes,
			Origin: entry.Origin,
		}
		if workID != 0 {
			row.WorkID = &workID
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (a Queue) SetClientPaused(ctx context.Context, downloaderID int64, paused bool) error {
	return a.Poll.SetPaused(ctx, downloaderID, paused)
}

func (a Queue) Remove(ctx context.Context, id int64, blocklist bool) error {
	err := a.Poll.Remove(ctx, id, blocklist)
	if errors.Is(err, poll.ErrNotRemovable) {
		return apiserver.ErrQueueItemNotRemovable
	}
	return err
}
