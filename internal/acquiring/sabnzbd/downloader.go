package sabnzbd

import (
	"context"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// historyLimit bounds how much of SABnzbd's history one poll reads: enough for every download
// finished since the last poll on any real setup.
const historyLimit = 200

// Downloader is SABnzbd behind download.Client.
type Downloader struct{ Client Client }

var _ download.Client = Downloader{}

// Add sends one NZB by URL.
func (d Downloader) Add(ctx context.Context, request download.AddRequest) (string, error) {
	return d.Client.AddURL(ctx, AddURLRequest{NZBURL: request.URL, Category: request.Category})
}

// Downloads answers the queue and the recent history in category.
func (d Downloader) Downloads(ctx context.Context, category string) ([]download.Download, error) {
	queue, err := d.Client.Queue(ctx)
	if err != nil {
		return nil, err
	}
	history, err := d.Client.History(ctx, category, historyLimit)
	if err != nil {
		return nil, err
	}
	var downloads []download.Download
	for _, slot := range queue.Slots {
		if category != "" && !strings.EqualFold(slot.Category, category) {
			continue
		}
		downloads = append(downloads, download.Download{
			ClientRef: slot.ClientRef, Name: slot.Filename, Category: slot.Category, State: queueState(slot.Status),
			SizeBytes: slot.SizeBytes, SizeLeftBytes: slot.SizeLeftBytes,
		})
	}
	for _, slot := range history {
		entry := download.Download{
			ClientRef: slot.ClientRef, Name: slot.Name, Category: slot.Category, State: historyState(slot.Status),
			SizeBytes: slot.SizeBytes, Error: slot.FailMessage,
		}
		if entry.State == download.StateCompleted {
			entry.Path = slot.StoragePath
		}
		downloads = append(downloads, entry)
	}
	return downloads, nil
}

// QueueStatus answers the queue's totals.
func (d Downloader) QueueStatus(ctx context.Context) (download.QueueStatus, error) {
	queue, err := d.Client.Queue(ctx)
	if err != nil {
		return download.QueueStatus{}, err
	}
	return download.QueueStatus{
		Paused: queue.Paused, SpeedBytesPerSec: queue.SpeedBytesPerSec, Items: queue.SlotsTotal,
		SizeLeftBytes: queue.SizeLeftBytes,
	}, nil
}

// Remove deletes one download from the queue and the history.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return d.Client.Remove(ctx, clientRef, deleteFiles)
}

func queueState(status string) download.State {
	switch status {
	case "Paused":
		return download.StatePaused
	case "Downloading":
		return download.StateDownloading
	default:
		return download.StateQueued
	}
}

func historyState(status string) download.State {
	switch status {
	case "Completed":
		return download.StateCompleted
	case "Failed":
		return download.StateFailed
	default:
		return download.StateProcessing
	}
}
