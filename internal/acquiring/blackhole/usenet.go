package blackhole

import (
	"context"
	"fmt"

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
// reference is the release name's refKey: the NZB is named with the name and a usenet client names its folder after it,
// changing it as it likes.
type Usenet struct{ Folders }

var _ download.UsenetClient = Usenet{}

// AddNZB writes the NZB into the drop folder.
func (u Usenet) AddNZB(_ context.Context, request download.AddRequest) (string, error) {
	name := cleanName(request.Name)
	key := refKey(name)
	if key == "" {
		return "", fmt.Errorf("blackhole: the release has no usable name")
	}
	if err := u.refuseTaken(key, true); err != nil {
		return "", err
	}
	if err := u.write(name+".nzb", request.File); err != nil {
		return "", err
	}
	return key, nil
}

// Downloads answers what is in the watch folder; a loose file is known by its name without the extension.
func (u Usenet) Downloads(context.Context, string) ([]download.Download, error) {
	return u.downloads(func(found entry) string {
		if found.isDir {
			return refKey(found.name)
		}
		return refKey(withoutExtension(found.name))
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
func (u Usenet) Remove(_ context.Context, clientRef string, deleteFiles bool) error {
	key := refKey(clientRef)
	if key == "" {
		return fmt.Errorf("blackhole: %q is not a download", clientRef)
	}
	watched, err := u.namesOf(u.Watch, key, true)
	if err != nil {
		return err
	}
	waiting, err := u.namesOf(u.Drop, key, true)
	if err != nil {
		return err
	}
	return u.remove(watched, waiting, deleteFiles)
}

// Pause is not something a blackhole can do.
func (u Usenet) Pause(context.Context, []string) error { return download.ErrNotSupported }

// Resume is not something a blackhole can do.
func (u Usenet) Resume(context.Context, []string) error { return download.ErrNotSupported }

// Probe proves both folders are there.
func (u Usenet) Probe(context.Context) (download.Probe, error) { return u.probe("Usenet Blackhole") }
