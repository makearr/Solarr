package blackhole

import (
	"context"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// UsenetDefinition is Sonarr's Usenet Blackhole as the settings screen offers it.
func UsenetDefinition() download.Definition {
	return download.Definition{
		Impl: "usenetblackhole", Label: "Usenet Blackhole", Protocol: download.ProtocolUsenet,
		Fields: folderFields("NZB folder", "Where Solarr writes each .nzb file, as Solarr sees it"),
	}
}

// Usenet is the Usenet Blackhole's part of download.UsenetClient; download.NewUsenet supplies the rest. A job's
// reference is the release's name, which the NZB is named with and a usenet client names its folder after.
type Usenet struct{ Folders }

var _ download.UsenetClient = Usenet{}

// AddNZB writes the NZB into the drop folder.
func (u Usenet) AddNZB(_ context.Context, request download.AddRequest) (string, error) {
	ref := cleanName(request.Name)
	if err := u.write(ref+".nzb", request.File); err != nil {
		return "", err
	}
	return ref, nil
}

// Downloads answers what is in the watch folder; a loose file is known by its name without the extension.
func (u Usenet) Downloads(context.Context, string) ([]download.Download, error) {
	return u.downloads(func(found entry) string {
		if found.isDir {
			return found.name
		}
		return strings.TrimSuffix(found.name, path.Ext(found.name))
	})
}

// QueueStatus answers what is not finished yet; a blackhole cannot say how much is left of it.
func (u Usenet) QueueStatus(ctx context.Context) (download.QueueStatus, error) {
	downloads, err := u.Downloads(ctx, "")
	if err != nil {
		return download.QueueStatus{}, err
	}
	var status download.QueueStatus
	for _, one := range downloads {
		if one.State != download.StateCompleted {
			status.Items++
		}
	}
	return status, nil
}

// Remove deletes one job's files and the NZB still waiting for it, if any.
func (u Usenet) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	downloads, err := u.Downloads(ctx, "")
	if err != nil {
		return err
	}
	var names []string
	for _, one := range downloads {
		if one.ClientRef == clientRef {
			names = append(names, one.Name)
		}
	}
	return u.remove(names, []string{clientRef + ".nzb"}, deleteFiles)
}

// Pause is not something a blackhole can do.
func (u Usenet) Pause(context.Context, []string) error { return download.ErrNotSupported }

// Resume is not something a blackhole can do.
func (u Usenet) Resume(context.Context, []string) error { return download.ErrNotSupported }

// Probe proves both folders are there.
func (u Usenet) Probe(context.Context) (download.Probe, error) { return u.probe("Usenet Blackhole") }
