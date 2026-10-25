package poll

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
)

// Entry is one download in flight as the Downloads queue shows it: Solarr's record and, when the
// client still reports it, the client's own view of it.
type Entry struct {
	Acquisition library.Acquisition
	ClientName  string
	// Reported is false while the client does not list it: not fetched yet, or gone.
	Reported bool
	Download download.Download
}

// ClientStatus is one client's own queue, or why it could not be read.
type ClientStatus struct {
	ID     int64
	Name   string
	Status download.QueueStatus
	Err    error
}

// Queue answers every download in flight and every enabled client's queue, read live as Lidarr's
// queue is. A client that cannot be reached is reported, and its downloads listed as Solarr last knew them.
func (j *Job) Queue(ctx context.Context) ([]Entry, []ClientStatus, error) {
	active, err := j.config.Acquisitions.ListActive(ctx)
	if err != nil {
		return nil, nil, err
	}
	clients, err := j.config.Clients(ctx)
	if err != nil {
		return nil, nil, err
	}
	read := j.clientReads(ctx, active, clients)

	entries := make([]Entry, len(active))
	for i, acquisition := range active {
		entry := Entry{Acquisition: acquisition, ClientName: clients[acquisition.DownloaderID].Name}
		entry.Download, entry.Reported = read.reported[acquisition.DownloaderID][acquisition.DownloadClientRef]
		entries[i] = entry
	}
	return entries, slices.Clone(read.statuses), nil
}

// queueReuse is how long one round of client reads answers every queue read: each open page polls, and without it
// every tab asks every download client again.
const queueReuse = 2 * time.Second

// clientRead is one round of asking the clients: their own queues, and the downloads of each with one in flight.
type clientRead struct {
	statuses []ClientStatus
	reported map[int64]map[string]download.Download
	at       time.Time
}

// clientReads answers a recent round of client reads, or asks the clients again; a read already under way is waited
// for rather than repeated. The clients are asked all at once, so one that is slow to answer delays the queue by its
// own wait, not by everyone's.
func (j *Job) clientReads(ctx context.Context, active []library.Acquisition, clients map[int64]Client) clientRead {
	j.mu.Lock()
	if j.lastRead != nil && j.now().Sub(j.lastRead.at) < queueReuse && sameClients(j.lastRead, clients) {
		read := *j.lastRead
		j.mu.Unlock()
		return read
	}
	if wait := j.reading; wait != nil {
		j.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
		}
		j.mu.Lock()
		read := j.lastRead
		j.mu.Unlock()
		if read != nil && sameClients(read, clients) {
			return *read
		}
		return j.readClients(ctx, active, clients)
	}
	done := make(chan struct{})
	j.reading = done
	j.mu.Unlock()

	read := j.readClients(ctx, active, clients)
	j.mu.Lock()
	if ctx.Err() == nil {
		j.lastRead = &read
	}
	j.reading = nil
	j.mu.Unlock()
	close(done)
	return read
}

func (j *Job) readClients(ctx context.Context, active []library.Acquisition, clients map[int64]Client) clientRead {
	ids := make([]int64, 0, len(clients))
	for id := range clients {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	statuses := make([]ClientStatus, len(ids))
	downloads := make([]map[string]download.Download, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			client := clients[id]
			status := ClientStatus{ID: id, Name: client.Name}
			status.Status, status.Err = client.Client.QueueStatus(ctx)
			if status.Err == nil && hasActive(active, id) {
				listed, err := client.Client.Downloads(ctx, "")
				status.Err = err
				byRef := make(map[string]download.Download, len(listed))
				for _, entry := range listed {
					byRef[entry.ClientRef] = entry
				}
				downloads[i] = byRef
			}
			statuses[i] = status
		})
	}
	wg.Wait()

	reported := make(map[int64]map[string]download.Download, len(ids))
	for i, id := range ids {
		if downloads[i] != nil {
			reported[id] = downloads[i]
		}
	}
	return clientRead{statuses: statuses, reported: reported, at: j.now()}
}

// sameClients says whether a read asked exactly the clients configured now, so a client just added or removed is
// never answered from a read that did not ask it.
func sameClients(read *clientRead, clients map[int64]Client) bool {
	if len(read.statuses) != len(clients) {
		return false
	}
	for _, status := range read.statuses {
		if _, found := clients[status.ID]; !found {
			return false
		}
	}
	return true
}

func (j *Job) now() time.Time {
	if j.config.Clock == nil {
		return time.Now()
	}
	return j.config.Clock.Now()
}

func hasActive(active []library.Acquisition, clientID int64) bool {
	return slices.ContainsFunc(active, func(acquisition library.Acquisition) bool {
		return acquisition.DownloaderID == clientID
	})
}
