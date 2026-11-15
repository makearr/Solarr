package blackhole

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// TorrentDefinition is Sonarr's Torrent Blackhole as the settings screen offers it.
func TorrentDefinition() download.Definition {
	fields := folderFields("Torrent folder", "Where Solarr writes each .torrent file, as Solarr sees it")
	fields = append(fields, download.Field{
		Key: download.FieldSaveMagnetFiles, Kind: download.FieldBool, Label: "Save magnet links",
		Help: "Write a magnet link to a .magnet file; off, a release that is only a magnet link is refused",
	})
	return download.Definition{
		Impl: "torrentblackhole", Label: "Torrent Blackhole", Protocol: download.ProtocolTorrent, KeepsCompleted: true,
		Fields: fields,
	}
}

// magnetExtension is what a saved magnet link is named with, Sonarr's default.
const magnetExtension = ".magnet"

// Torrent is the Torrent Blackhole's part of download.TorrentClient; download.NewTorrent supplies the rest. A torrent's
// reference is the name its content is saved under, which is what appears in the watch folder.
type Torrent struct {
	Folders
	SaveMagnetFiles bool
}

var _ download.TorrentClient = Torrent{}

// AddTorrentFile writes the torrent file into the drop folder.
func (t Torrent) AddTorrentFile(_ context.Context, _ string, request download.AddRequest) (string, error) {
	name, key, err := t.reference(request)
	if err != nil {
		return "", err
	}
	if err := t.write(cleanName(name)+".torrent", request.File); err != nil {
		return "", err
	}
	return key, nil
}

// AddMagnet writes the magnet link into the drop folder, when the blackhole is set to.
func (t Torrent) AddMagnet(_ context.Context, _, magnet string, request download.AddRequest) (string, error) {
	if !t.SaveMagnetFiles {
		return "", fmt.Errorf("%w: the blackhole saves no magnet links", download.ErrRefused)
	}
	name, key, err := t.reference(request)
	if err != nil {
		return "", err
	}
	if err := t.write(cleanName(name)+magnetExtension, []byte(magnet)); err != nil {
		return "", err
	}
	return key, nil
}

// reference answers the name the torrent is written under and its refKey, refusing one already in the blackhole.
func (t Torrent) reference(request download.AddRequest) (name, key string, err error) {
	// The torrent's own name is what the client saves its content under, but it comes from the indexer: one that is not
	// a single plain file name ("../..", "a/b") is cleaned like a release name, or removing the download would reach
	// outside the watch folder.
	name = cleanName(request.Name)
	if own := download.TorrentName(request.File); own != "" {
		if download.PlainName(own) {
			name = own
		} else if cleaned := cleanName(own); cleaned != "" {
			name = cleaned
		}
	}
	if key = refKey(name); key == "" {
		return "", "", fmt.Errorf("blackhole: the release has no usable name")
	}
	return name, key, t.refuseTaken(key, false)
}

// Downloads answers what is in the watch folder, each under the refKey of its name.
func (t Torrent) Downloads(context.Context, string) ([]download.Download, error) {
	return t.downloads(func(found entry) string { return refKey(found.name) })
}

// Remove deletes one download and the torrent file still waiting for it, if any.
func (t Torrent) Remove(_ context.Context, clientRef string, deleteFiles bool) error {
	key := refKey(clientRef)
	if key == "" {
		return fmt.Errorf("blackhole: %q is not a download", clientRef)
	}
	watched, err := t.namesOf(t.Watch, key, false)
	if err != nil {
		return err
	}
	waiting, err := t.namesOf(t.Drop, key, true)
	if err != nil {
		return err
	}
	return t.remove(watched, waiting, deleteFiles)
}

// Pause is not something a blackhole can do.
func (t Torrent) Pause(context.Context, []string) error { return download.ErrNotSupported }

// Resume is not something a blackhole can do.
func (t Torrent) Resume(context.Context, []string) error { return download.ErrNotSupported }

// Probe proves both folders are there.
func (t Torrent) Probe(context.Context) (download.Probe, error) { return t.probe("Torrent Blackhole") }
