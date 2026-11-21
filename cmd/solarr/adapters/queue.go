package adapters

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Queue satisfies apiserver.Queue over the download poll's live view of every client.
type Queue struct {
	Poll  *poll.Job
	Ready library.ReadyQueue
	Clock clock.Clock
	// Describer starts one read's naming of downloads: their album, artist, library, indexer and client.
	Describer func(ctx context.Context) func(library.Acquisition) (download.HistoryData, int64)
	// Memo lets the screens that poll at once share one read; nil reads every time.
	Memo *SnapshotMemo
}

// snapshotShared is how long a queue read answers the next ones: under a poll interval, over a burst of tabs.
const snapshotShared = 2 * time.Second

// SnapshotMemo keeps the last queue read for snapshotShared; callers that arrive while one is read wait for it.
type SnapshotMemo struct {
	mutex sync.Mutex
	at    time.Time
	value apiserver.QueueSnapshot
	held  bool
}

// forget drops what is kept, so the read after a person's action shows it; safe on nil.
func (m *SnapshotMemo) forget() {
	if m == nil {
		return
	}
	m.mutex.Lock()
	m.held = false
	m.mutex.Unlock()
}

// get answers the memo while it is fresh, else the result of read, which it keeps when it succeeds.
func (m *SnapshotMemo) get(ctx context.Context, now func() time.Time, read func(context.Context) (apiserver.QueueSnapshot, error)) (apiserver.QueueSnapshot, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.held && now().Sub(m.at) < snapshotShared {
		return m.value, nil
	}
	value, err := read(ctx)
	if err != nil {
		return apiserver.QueueSnapshot{}, err
	}
	m.value, m.at, m.held = value, now(), true
	return value, nil
}

// heldShown bounds the held releases one queue read lists.
const heldShown = 200

func (a Queue) List(ctx context.Context) (apiserver.QueueSnapshot, error) {
	if a.Memo == nil {
		return a.read(ctx)
	}
	return a.Memo.get(ctx, a.Clock.Now, a.read)
}

func (a Queue) read(ctx context.Context) (apiserver.QueueSnapshot, error) {
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
	describe := a.Describer(ctx)
	for i, entry := range entries {
		data, workID := describe(entry.Acquisition)
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
	held, err := a.held(ctx, describe)
	if err != nil {
		return apiserver.QueueSnapshot{}, err
	}
	return apiserver.QueueSnapshot{Rows: rows, Clients: clients, Held: held}, nil
}

func (a Queue) held(ctx context.Context, describe func(library.Acquisition) (download.HistoryData, int64)) ([]apiserver.HeldRow, error) {
	entries, err := a.Ready.Next(ctx, a.Clock.Now().Unix(), heldShown)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.HeldRow, 0, len(entries))
	for _, entry := range entries {
		itemID, indexerID := entry.DesiredItemID, entry.IndexerID
		data, workID := describe(library.Acquisition{
			DesiredItemID: &itemID, IndexerID: &indexerID, CandidateTitle: entry.Title,
		})
		row := apiserver.HeldRow{
			CategoryID: data.CategoryID, Album: data.Album, Artist: data.Artist, ReleaseTitle: entry.Title,
			Indexer: data.Indexer, HeldAt: time.Unix(entry.QueuedAt, 0).UTC(), Origin: entry.Origin,
		}
		if entry.SizeBytes > 0 {
			size := entry.SizeBytes
			row.SizeBytes = &size
		}
		if workID != 0 {
			row.WorkID = &workID
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (a Queue) SetClientPaused(ctx context.Context, downloaderID int64, paused bool) error {
	a.Memo.forget()
	return a.Poll.SetPaused(ctx, downloaderID, paused)
}

func (a Queue) Remove(ctx context.Context, id int64, blocklist bool) error {
	a.Memo.forget()
	err := a.Poll.Remove(ctx, id, blocklist)
	a.Memo.forget()
	if errors.Is(err, poll.ErrNotRemovable) {
		return apiserver.ErrQueueItemNotRemovable
	}
	return err
}
