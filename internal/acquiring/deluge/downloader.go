package deluge

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is Deluge as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "deluge", Label: "Deluge", Protocol: download.ProtocolTorrent,
		AddressPlaceholder: "http://deluge:8112", SecretLabel: "Web UI password", KeepsCompleted: true, Categories: true,
	}
}

// Downloader is Deluge's part of download.TorrentClient; download.NewTorrent supplies the rest.
type Downloader struct{ Client Client }

var (
	_ download.TorrentClient = Downloader{}
	_ download.TransferSpeed = Downloader{}
)

// AddTorrentFile sends the torrent file, then labels it.
func (d Downloader) AddTorrentFile(ctx context.Context, hash string, request download.AddRequest) (string, error) {
	added, err := d.Client.AddFile(ctx, request.Name+".torrent", base64.StdEncoding.EncodeToString(request.File))
	return d.label(ctx, hash, added, request.Category, err)
}

// AddMagnet sends a magnet link, then labels it.
func (d Downloader) AddMagnet(ctx context.Context, hash, magnet string, request download.AddRequest) (string, error) {
	added, err := d.Client.AddMagnet(ctx, magnet)
	return d.label(ctx, hash, added, request.Category, err)
}

// label files the added torrent under category. Deluge answers no hash for a torrent it already has, which is refused,
// as qBittorrent's "Fails." is.
func (d Downloader) label(ctx context.Context, hash, added, category string, err error) (string, error) {
	var refused *MethodError
	if errors.As(err, &refused) {
		return "", fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	if err != nil {
		return "", err
	}
	if added == "" {
		return "", fmt.Errorf("%w: Deluge added nothing (it may already have the torrent)", download.ErrRefused)
	}
	if category != "" {
		if err := d.Client.SetLabel(ctx, hash, category); err != nil {
			return "", fmt.Errorf("deluge: label the torrent (is the Label plugin enabled?): %w", err)
		}
	}
	return hash, nil
}

// Downloads answers every torrent.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	torrents, err := d.Client.Torrents(ctx)
	if err != nil {
		return nil, err
	}
	downloads := make([]download.Download, len(torrents))
	for i, torrent := range torrents {
		downloads[i] = download.Download{
			ClientRef: strings.ToLower(torrent.Hash), Name: torrent.Name, Category: torrent.Label, State: stateOf(torrent),
			SizeBytes: torrent.TotalSize, SizeLeftBytes: max(torrent.TotalSize-torrent.TotalDone, 0),
			Ratio: torrent.Ratio, SeedingSeconds: torrent.SeedingTime, SpeedBytesPerSec: int64(torrent.Rate),
		}
		switch downloads[i].State {
		case download.StateCompleted:
			downloads[i].Path = strings.TrimRight(torrent.SavePath, "/") + "/" + torrent.Name
		case download.StateFailed:
			downloads[i].Error = "Deluge reports " + torrent.Message
		}
	}
	return downloads, nil
}

func stateOf(torrent Torrent) download.State {
	switch torrent.State {
	case "Error":
		return download.StateFailed
	case "Seeding":
		return download.StateCompleted
	case "Paused":
		if torrent.IsFinished {
			return download.StateCompleted
		}
		return download.StatePaused
	case "Queued":
		if torrent.IsFinished {
			return download.StateCompleted
		}
		return download.StateQueued
	case "Downloading":
		return download.StateDownloading
	}
	return download.StateProcessing
}

// Remove deletes one torrent; deleteFiles also deletes its data.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return d.Client.Remove(ctx, clientRef, deleteFiles)
}

// Pause pauses the named torrents only.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.Client.SetPaused(ctx, clientRefs, true)
}

// Resume resumes the named torrents only.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.Client.SetPaused(ctx, clientRefs, false)
}

// TransferSpeed answers the overall download speed.
func (d Downloader) TransferSpeed(ctx context.Context) (int64, error) {
	return d.Client.DownloadRate(ctx)
}

// Probe proves the address, the password and the daemon; categories are the Label plugin's, which it creates.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	version, err := d.Client.Version(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: "Deluge " + version, CreatesCategories: true}
	if config, err := d.Client.Config(ctx); err == nil {
		probe.DownloadFolder = config.FinishedFolder()
	}
	if labels, err := d.Client.Labels(ctx); err == nil {
		probe.Categories = labels
	} else {
		probe.Notes = append(probe.Notes, "Its Label plugin is not enabled: enable it, or downloads cannot be sent under a category.")
	}
	return probe, nil
}
