package sabnzbd

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// historyLimit bounds how much of SABnzbd's history one poll reads: enough for every download
// finished since the last poll on any real setup.
const historyLimit = 200

// Definition is SABnzbd as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "sabnzbd", Label: "SABnzbd", Protocol: download.ProtocolUsenet,
		AddressPlaceholder: "http://sabnzbd:8080", SecretLabel: "API key", Categories: true,
	}
}

// Downloader is SABnzbd's part of download.UsenetClient; download.NewUsenet supplies the rest.
type Downloader struct{ Client Client }

var _ download.UsenetClient = Downloader{}

// AddNZB uploads one NZB.
func (d Downloader) AddNZB(ctx context.Context, request download.AddRequest) (string, error) {
	return d.Client.AddFile(ctx, AddFileRequest{Name: request.Name, NZB: request.File, Category: request.Category})
}

// Probe proves the address (version) and the key (the download folder needs it).
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	version, err := d.Client.Version(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	folder, err := d.Client.CompleteFolder(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: fmt.Sprintf("SABnzbd %s", version), DownloadFolder: folder}
	if categories, err := d.Client.Categories(ctx); err == nil {
		probe.Categories = categories
	}
	return probe, nil
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
		name := slot.Filename
		if slot.Status == statusGrabbing {
			// Until the NZB is fetched the filename is "Trying to fetch NZB from <url>", API key and all.
			name = ""
		}
		downloads = append(downloads, download.Download{
			ClientRef: slot.ClientRef, Name: name, Category: slot.Category, State: queueState(slot.Status),
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

// Pause pauses the named downloads in SABnzbd's queue; the rest of its queue carries on.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.Client.SetPaused(ctx, clientRefs, true)
}

// Resume resumes the named downloads.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.Client.SetPaused(ctx, clientRefs, false)
}

const statusGrabbing = "Grabbing"

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
