package qbittorrent

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is qBittorrent as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "qbittorrent", Label: "qBittorrent", Protocol: download.ProtocolTorrent,
		AddressPlaceholder: "http://qbittorrent:8080", UserName: true, SecretLabel: "Password",
		KeepsCompleted: true, Categories: true,
	}
}

// Downloader is qBittorrent's part of download.TorrentClient; download.NewTorrent supplies the rest. A torrent's
// reference is its info hash.
type Downloader struct{ Client Client }

var (
	_ download.TorrentClient = Downloader{}
	_ download.TransferSpeed = Downloader{}
	_ download.FinishedFiles = Downloader{}
)

// AddTorrentFile sends the torrent file.
func (d Downloader) AddTorrentFile(ctx context.Context, hash string, request download.AddRequest) (string, error) {
	return d.add(ctx, hash, AddRequest{File: request.File, Category: request.Category})
}

// AddMagnet sends a magnet link the indexer answered.
func (d Downloader) AddMagnet(ctx context.Context, hash, magnet string, request download.AddRequest) (string, error) {
	return d.add(ctx, hash, AddRequest{Magnet: magnet, Category: request.Category})
}

func (d Downloader) add(ctx context.Context, hash string, add AddRequest) (string, error) {
	if err := d.Client.Add(ctx, add); err != nil {
		return "", fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	return hash, nil
}

// Probe proves the address and the login (the version call needs it); qBittorrent creates a missing category.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	version, err := d.Client.Version(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: "qBittorrent " + version, CreatesCategories: true}
	if categories, err := d.Client.Categories(ctx); err == nil {
		probe.Categories = categories
	}
	probe.DownloadFolder, _ = d.Client.SavePath(ctx)
	return probe, nil
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
			Ratio: torrent.Ratio, SeedingSeconds: torrent.SeedingTime,
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

// FinishedFiles implements download.FinishedFiles: the files of a torrent that are fully downloaded and wanted, below
// the torrent's own top folder; qBittorrent names them relative to where the torrent is being written.
func (d Downloader) FinishedFiles(ctx context.Context, clientRef string) (string, []string, error) {
	torrent, found, err := d.Client.Torrent(ctx, clientRef)
	if err != nil || !found {
		return "", nil, err
	}
	files, err := d.Client.Files(ctx, clientRef)
	if err != nil {
		return "", nil, err
	}
	base := torrent.DownloadPath
	if base == "" {
		base = torrent.SavePath
	}
	top := ""
	var finished []string
	for _, file := range files {
		head, rest, nested := strings.Cut(file.Name, "/")
		if !nested || (top != "" && head != top) {
			return "", nil, nil // files loose in the client's folder: there is no folder of the download's own
		}
		top = head
		if file.Priority != 0 && file.Progress >= 1 {
			finished = append(finished, rest)
		}
	}
	if top == "" || len(finished) == 0 {
		return "", nil, nil
	}
	return path.Join(base, top), finished, nil
}

// TransferSpeed answers qBittorrent's overall download speed.
func (d Downloader) TransferSpeed(ctx context.Context) (int64, error) {
	transfer, err := d.Client.Transfer(ctx)
	return transfer.DLSpeed, err
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
	case "checkingDL", "checkingUP", "checkingResumeData", "allocating", "moving", "unknown":
		return download.StateProcessing
	case "uploading":
		// Seeding to connected peers: finished, though it lacks the UP suffix the other seeding states share.
		return download.StateCompleted
	}
	if strings.HasSuffix(state, "UP") {
		return download.StateCompleted
	}
	return download.StateProcessing
}
