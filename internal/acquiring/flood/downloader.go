package flood

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is Flood as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "flood", Label: "Flood", Protocol: download.ProtocolTorrent,
		AddressPlaceholder: "http://flood:3000", UserName: true, SecretLabel: "Password",
		KeepsCompleted: true, Categories: true,
	}
}

// ErrNotConnected is Flood reachable but not connected to its torrent client.
var ErrNotConnected = errors.New("flood: Flood is not connected to its torrent client")

// Downloader is Flood's part of download.TorrentClient; download.NewTorrent supplies the rest. Flood names hashes in
// upper case; Solarr's references are lower case.
type Downloader struct{ Client Client }

var _ download.TorrentClient = Downloader{}

// AddTorrentFile sends the torrent file, tagged with its category.
func (d Downloader) AddTorrentFile(ctx context.Context, hash string, request download.AddRequest) (string, error) {
	if err := d.Client.AddFile(ctx, base64.StdEncoding.EncodeToString(request.File), request.Category); err != nil {
		return "", err
	}
	return hash, nil
}

// AddMagnet sends a magnet link, tagged with its category.
func (d Downloader) AddMagnet(ctx context.Context, hash, magnet string, request download.AddRequest) (string, error) {
	if err := d.Client.AddURL(ctx, magnet, request.Category); err != nil {
		return "", err
	}
	return hash, nil
}

// Downloads answers every torrent, as Sonarr reads Flood's status list.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	torrents, err := d.Client.Torrents(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(torrents, func(i, j int) bool { return torrents[i].Hash < torrents[j].Hash })
	downloads := make([]download.Download, len(torrents))
	for i, torrent := range torrents {
		category := ""
		if len(torrent.Tags) > 0 {
			category = torrent.Tags[0]
		}
		downloads[i] = download.Download{
			ClientRef: strings.ToLower(torrent.Hash), Name: torrent.Name, Category: category, State: stateOf(torrent.Status),
			SizeBytes: torrent.SizeBytes, SizeLeftBytes: max(torrent.SizeBytes-torrent.BytesDone, 0),
			Ratio: torrent.Ratio, SpeedBytesPerSec: torrent.DownRate,
		}
		switch downloads[i].State {
		case download.StateCompleted:
			downloads[i].Path = download.TorrentPath(torrent.Directory, torrent.Name)
		case download.StateFailed:
			downloads[i].Error = "Flood reports " + torrent.Message
		}
	}
	return downloads, nil
}

func stateOf(status []string) download.State {
	switch {
	case slices.Contains(status, "seeding"), slices.Contains(status, "complete"):
		return download.StateCompleted
	case slices.Contains(status, "error"):
		return download.StateFailed
	case slices.Contains(status, "checking"):
		return download.StateProcessing
	case slices.Contains(status, "stopped"):
		return download.StatePaused
	case slices.Contains(status, "downloading"):
		return download.StateDownloading
	}
	return download.StateQueued
}

// Remove deletes one torrent; deleteFiles also deletes its data.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return d.Client.Delete(ctx, []string{strings.ToUpper(clientRef)}, deleteFiles)
}

// Pause stops the named torrents only.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.Client.SetStarted(ctx, upper(clientRefs), false)
}

// Resume starts the named torrents only.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.Client.SetStarted(ctx, upper(clientRefs), true)
}

func upper(refs []string) []string {
	hashes := make([]string, len(refs))
	for i, ref := range refs {
		hashes[i] = strings.ToUpper(ref)
	}
	return hashes
}

// Probe proves the address, the login and that Flood reaches its own client; a tag needs no creating.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	connected, err := d.Client.ConnectionTest(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	if !connected {
		return download.Probe{}, ErrNotConnected
	}
	probe := download.Probe{Version: "Flood", CreatesCategories: true}
	probe.DownloadFolder, _ = d.Client.Settings(ctx)
	return probe, nil
}
