package slskd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is slskd as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "slskd", Label: "slskd (Soulseek)", Protocol: download.ProtocolDDL,
		AddressPlaceholder: "http://slskd:5030", SecretLabel: "API key",
	}
}

// Downloader is slskd's part of download.DirectClient; download.NewDirect supplies the rest. A download is one peer's
// folder: its reference is the peer and the folder, and slskd saves it below its downloads directory under the
// folder's own name.
type Downloader struct{ Client Client }

var _ download.DirectClient = Downloader{}

// refSeparator splits a reference into the peer, path-escaped, and the peer's folder.
const refSeparator = "|"

func reference(username, directory string) string {
	return url.PathEscape(username) + refSeparator + directory
}

func parseReference(clientRef string) (username, directory string, err error) {
	escaped, directory, found := strings.Cut(clientRef, refSeparator)
	username, unescapeErr := url.PathUnescape(escaped)
	if !found || unescapeErr != nil || username == "" {
		return "", "", fmt.Errorf("slskd: %q is not a reference Solarr made", clientRef)
	}
	return username, directory, nil
}

// folderOf is the folder a Soulseek path lies in, as the peer names it ("@@music\Artist\Album").
func folderOf(path string) string {
	cut := strings.LastIndexAny(path, `\/`)
	if cut < 0 {
		return ""
	}
	return path[:cut]
}

// lastSegment is the folder's own name, which slskd saves the files under.
func lastSegment(directory string) string {
	return directory[strings.LastIndexAny(directory, `\/`)+1:]
}

// ErrSpansFolders is a direct release whose files lie in more than one of the peer's folders.
var ErrSpansFolders = errors.New("slskd: the release's files lie in more than one folder")

// AddDirect asks the peer for the release's files, which must share one folder.
func (d Downloader) AddDirect(ctx context.Context, release download.DirectRelease, _ download.AddRequest) (string, error) {
	directory := folderOf(release.Files[0].Path)
	files := make([]FileRequest, len(release.Files))
	for i, file := range release.Files {
		if folderOf(file.Path) != directory {
			return "", ErrSpansFolders
		}
		files[i] = FileRequest{Filename: file.Path, Size: file.Size}
	}
	if err := d.Client.Enqueue(ctx, release.Source, files); err != nil {
		return "", fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	return reference(release.Source, directory), nil
}

// Downloads answers one download per peer's folder.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	users, err := d.Client.Downloads(ctx)
	if err != nil {
		return nil, err
	}
	root, err := d.Client.DownloadsDirectory(ctx)
	if err != nil {
		return nil, err
	}
	var downloads []download.Download
	for _, user := range users {
		for _, directory := range user.Directories {
			entry := download.Download{
				ClientRef: reference(user.Username, directory.Directory), Name: lastSegment(directory.Directory),
				State: stateOf(directory.Files),
			}
			for _, file := range directory.Files {
				entry.SizeBytes += file.Size
				entry.SizeLeftBytes += max(file.BytesRemaining, 0)
				entry.SpeedBytesPerSec += int64(file.AverageSpeed)
				if entry.Error == "" && isFailed(file.State) {
					entry.Error = "slskd reports " + file.State
					if file.Exception != "" {
						entry.Error += ": " + file.Exception
					}
				}
			}
			if entry.State == download.StateCompleted {
				entry.Path = strings.TrimRight(root, "/") + "/" + entry.Name
			}
			downloads = append(downloads, entry)
		}
	}
	return downloads, nil
}

// slskd's states are "Completed, Succeeded", "Completed, Errored", "Queued, Remotely", "InProgress" and so on.
func isFailed(state string) bool {
	return strings.HasPrefix(state, "Completed") && !strings.Contains(state, "Succeeded")
}

func stateOf(files []Transfer) download.State {
	succeeded, running := 0, false
	for _, file := range files {
		switch {
		case isFailed(file.State):
			return download.StateFailed
		case strings.Contains(file.State, "Succeeded"):
			succeeded++
		case strings.HasPrefix(file.State, "InProgress"), strings.HasPrefix(file.State, "Initializing"):
			running = true
		}
	}
	switch {
	case len(files) > 0 && succeeded == len(files):
		return download.StateCompleted
	case running:
		return download.StateDownloading
	}
	return download.StateQueued
}

// Remove cancels and forgets every transfer of the folder; deleteFiles also deletes what arrived.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	username, directory, err := parseReference(clientRef)
	if err != nil {
		return err
	}
	users, err := d.Client.Downloads(ctx)
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.Username != username {
			continue
		}
		for _, folder := range user.Directories {
			if folder.Directory != directory {
				continue
			}
			for _, file := range folder.Files {
				if err := d.Client.RemoveTransfer(ctx, username, file.ID); err != nil {
					return err
				}
			}
		}
	}
	if deleteFiles {
		return d.Client.DeleteDirectory(ctx, lastSegment(directory))
	}
	return nil
}

// Pause is not something Soulseek transfers can do.
func (d Downloader) Pause(context.Context, []string) error { return download.ErrNotSupported }

// Resume is not something Soulseek transfers can do.
func (d Downloader) Resume(context.Context, []string) error { return download.ErrNotSupported }

// Probe proves the address and the key, and reads where finished downloads go.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	version, err := d.Client.Version(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: "slskd " + version}
	probe.DownloadFolder, _ = d.Client.DownloadsDirectory(ctx)
	return probe, nil
}
