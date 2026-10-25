package utorrent

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is µTorrent as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "utorrent", Label: "µTorrent", Protocol: download.ProtocolTorrent,
		AddressPlaceholder: "http://utorrent:8080", UserName: true, SecretLabel: "Password",
		KeepsCompleted: true, Categories: true,
	}
}

// Downloader is µTorrent's part of download.TorrentClient; download.NewTorrent supplies the rest. µTorrent names
// hashes in upper case; Solarr's references are lower case.
type Downloader struct{ Client Client }

var _ download.TorrentClient = Downloader{}

// AddTorrentFile uploads the torrent file, then labels it.
func (d Downloader) AddTorrentFile(ctx context.Context, hash string, request download.AddRequest) (string, error) {
	if err := d.Client.AddFile(ctx, request.File); err != nil {
		return "", err
	}
	return d.label(ctx, hash, request.Category)
}

// AddMagnet adds a magnet link, then labels it.
func (d Downloader) AddMagnet(ctx context.Context, hash, magnet string, request download.AddRequest) (string, error) {
	if err := d.Client.AddURL(ctx, magnet); err != nil {
		return "", err
	}
	return d.label(ctx, hash, request.Category)
}

func (d Downloader) label(ctx context.Context, hash, category string) (string, error) {
	if category != "" {
		if err := d.Client.SetLabel(ctx, strings.ToUpper(hash), category); err != nil {
			return "", fmt.Errorf("utorrent: label the torrent: %w", err)
		}
	}
	return hash, nil
}

// µTorrent's status bits.
const (
	statusStarted = 1 << 0
	statusError   = 1 << 4
	statusPaused  = 1 << 5
	statusQueued  = 1 << 6
	statusCheck   = 1 << 1
)

// Downloads answers every torrent, as Sonarr reads µTorrent's status bits.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	list, err := d.Client.List(ctx)
	if err != nil {
		return nil, err
	}
	downloads := make([]download.Download, len(list.Torrents))
	for i, torrent := range list.Torrents {
		downloads[i] = download.Download{
			ClientRef: strings.ToLower(torrent.Hash), Name: torrent.Name, Category: torrent.Label, State: stateOf(torrent),
			SizeBytes: torrent.Size, SizeLeftBytes: torrent.Remaining, SpeedBytesPerSec: torrent.DownSpeed,
			Ratio: float64(torrent.RatioPerm) / 1000,
		}
		switch downloads[i].State {
		case download.StateCompleted:
			downloads[i].Path = download.TorrentPath(torrent.SavePath, torrent.Name)
		case download.StateFailed:
			downloads[i].Error = "µTorrent reports " + torrent.StatusText
		}
	}
	return downloads, nil
}

func stateOf(torrent Torrent) download.State {
	switch {
	case torrent.Status&statusError != 0:
		return download.StateFailed
	case torrent.Status&statusCheck != 0:
		return download.StateProcessing
	case torrent.Remaining == 0 && torrent.ProgressPerm == 1000:
		return download.StateCompleted
	case torrent.Status&statusPaused != 0:
		return download.StatePaused
	case torrent.Status&statusStarted != 0:
		return download.StateDownloading
	case torrent.Status&statusQueued != 0:
		return download.StateQueued
	}
	return download.StatePaused
}

// Remove deletes one torrent; deleteFiles also deletes its data.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	action := "remove"
	if deleteFiles {
		action = "removedata"
	}
	return d.Client.Act(ctx, action, []string{strings.ToUpper(clientRef)})
}

// Pause stops the named torrents only.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.Client.Act(ctx, "stop", upper(clientRefs))
}

// Resume starts the named torrents only.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.Client.Act(ctx, "start", upper(clientRefs))
}

func upper(refs []string) []string {
	hashes := make([]string, len(refs))
	for i, ref := range refs {
		hashes[i] = strings.ToUpper(ref)
	}
	return hashes
}

// Probe proves the address and the login, and reads the labels and where finished torrents go.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	list, err := d.Client.List(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: fmt.Sprintf("µTorrent build %d", list.Build), Categories: list.Labels, CreatesCategories: true}
	if probe.Categories == nil {
		probe.Categories = []string{}
	}
	if settings, err := d.Client.Settings(ctx); err == nil {
		probe.DownloadFolder = settings["dir_active_download"]
		if settings["dir_completed_download_flag"] == "true" && settings["dir_completed_download"] != "" {
			probe.DownloadFolder = settings["dir_completed_download"]
		}
	}
	return probe, nil
}
