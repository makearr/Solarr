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
		AddressPlaceholder: "http://slskd:5030", SecretLabel: "API key", CompanionIndexer: "soulseek",
	}
}

// Downloader is slskd's part of download.DirectClient; download.NewDirect supplies the rest. A download is one peer's
// whole folder, or one file and what shares its name (a film and its subtitles): its reference is the peer, the folder
// and, for a file, the file's name. slskd saves either below its downloads directory, under the folder's own name.
type Downloader struct{ Client Client }

var _ download.DirectClient = Downloader{}

// refSeparator splits a reference into the peer, path-escaped, the peer's folder and, for a file, its name; neither a
// Soulseek path nor a file name can hold it.
const refSeparator = "|"

// ref is one parsed reference; file is empty for a whole folder.
type ref struct{ username, directory, file string }

func (r ref) String() string {
	text := url.PathEscape(r.username) + refSeparator + r.directory
	if r.file != "" {
		text += refSeparator + r.file
	}
	return text
}

func parseReference(clientRef string) (ref, error) {
	parts := strings.SplitN(clientRef, refSeparator, 3)
	username, err := url.PathUnescape(parts[0])
	if len(parts) < 2 || err != nil || username == "" {
		return ref{}, fmt.Errorf("slskd: %q is not a reference Solarr made", clientRef)
	}
	parsed := ref{username: username, directory: parts[1]}
	if len(parts) == 3 {
		parsed.file = parts[2]
	}
	return parsed, nil
}

// folderOf is the folder a Soulseek path lies in, as the peer names it ("@@music\Artist\Album").
func folderOf(path string) string {
	cut := strings.LastIndexAny(path, `\/`)
	if cut < 0 {
		return ""
	}
	return path[:cut]
}

// lastSegment is a path's own last name: a folder's, which slskd saves the files under, or a file's.
func lastSegment(path string) string {
	return path[strings.LastIndexAny(path, `\/`)+1:]
}

// stem is a file's name without its extension: a film and its subtitles share it.
func stem(name string) string {
	if dot := strings.LastIndex(name, "."); dot > 0 {
		name = name[:dot]
	}
	// "Film.en.srt" belongs with "Film.mkv".
	if dot := strings.LastIndex(name, "."); dot > 0 && len(name)-dot <= 4 {
		name = name[:dot]
	}
	return name
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
	reference := ref{username: release.Source, directory: directory}
	if !release.WholeFolder {
		reference.file = lastSegment(release.Files[0].Path)
	}
	return reference.String(), nil
}

// Downloads answers one download per peer's folder, and a part for each file with the files that share its name.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	users, err := d.Client.Downloads(ctx)
	if err != nil {
		return nil, err
	}
	root, err := d.Client.DownloadsDirectory(ctx)
	if err != nil {
		return nil, err
	}
	root = strings.TrimRight(root, "/")
	var downloads []download.Download
	for _, user := range users {
		for _, directory := range user.Directories {
			folder := lastSegment(directory.Directory)
			whole := summarise(directory.Files)
			whole.ClientRef, whole.Name = ref{username: user.Username, directory: directory.Directory}.String(), folder
			if whole.State == download.StateCompleted {
				whole.Path = root + "/" + folder
			}
			downloads = append(downloads, whole)
			for _, file := range directory.Files {
				name := lastSegment(file.Filename)
				part := summarise(sameStem(directory.Files, name))
				part.ClientRef, part.Name, part.Part = ref{username: user.Username, directory: directory.Directory, file: name}.String(), name, true
				if part.State == download.StateCompleted {
					part.Path = root + "/" + folder + "/" + name
				}
				downloads = append(downloads, part)
			}
		}
	}
	return downloads, nil
}

// sameStem are the transfers whose file shares name's stem.
func sameStem(files []Transfer, name string) []Transfer {
	var found []Transfer
	for _, file := range files {
		if stem(lastSegment(file.Filename)) == stem(name) {
			found = append(found, file)
		}
	}
	return found
}

// summarise is what some transfers add up to: their state, sizes, speed and first failure.
func summarise(files []Transfer) download.Download {
	entry := download.Download{State: stateOf(files)}
	for _, file := range files {
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
	return entry
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

// Remove cancels and forgets the download's transfers; deleteFiles also deletes what arrived.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	reference, err := parseReference(clientRef)
	if err != nil {
		return err
	}
	users, err := d.Client.Downloads(ctx)
	if err != nil {
		return err
	}
	var removed []string
	for _, user := range users {
		if user.Username != reference.username {
			continue
		}
		for _, folder := range user.Directories {
			if folder.Directory != reference.directory {
				continue
			}
			files := folder.Files
			if reference.file != "" {
				files = sameStem(files, reference.file)
			}
			for _, file := range files {
				if err := d.Client.RemoveTransfer(ctx, reference.username, file.ID); err != nil {
					return err
				}
				removed = append(removed, lastSegment(file.Filename))
			}
		}
	}
	if !deleteFiles {
		return nil
	}
	folder := lastSegment(reference.directory)
	if reference.file == "" {
		return d.Client.DeleteDirectory(ctx, folder)
	}
	for _, name := range removed {
		if err := d.Client.DeleteFile(ctx, folder+"/"+name); err != nil {
			return err
		}
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
