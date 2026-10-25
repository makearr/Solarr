package nzbget

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// Definition is NZBGet as the settings screen offers it.
func Definition() download.Definition {
	return download.Definition{
		Impl: "nzbget", Label: "NZBGet", Protocol: download.ProtocolUsenet,
		AddressPlaceholder: "http://nzbget:6789", UserName: true, SecretLabel: "Password", Categories: true,
	}
}

// Downloader is NZBGet's part of download.UsenetClient; download.NewUsenet supplies the rest. A job's reference is its
// NZBID, which it keeps from the queue into the history.
type Downloader struct{ Client Client }

var _ download.UsenetClient = Downloader{}

// AddNZB uploads one NZB named after the release.
func (d Downloader) AddNZB(ctx context.Context, request download.AddRequest) (string, error) {
	id, err := d.Client.Append(ctx, request.Name+".nzb", request.File, request.Category)
	if err != nil {
		return "", err
	}
	if id <= 0 {
		return "", fmt.Errorf("nzbget: %w (it answered NZBID %d)", download.ErrRefused, id)
	}
	return strconv.FormatInt(id, 10), nil
}

// Downloads answers the queue and the history.
func (d Downloader) Downloads(ctx context.Context, _ string) ([]download.Download, error) {
	groups, err := d.Client.Groups(ctx)
	if err != nil {
		return nil, err
	}
	history, err := d.Client.History(ctx)
	if err != nil {
		return nil, err
	}
	downloads := make([]download.Download, 0, len(groups)+len(history))
	for _, group := range groups {
		downloads = append(downloads, download.Download{
			ClientRef: strconv.FormatInt(group.NZBID, 10), Name: group.NZBName, Category: group.Category,
			State: queueState(group), SizeBytes: size(group.FileSizeLo, group.FileSizeHi),
			SizeLeftBytes: size(group.RemainingSizeLo, group.RemainingSizeHi),
		})
	}
	for _, item := range history {
		if item.Kind != "" && item.Kind != "NZB" {
			continue
		}
		entry := download.Download{
			ClientRef: strconv.FormatInt(item.NZBID, 10), Name: item.Name, Category: item.Category,
			State: historyState(item.Status), SizeBytes: size(item.FileSizeLo, item.FileSizeHi),
		}
		switch entry.State {
		case download.StateCompleted:
			entry.Path = item.FinalDir
			if entry.Path == "" {
				entry.Path = item.DestDir
			}
		case download.StateFailed:
			entry.Error = "NZBGet reports " + item.Status
		}
		downloads = append(downloads, entry)
	}
	return downloads, nil
}

func queueState(group Group) download.State {
	switch group.Status {
	case "QUEUED", "FETCHING":
		return download.StateQueued
	case "PAUSED":
		return download.StatePaused
	case "DOWNLOADING":
		return download.StateDownloading
	}
	if size(group.RemainingSizeLo, group.RemainingSizeHi) > 0 && size(group.RemainingSizeLo, group.RemainingSizeHi) ==
		size(group.PausedSizeLo, group.PausedSizeHi) {
		return download.StatePaused
	}
	// PP_QUEUED, LOADING_PARS, VERIFYING_SOURCES, REPAIRING, UNPACKING, MOVING, EXECUTING_SCRIPT, PP_FINISHED.
	return download.StateProcessing
}

// historyState reads NZBGet's "SUCCESS/UNPACK"-shaped status: a script's warning still leaves the files usable.
func historyState(status string) download.State {
	kind, detail, _ := strings.Cut(status, "/")
	switch {
	case kind == "SUCCESS", kind == "WARNING" && detail == "SCRIPT":
		return download.StateCompleted
	}
	return download.StateFailed
}

// QueueStatus answers NZBGet's own totals.
func (d Downloader) QueueStatus(ctx context.Context) (download.QueueStatus, error) {
	status, err := d.Client.Status(ctx)
	if err != nil {
		return download.QueueStatus{}, err
	}
	groups, err := d.Client.Groups(ctx)
	if err != nil {
		return download.QueueStatus{}, err
	}
	return download.QueueStatus{
		Paused: status.DownloadPaused, SpeedBytesPerSec: status.DownloadRate, Items: len(groups),
		SizeLeftBytes: size(status.RemainingSizeLo, status.RemainingSizeHi),
	}, nil
}

// Remove deletes one job from the queue, or its entry from the history; deleteFiles also deletes what the queue had
// downloaded. NZBGet keeps the files of a job it finished, so those stay.
func (d Downloader) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	id, err := parseID(clientRef)
	if err != nil {
		return err
	}
	groups, err := d.Client.Groups(ctx)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if group.NZBID == id {
			command := "GroupDelete"
			if deleteFiles {
				command = "GroupFinalDelete"
			}
			return d.Client.EditQueue(ctx, command, []int64{id})
		}
	}
	command := "HistoryDelete"
	if deleteFiles {
		command = "HistoryFinalDelete"
	}
	return d.Client.EditQueue(ctx, command, []int64{id})
}

// Pause pauses the named jobs; the rest of the queue carries on.
func (d Downloader) Pause(ctx context.Context, clientRefs []string) error {
	return d.edit(ctx, "GroupPause", clientRefs)
}

// Resume resumes the named jobs.
func (d Downloader) Resume(ctx context.Context, clientRefs []string) error {
	return d.edit(ctx, "GroupResume", clientRefs)
}

func (d Downloader) edit(ctx context.Context, command string, clientRefs []string) error {
	ids := make([]int64, len(clientRefs))
	for i, ref := range clientRefs {
		id, err := parseID(ref)
		if err != nil {
			return err
		}
		ids[i] = id
	}
	return d.Client.EditQueue(ctx, command, ids)
}

// Probe proves the address and the login, and reads where finished jobs go and which categories NZBGet has.
func (d Downloader) Probe(ctx context.Context) (download.Probe, error) {
	version, err := d.Client.Version(ctx)
	if err != nil {
		return download.Probe{}, err
	}
	probe := download.Probe{Version: "NZBGet " + version}
	config, err := d.Client.Config(ctx)
	if err != nil {
		return probe, nil //nolint:nilerr // the version already proved the connection; the folder is extra
	}
	probe.DownloadFolder = config["DestDir"]
	probe.Categories = []string{}
	var names []string
	for key, value := range config {
		if strings.HasPrefix(key, "Category") && strings.HasSuffix(key, ".Name") {
			names = append(names, value)
		}
	}
	sort.Strings(names)
	probe.Categories = append(probe.Categories, names...)
	return probe, nil
}
