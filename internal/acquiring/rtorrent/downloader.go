package rtorrent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is rTorrent as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "rtorrent", Label: "rTorrent", Protocol: download.ProtocolTorrent,
		AddressPlaceholder: "http://rtorrent:8080", UserName: true, SecretLabel: "Password",
		KeepsCompleted: true, Categories: true,
		Fields: []download.Field{{
			Key: download.FieldURLBase, Kind: download.FieldText, Label: "URL path", Placeholder: DefaultURLBase,
			Help: "The XML-RPC path below the address: RPC2, or plugins/httprpc/action.php through ruTorrent",
		}},
	}
}

// Downloader is rTorrent's part of download.TorrentClient; download.NewTorrent supplies the rest. rTorrent names
// hashes in upper case; Solarr's references are lower case, as every other client's are.
type Downloader struct{ Client Client }

var (
	_ download.TorrentClient = Downloader{}
	_ download.TransferSpeed = Downloader{}
)

// AddTorrentFile loads and starts the torrent file under its label.
func (d Downloader) AddTorrentFile(ctx context.Context, hash string, request download.AddRequest) (string, error) {
	return refused(hash, d.Client.AddFile(ctx, request.File, request.Category))
}

// AddMagnet loads and starts a magnet link under its label.
func (d Downloader) AddMagnet(ctx context.Context, hash, magnet string, request download.AddRequest) (string, error) {
	return refused(hash, d.Client.AddMagnet(ctx, magnet, request.Category))
}

func refused(hash string, err error) (string, error) {
	var fault *FaultError
	if errors.As(err, &fault) {
		return "", fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	if err != nil {
		return "", err
	}
	return hash, nil
}

// Downloads answers every torrent, as Sonarr reads them: complete, else running, else stopped.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	torrents, err := d.Client.Torrents(ctx)
	if err != nil {
		return nil, err
	}
	downloads := make([]download.Download, len(torrents))
	for i, torrent := range torrents {
		state := download.StatePaused
		switch {
		case torrent.IsComplete:
			state = download.StateCompleted
		case torrent.IsActive:
			state = download.StateDownloading
		}
		downloads[i] = download.Download{
			ClientRef: strings.ToLower(torrent.Hash), Name: torrent.Name, Category: torrent.Label, State: state,
			SizeBytes: torrent.SizeBytes, SizeLeftBytes: torrent.LeftBytes, SpeedBytesPerSec: torrent.DownRate,
			Ratio: float64(torrent.RatioPermill) / 1000,
		}
		if state == download.StateCompleted {
			downloads[i].Path = torrent.BasePath
			if downloads[i].Path == "" {
				downloads[i].Path = download.TorrentPath(torrent.Directory, torrent.Name)
			}
		}
	}
	return downloads, nil
}

// Remove erases one torrent; deleteFiles asks ruTorrent's erasedata plugin to delete its data.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return d.Client.Erase(ctx, strings.ToUpper(clientRef), deleteFiles)
}

// Pause stops the named torrents only.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.each(ctx, clientRefs, false)
}

// Resume starts the named torrents only.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.each(ctx, clientRefs, true)
}

func (d Downloader) each(ctx context.Context, clientRefs []string, started bool) error {
	for _, ref := range clientRefs {
		if err := d.Client.SetStarted(ctx, strings.ToUpper(ref), started); err != nil {
			return err
		}
	}
	return nil
}

// TransferSpeed answers the overall download speed.
func (d Downloader) TransferSpeed(ctx context.Context) (int64, error) {
	return d.Client.DownloadRate(ctx)
}

// Probe proves the address and the login; a label is created by the first torrent sent under it.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	version, err := d.Client.Version(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: "rTorrent " + version, CreatesCategories: true}
	probe.DownloadFolder, _ = d.Client.DefaultDirectory(ctx)
	return probe, nil
}
