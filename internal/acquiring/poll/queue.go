package poll

import (
	"context"
	"slices"

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
	ids := make([]int64, 0, len(clients))
	for id := range clients {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	statuses := make([]ClientStatus, 0, len(ids))
	reported := make(map[int64]map[string]download.Download, len(ids))
	for _, id := range ids {
		client := clients[id]
		status := ClientStatus{ID: id, Name: client.Name}
		status.Status, status.Err = client.Client.QueueStatus(ctx)
		if status.Err == nil && hasActive(active, id) {
			downloads, err := client.Client.Downloads(ctx, "")
			status.Err = err
			byRef := make(map[string]download.Download, len(downloads))
			for _, entry := range downloads {
				byRef[entry.ClientRef] = entry
			}
			reported[id] = byRef
		}
		statuses = append(statuses, status)
	}

	entries := make([]Entry, len(active))
	for i, acquisition := range active {
		entry := Entry{Acquisition: acquisition, ClientName: clients[acquisition.DownloaderID].Name}
		entry.Download, entry.Reported = reported[acquisition.DownloaderID][acquisition.DownloadClientRef]
		entries[i] = entry
	}
	return entries, statuses, nil
}

func hasActive(active []library.Acquisition, clientID int64) bool {
	return slices.ContainsFunc(active, func(acquisition library.Acquisition) bool {
		return acquisition.DownloaderID == clientID
	})
}
