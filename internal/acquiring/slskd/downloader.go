package slskd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
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

var (
	_ download.DirectClient = Downloader{}
	_ download.OwnFiles     = Downloader{}
	_ download.Gatherer     = Downloader{}
)

// discsMarker ends the reference directory of an album sent as several disc folders: the parent folder, then this.
const discsMarker = `\*`

// gatherRoot is the folder below the downloads directory where a multi-disc download's folders are collected.
const gatherRoot = ".solarr"

// parentOfDiscs is the album folder a reference directory stands for, when it stands for all its disc folders.
func parentOfDiscs(directory string) (string, bool) { return strings.CutSuffix(directory, discsMarker) }

// discFolder is a folder named for one disc of an album.
func discFolder(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range []string{"cd", "disc", "disk"} {
		if rest, found := strings.CutPrefix(lower, prefix); found && strings.TrimLeft(strings.TrimSpace(rest), "0123456789") == "" {
			return true
		}
	}
	return false
}

// gatherName is the folder of one multi-disc download below gatherRoot; the same peer and album always give the same.
func gatherName(username, parent string) string {
	sum := sha256.Sum256([]byte(username + refSeparator + parent))
	return "d-" + hex.EncodeToString(sum[:8])
}

// holds says whether a folder the peer named belongs to the reference: it is the folder, or one disc of the album.
func (r ref) holds(directory string) bool {
	if parent, discs := parentOfDiscs(r.directory); discs {
		return folderOf(directory) == parent && discFolder(lastSegment(directory))
	}
	return directory == r.directory
}

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

// AddDirect asks the peer for the release's files, which must share one folder, or lie in the disc folders of one album.
func (d Downloader) AddDirect(ctx context.Context, release download.DirectRelease, _ download.AddRequest) (string, error) {
	directory := folderOf(release.Files[0].Path)
	files := make([]FileRequest, len(release.Files))
	var folders []string
	for i, file := range release.Files {
		folder := folderOf(file.Path)
		if !slices.Contains(folders, folder) {
			folders = append(folders, folder)
		}
		files[i] = FileRequest{Filename: file.Path, Size: file.Size}
	}
	if len(folders) > 1 {
		parent := folderOf(folders[0])
		for _, folder := range folders {
			if folderOf(folder) != parent || !discFolder(lastSegment(folder)) {
				return "", ErrSpansFolders
			}
		}
		directory = parent + discsMarker
	}
	for _, folder := range folders {
		if clash := d.clashWith(ctx, release, folder); clash != "" {
			return "", fmt.Errorf("%w: %s", download.ErrRefused, clash)
		}
	}
	answer, err := d.Client.Enqueue(ctx, release.Source, files)
	if err != nil {
		return "", fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	if len(answer.Failed) > 0 {
		// Half an album tracked as a whole one would be imported or blocklisted as if it were all there.
		for _, taken := range answer.Enqueued {
			_ = d.Client.RemoveTransfer(context.WithoutCancel(ctx), release.Source, taken.ID)
		}
		return "", fmt.Errorf("%w: the peer refused %d of %d files", download.ErrRefused, len(answer.Failed), len(files))
	}
	reference := ref{username: release.Source, directory: directory}
	if !release.WholeFolder {
		reference.file = lastSegment(release.Files[0].Path)
	}
	return reference.String(), nil
}

// clashWith says what another transfer already in slskd would share with the release: slskd saves a folder under its
// last name alone, so two peers' folders of one name, or two peers' file of one name, would mix in one place.
func (d Downloader) clashWith(ctx context.Context, release download.DirectRelease, directory string) string {
	users, err := d.Client.Downloads(ctx)
	if err != nil {
		return ""
	}
	folder := lastSegment(directory)
	for _, user := range users {
		for _, other := range user.Directories {
			if lastSegment(other.Directory) != folder || (user.Username == release.Source && other.Directory == directory) {
				continue
			}
			if release.WholeFolder {
				return fmt.Sprintf("slskd already holds a folder named %q from another download", folder)
			}
			for _, file := range other.Files {
				if lastSegment(file.Filename) == lastSegment(release.Files[0].Path) {
					return fmt.Sprintf("slskd already holds a file named %q in a folder %q from another download", lastSegment(file.Filename), folder)
				}
			}
		}
	}
	return ""
}

// OwnFiles answers the names of the files this download's transfers brought.
func (d Downloader) OwnFiles(ctx context.Context, clientRef string) ([]string, error) {
	reference, err := parseReference(clientRef)
	if err != nil {
		return nil, err
	}
	if _, discs := parentOfDiscs(reference.directory); discs {
		return nil, nil // gathered into a folder of its own: everything in it is this download's
	}
	users, err := d.Client.Downloads(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, user := range users {
		if user.Username != reference.username {
			continue
		}
		for _, folder := range user.Directories {
			if !reference.holds(folder.Directory) {
				continue
			}
			files := folder.Files
			if reference.file != "" {
				files = sameStem(files, reference.file)
			}
			for _, file := range files {
				names = append(names, lastSegment(file.Filename))
			}
		}
	}
	return names, nil
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
			// The names are the peer's: "..", "." or nothing would make the path the downloads folder or the one above.
			if whole.State == download.StateCompleted && download.PlainName(folder) {
				whole.Path = root + "/" + folder
			}
			downloads = append(downloads, whole)
			for _, file := range directory.Files {
				name := lastSegment(file.Filename)
				part := summarise(sameStem(directory.Files, name))
				part.ClientRef, part.Name, part.Part = ref{username: user.Username, directory: directory.Directory, file: name}.String(), name, true
				if part.State == download.StateCompleted && download.PlainName(folder) && download.PlainName(name) {
					part.Path = root + "/" + folder + "/" + name
				}
				downloads = append(downloads, part)
			}
		}
		downloads = append(downloads, albumsOfDiscs(user, root)...)
	}
	return downloads, nil
}

