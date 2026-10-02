package qbittorrent

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Downloader is qBittorrent behind download.Client. A torrent's reference is its info hash.
type Downloader struct{ Client Client }

var _ download.Client = Downloader{}

// Add sends the torrent file, or a magnet link when the indexer answered one.
func (d Downloader) Add(ctx context.Context, request download.AddRequest) (string, error) {
	hash, err := download.InfoHash(request.File)
	if err != nil {
		return "", err
	}
	add := AddRequest{File: request.File, Category: request.Category}
	if strings.HasPrefix(string(request.File), "magnet:") {
		add = AddRequest{Magnet: string(request.File), Category: request.Category}
	}
	if err := d.Client.Add(ctx, add); err != nil {
		return "", fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	return hash, nil
}

// Downloads answers the torrents in category. A finished torrent is completed whether it is seeding or stopped.
func (d Downloader) Downloads(ctx context.Context, category string) ([]download.Download, error) {
	torrents, err := d.Client.Torrents(ctx, category)
	if err != nil {
		return nil, err
	}
	downloads := make([]download.Download, len(torrents))
	for i, torrent := range torrents {
		downloads[i] = download.Download{
			ClientRef: torrent.Hash, Name: torrent.Name, Category: torrent.Category, State: stateOf(torrent.State),
			SizeBytes: torrent.Size, SizeLeftBytes: torrent.AmountLeft,
		}
		if downloads[i].State == download.StateCompleted {
			downloads[i].Path = torrent.ContentPath
		}
		if downloads[i].State == download.StateFailed {
			downloads[i].Error = "qBittorrent reports " + torrent.State
		}
	}
	return downloads, nil
}

// QueueStatus answers the totals over every torrent still downloading.
func (d Downloader) QueueStatus(ctx context.Context) (download.QueueStatus, error) {
	torrents, err := d.Client.Torrents(ctx, "")
	if err != nil {
		return download.QueueStatus{}, err
	}
	transfer, err := d.Client.Transfer(ctx)
	if err != nil {
		return download.QueueStatus{}, err
	}
	status := download.QueueStatus{SpeedBytesPerSec: transfer.DLSpeed}
	for _, torrent := range torrents {
		if torrent.AmountLeft > 0 && stateOf(torrent.State) != download.StateFailed {
			status.Items++
			status.SizeLeftBytes += torrent.AmountLeft
		}
	}
	return status, nil
}

// Remove deletes one torrent; deleteFiles also deletes its data.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return d.Client.Delete(ctx, []string{clientRef}, deleteFiles)
}

// Pause pauses the named torrents only.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.Client.SetPaused(ctx, clientRefs, true)
}

// Resume resumes the named torrents only.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.Client.SetPaused(ctx, clientRefs, false)
}

// stateOf maps qBittorrent's torrent states: anything ending UP has everything it needs.
func stateOf(state string) download.State {
	switch state {
	case "error", "missingFiles":
		return download.StateFailed
	case "pausedDL", "stoppedDL":
		return download.StatePaused
	case "queuedDL":
		return download.StateQueued
	case "downloading", "stalledDL", "forcedDL", "metaDL", "forcedMetaDL":
		return download.StateDownloading
	case "checkingDL", "checkingResumeData", "allocating", "moving", "unknown":
		return download.StateProcessing
	}
	if strings.HasSuffix(state, "UP") {
		return download.StateCompleted
	}
	return download.StateProcessing
}
