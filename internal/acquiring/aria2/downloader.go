package aria2

import (
	"context"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is aria2 as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "aria2", Label: "aria2", Protocol: download.ProtocolTorrent,
		AddressPlaceholder: "http://aria2:6800", SecretLabel: "RPC secret",
		Fields: []download.Field{{
			Key: download.FieldURLBase, Kind: download.FieldText, Label: "RPC path", Placeholder: DefaultURLBase,
			Help: "The JSON-RPC path below the address; empty is " + DefaultURLBase,
		}},
	}
}

// Downloader is aria2's part of download.TorrentClient; download.NewTorrent supplies the rest. aria2 has no
// categories, and never deletes a download's files itself.
type Downloader struct{ Client Client }

var (
	_ download.TorrentClient = Downloader{}
	_ download.TransferSpeed = Downloader{}
)

// AddTorrentFile sends the torrent file.
func (d Downloader) AddTorrentFile(ctx context.Context, hash string, request download.AddRequest) (string, error) {
	if _, err := d.Client.AddTorrent(ctx, request.File); err != nil {
		return "", fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	return hash, nil
}

// AddMagnet sends a magnet link.
func (d Downloader) AddMagnet(ctx context.Context, hash, magnet string, _ download.AddRequest) (string, error) {
	if _, err := d.Client.AddURI(ctx, magnet); err != nil {
		return "", fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	return hash, nil
}

// Downloads answers every torrent aria2 holds, by info hash; a magnet's metadata download shares its hash and is
// left out once the torrent it led to is there.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	statuses, err := d.Client.Downloads(ctx)
	if err != nil {
		return nil, err
	}
	var downloads []download.Download
	seen := map[string]int{}
	for _, status := range statuses {
		if status.InfoHash == "" {
			continue
		}
		total, done := Number(status.TotalLength), Number(status.CompletedLength)
		entry := download.Download{
			ClientRef: strings.ToLower(status.InfoHash), Name: status.BitTorrent.Info.Name, State: stateOf(status.Status, total, done),
			SizeBytes: total, SizeLeftBytes: max(total-done, 0), SpeedBytesPerSec: Number(status.DownloadSpeed),
		}
		switch entry.State {
		case download.StateCompleted:
			entry.Path = strings.TrimRight(status.Dir, "/") + "/" + entry.Name
		case download.StateFailed:
			entry.Error = "aria2 reports " + status.ErrorMessage
		}
		if at, found := seen[entry.ClientRef]; found {
			if entry.Name != "" {
				downloads[at] = entry
			}
			continue
		}
		seen[entry.ClientRef] = len(downloads)
		downloads = append(downloads, entry)
	}
	return downloads, nil
}

func stateOf(status string, total, done int64) download.State {
	switch status {
	case "active":
		if total > 0 && done == total {
			return download.StateCompleted
		}
		return download.StateDownloading
	case "waiting":
		return download.StateQueued
	case "paused":
		return download.StatePaused
	case "complete":
		return download.StateCompleted
	case "error", "removed":
		return download.StateFailed
	}
	return download.StateProcessing
}

// gids finds every gid aria2 holds for a torrent, its metadata download's included.
func (d Downloader) gids(ctx context.Context, clientRef string) ([]Status, error) {
	statuses, err := d.Client.Downloads(ctx)
	if err != nil {
		return nil, err
	}
	var found []Status
	for _, status := range statuses {
		if strings.EqualFold(status.InfoHash, clientRef) {
			found = append(found, status)
		}
	}
	return found, nil
}

// Remove stops one torrent and forgets it. aria2 never deletes files, so deleteFiles leaves them where they are.
func (d Downloader) Remove(ctx context.Context, clientRef string, _ bool) error {
	statuses, err := d.gids(ctx, clientRef)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		running := status.Status == "active" || status.Status == "waiting" || status.Status == "paused"
		if running {
			if err := d.Client.Act(ctx, "forceRemove", status.GID); err != nil {
				return err
			}
		}
		// A download just force-removed may not have stopped yet; its result is then forgotten by aria2 itself later.
		if err := d.Client.Act(ctx, "removeDownloadResult", status.GID); err != nil && !running {
			return err
		}
	}
	return nil
}

// Pause pauses the named torrents only.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.each(ctx, clientRefs, "forcePause", "active", "waiting")
}

// Resume resumes the named torrents only.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.each(ctx, clientRefs, "unpause", "paused")
}

func (d Downloader) each(ctx context.Context, clientRefs []string, method string, from ...string) error {
	for _, ref := range clientRefs {
		statuses, err := d.gids(ctx, ref)
		if err != nil {
			return err
		}
		for _, status := range statuses {
			for _, state := range from {
				if status.Status == state {
					if err := d.Client.Act(ctx, method, status.GID); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// TransferSpeed answers the overall download speed.
func (d Downloader) TransferSpeed(ctx context.Context) (int64, error) {
	return d.Client.DownloadSpeed(ctx)
}

// Probe proves the address and the secret, and reads where downloads go.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	version, err := d.Client.Version(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: "aria2 " + version}
	probe.DownloadFolder, _ = d.Client.Directory(ctx)
	probe.Notes = append(probe.Notes, "aria2 has no categories, and leaves a removed download's files in place.")
	return probe, nil
}