// albumsOfDiscs answers one download for each album the peer's disc folders make up, which is where a multi-disc release is tracked.
func albumsOfDiscs(user UserTransfers, root string) []download.Download {
	discs := map[string][]Transfer{}
	var parents []string
	for _, directory := range user.Directories {
		if parent := folderOf(directory.Directory); discFolder(lastSegment(directory.Directory)) {
			if _, seen := discs[parent]; !seen {
				parents = append(parents, parent)
			}
			discs[parent] = append(discs[parent], directory.Files...)
		}
	}
	var albums []download.Download
	for _, parent := range parents {
		count := 0
		for _, directory := range user.Directories {
			if folderOf(directory.Directory) == parent && discFolder(lastSegment(directory.Directory)) {
				count++
			}
		}
		if count < 2 {
			continue
		}
		whole := summarise(discs[parent])
		whole.ClientRef, whole.Name = ref{username: user.Username, directory: parent + discsMarker}.String(), lastSegment(parent)
		if whole.State == download.StateCompleted {
			whole.Path = root + "/" + gatherRoot + "/" + gatherName(user.Username, parent)
		}
		albums = append(albums, whole)
	}
	return albums
}

// Gather names where a finished multi-disc download's disc folders must be collected, and the folders slskd made for them.
func (d Downloader) Gather(ctx context.Context, clientRef string) (string, []string, error) {
	reference, err := parseReference(clientRef)
	if err != nil {
		return "", nil, err
	}
	parent, discs := parentOfDiscs(reference.directory)
	if !discs {
		return "", nil, nil
	}
	users, err := d.Client.Downloads(ctx)
	if err != nil {
		return "", nil, err
	}
	root, err := d.Client.DownloadsDirectory(ctx)
	if err != nil {
		return "", nil, err
	}
	root = strings.TrimRight(root, "/")
	var sources []string
	for _, user := range users {
		if user.Username != reference.username {
			continue
		}
		for _, folder := range user.Directories {
			if reference.holds(folder.Directory) && download.PlainName(lastSegment(folder.Directory)) {
				sources = append(sources, root+"/"+lastSegment(folder.Directory))
			}
		}
	}
	return root + "/" + gatherRoot + "/" + gatherName(reference.username, parent), sources, nil
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
			if !reference.holds(folder.Directory) {
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
				removed = append(removed, lastSegment(folder.Directory)+"/"+lastSegment(file.Filename))
			}
		}
	}
	if !deleteFiles {
		return nil
	}
	// Only the files this download brought: its folder is shared by name with other downloads, and may hold more.
	parent, discs := parentOfDiscs(reference.directory)
	for _, name := range removed {
		if err := d.Client.DeleteFile(ctx, name); err != nil {
			return err
		}
		if discs {
			if err := d.Client.DeleteFile(ctx, gatherRoot+"/"+gatherName(reference.username, parent)+"/"+name); err != nil {
				return err
			}
		}
	}
	if discs {
		// The gathering folder is this download's alone.
		return d.Client.DeleteDirectory(ctx, gatherRoot+"/"+gatherName(reference.username, parent))
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
