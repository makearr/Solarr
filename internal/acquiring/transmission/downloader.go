package transmission

import (
	"context"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is Transmission as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "transmission", Label: "Transmission", Protocol: download.ProtocolTorrent,
		AddressPlaceholder: "http://transmission:9091", UserName: true, SecretLabel: "Password",
		KeepsCompleted: true, Categories: true, Fields: []download.Field{urlBaseField()},
	}
}

// VuzeDefinition is Vuze, driven through its Transmission-compatible Web Remote plugin.
func VuzeDefinition() download.Definition {
	definition := Definition()
	definition.Impl, definition.Label, definition.AddressPlaceholder = "vuze", "Vuze", "http://vuze:9091"
	return definition
}

func urlBaseField() download.Field {
	return download.Field{
		Key: download.FieldURLBase, Kind: download.FieldText, Label: "URL base", Placeholder: DefaultURLBase,
		Help: "The path of the RPC below the address; empty is " + DefaultURLBase,
	}
}

// Downloader is Transmission's part of download.TorrentClient; download.NewTorrent supplies the rest. Transmission has
// no categories, so a category is a folder below its download folder, as Sonarr does it.
type Downloader struct {
	Client Client
	// Vuze reports a multi-file torrent's own folder as its download folder, a single file's parent otherwise.
	Vuze bool
}

var (
	_ download.TorrentClient = Downloader{}
	_ download.TransferSpeed = Downloader{}
)

// AddTorrentFile sends the torrent file.
func (d Downloader) AddTorrentFile(ctx context.Context, hash string, request download.AddRequest) (string, error) {
	return d.add(ctx, hash, AddRequest{File: request.File}, request.Category)
}

// AddMagnet sends a magnet link.
func (d Downloader) AddMagnet(ctx context.Context, hash, magnet string, request download.AddRequest) (string, error) {
	return d.add(ctx, hash, AddRequest{Magnet: magnet}, request.Category)
}

func (d Downloader) add(ctx context.Context, hash string, add AddRequest, category string) (string, error) {
	if category != "" {
		session, err := d.Client.Session(ctx)
		if err != nil {
			return "", err
		}
		add.DownloadDir = path.Join(session.DownloadDir, category)
	}
	if err := d.Client.Add(ctx, add); err != nil {
		return "", err
	}
	return hash, nil
}

// Downloads answers every torrent; a finished one is completed whether it seeds or is stopped.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	torrents, err := d.Client.Torrents(ctx, nil)
	if err != nil {
		return nil, err
	}
	downloads := make([]download.Download, len(torrents))
	for i, torrent := range torrents {
		downloads[i] = download.Download{
			ClientRef: strings.ToLower(torrent.HashString), Name: torrent.Name,
			State: stateOf(torrent), SizeBytes: torrent.TotalSize, SizeLeftBytes: torrent.LeftUntilDone,
			Ratio: torrent.UploadRatio, SeedingSeconds: torrent.SecondsSeeding, SpeedBytesPerSec: torrent.RateDownload,
		}
		switch downloads[i].State {
		case download.StateCompleted:
			downloads[i].Path = d.outputPath(torrent)
		case download.StateFailed:
			downloads[i].Error = torrent.ErrorString
		}
	}
	return downloads, nil
}

func (d Downloader) outputPath(torrent Torrent) string {
	if d.Vuze && torrent.FileCount > 1 {
		return torrent.DownloadDir
	}
	if d.Vuze {
		return download.TorrentPath(torrent.DownloadDir, torrent.Name)
	}
	return strings.TrimRight(torrent.DownloadDir, "/") + "/" + torrent.Name
}

// Transmission's torrent status numbers.
const (
	statusStopped = iota
	statusCheckPending
	statusChecking
	statusDownloadPending
	statusDownloading
	statusSeedPending
	statusSeeding
)

// errorLocal is a local error (disk full, files missing); 1 and 2 are tracker warnings and errors the torrent survives.
const errorLocal = 3

func stateOf(torrent Torrent) download.State {
	if torrent.Error == errorLocal {
		return download.StateFailed
	}
	switch torrent.Status {
	case statusStopped:
		if torrent.IsFinished || (torrent.LeftUntilDone == 0 && torrent.TotalSize > 0) {
			return download.StateCompleted
		}
		return download.StatePaused
	case statusCheckPending, statusChecking:
		return download.StateProcessing
	case statusDownloadPending:
		return download.StateQueued
	case statusDownloading:
		return download.StateDownloading
	case statusSeedPending, statusSeeding:
		return download.StateCompleted
	}
	return download.StateProcessing
}

// Remove deletes one torrent; deleteFiles also deletes its data.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return d.Client.Remove(ctx, []string{clientRef}, deleteFiles)
}

// Pause stops the named torrents only.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.Client.SetPaused(ctx, clientRefs, true)
}

// Resume starts the named torrents only.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.Client.SetPaused(ctx, clientRefs, false)
}

// TransferSpeed answers the overall download speed.
func (d Downloader) TransferSpeed(ctx context.Context) (int64, error) {
	return d.Client.DownloadSpeed(ctx)
}

// Probe proves the address and the login; a category becomes a folder, so there are none to check.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	session, err := d.Client.Session(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	name := "Transmission"
	if d.Vuze {
		name = "Vuze"
	}
	return download.Probe{
		Version: name + " " + session.Version, DownloadFolder: session.DownloadDir,
		Notes: []string{"Each category is a folder below that one."},
	}, nil
}
