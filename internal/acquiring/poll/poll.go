// Package poll follows every download Solarr sent until it is imported and cleaned up (section
// 10.3, 10.9): by client reference, never by path, and never fighting what a person did in the
// client. What importing and searching mean is the media type's business, supplied as functions.
package poll

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// JobType is one poll of every download client.
const JobType = jobs.Type("downloads.poll")

// Definition runs one poll at a time; the next scheduled poll is its retry. The acquire pool, so a
// long scan or probe in maintenance never stops downloads moving (found 2026-09-28).
var Definition = jobs.Definition{Type: JobType, Pool: jobs.PoolAcquire, Mutating: true, MaxAttempts: 1}

// Acquisition states this package moves between (section 10.3).
const (
	StateQueued      = "QUEUED"
	StateDownloading = "DOWNLOADING"
	StateCompleted   = "COMPLETED"
	StateImporting   = "IMPORTING"
	StateImported    = "IMPORTED"
	StateCleaned     = "CLEANED"
	StateFailed      = "FAILED"
	StateCancelled   = "CANCELLED"
)

// failureMemory is how long an automatic blocklist entry lasts (section 10.11).
const failureMemory = 30 * 24 * time.Hour

// Mass disappearance: this many of a client's downloads gone at once, and at least half, is the
// client losing its queue, not a person removing downloads (section 10.9).
const massVanishFloor = 3

// Client is one configured download client as a poll needs it.
type Client struct {
	Name            string
	Client          download.Client
	RemoveCompleted bool
	// LocalPath maps a path the client reports to where Solarr sees it.
	LocalPath func(reported string) string
}

// Config is what a Job needs; every function is required.
type Config struct {
	Acquisitions library.Acquisitions
	Sessions     library.ImportSessions
	Blocklist    library.BlocklistStore
	FS           fsx.FS
	// Clients answers the enabled download clients by id.
	Clients func(ctx context.Context) (map[int64]Client, error)
	// DownloadFolders are the folders a reported path must lie in (ADR-14, section 10.9).
	DownloadFolders func(ctx context.Context) ([]string, error)
	// Import starts a targeted import of one download's folder (Invariant 17).
	Import func(ctx context.Context, acquisition library.Acquisition, localPath string) error
	// Search searches a desired item again after its download failed.
	Search func(ctx context.Context, desiredItemID int64) error
	// History records an imported or failed download for History and the album's page.
	History func(ctx context.Context, kind download.HistoryKind, acquisition library.Acquisition, detail string)
	// SendHeld is optional: after each poll, held releases go to clients that now have room (ADR-57).
	SendHeld func(ctx context.Context) error
	// Imported is optional: told once a download's files are in the library, e.g. to settle an upgrade.
	Imported func(ctx context.Context, acquisition library.Acquisition)
	Clock    clock.Clock
	Logger   *slog.Logger
}

// Job is one poll.
type Job struct {
	config Config
	mu     sync.Mutex
	// vanishLogged is when each client's mass loss was last reported: a poll runs every minute and the condition
	// outlasts it, so it is reported again only after vanishRepeat.
	vanishLogged map[string]time.Time
}

// vanishRepeat is how long a mass loss is left unmentioned after it was reported.
const vanishRepeat = 6 * time.Hour

// NewJob builds the handler.
func NewJob(config Config) *Job {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Job{config: config, vanishLogged: map[string]time.Time{}}
}

// Run polls every client with a download in flight and moves each download on.
func (j *Job) Run(ctx context.Context, _ jobs.Job) error {
	polled := j.poll(ctx)
	if j.config.SendHeld == nil {
		return polled
	}
	return errors.Join(polled, j.config.SendHeld(ctx))
}

func (j *Job) poll(ctx context.Context) error {
	active, err := j.config.Acquisitions.ListActive(ctx)
	if err != nil || len(active) == 0 {
		return err
	}
	clients, err := j.config.Clients(ctx)
	if err != nil {
		return err
	}
	byClient := make(map[int64][]library.Acquisition)
	for _, acquisition := range active {
		byClient[acquisition.DownloaderID] = append(byClient[acquisition.DownloaderID], acquisition)
	}
	var failures []error
	for clientID, acquisitions := range byClient {
		client, ok := clients[clientID]
		if !ok {
			continue // Disabled or removed: nothing to ask, nothing concluded.
		}
		if err := j.pollClient(ctx, client, acquisitions); err != nil {
			failures = append(failures, fmt.Errorf("download client %d: %w", clientID, err))
		}
	}
	return errors.Join(failures...)
}

func (j *Job) pollClient(ctx context.Context, client Client, acquisitions []library.Acquisition) error {
	reported, err := client.Client.Downloads(ctx, "")
	if err != nil {
		return err
	}
	byRef := make(map[string]download.Download, len(reported))
	for _, entry := range reported {
		byRef[entry.ClientRef] = entry
	}
	massVanish := j.massVanish(acquisitions, byRef)
	if massVanish {
		j.reportMassVanish(client, acquisitions, byRef)
	}
	var failures []error
	for _, acquisition := range acquisitions {
		entry, found := byRef[acquisition.DownloadClientRef]
		if err := j.advance(ctx, client, acquisition, entry, found, massVanish); err != nil {
			failures = append(failures, fmt.Errorf("acquisition %d: %w", acquisition.ID, err))
		}
	}
	return errors.Join(failures...)
}

// reportMassVanish says which downloads the client no longer has, once per vanishRepeat and again whenever a
// different set is missing.
func (j *Job) reportMassVanish(client Client, acquisitions []library.Acquisition, byRef map[string]download.Download) {
	var missing []string
	for _, acquisition := range acquisitions {
		if _, found := byRef[acquisition.DownloadClientRef]; !found && (acquisition.State == StateQueued || acquisition.State == StateDownloading) {
			missing = append(missing, strconv.FormatInt(acquisition.ID, 10)+" "+acquisition.CandidateTitle)
		}
	}
	slices.Sort(missing)
	key := client.Name + "\x00" + strings.Join(missing, "\x00")
	now := j.config.Clock.Now()
	j.mu.Lock()
	last, reported := j.vanishLogged[key]
	if !reported || now.Sub(last) >= vanishRepeat {
		j.vanishLogged = map[string]time.Time{key: now}
	}
	j.mu.Unlock()
	if reported && now.Sub(last) < vanishRepeat {
		return
	}
	j.config.Logger.Error("a download client no longer has most of Solarr's downloads; nothing is marked failed or "+
		"sent again until you look: remove the ones that are gone from Activity -> Queue",
		slog.String("client", client.Name), slog.Int("missing", len(missing)), slog.Any("downloads", missing))
}

func (j *Job) massVanish(acquisitions []library.Acquisition, byRef map[string]download.Download) bool {
	watched, missing := 0, 0
	for _, acquisition := range acquisitions {
		if acquisition.State != StateQueued && acquisition.State != StateDownloading {
			continue
		}
		watched++
		if _, found := byRef[acquisition.DownloadClientRef]; !found {
			missing++
		}
	}
	return missing >= massVanishFloor && missing*2 >= watched
}

func (j *Job) advance(
	ctx context.Context, client Client, acquisition library.Acquisition, entry download.Download, found, massVanish bool,
) error {
	now := j.config.Clock.Now().Unix()
	if acquisition.State == StateImporting {
		return j.followImport(ctx, client, acquisition)
	}
	if !found {
		if massVanish {
			return nil
		}
		// A person removed it: record that, never send it again on our own (section 10.9).
		return j.setFailed(ctx, acquisition, "removed from the download client")
	}
	if reused, err := j.checkFingerprint(ctx, acquisition, entry); err != nil || reused {
		return err
	}
	switch entry.State {
	case download.StateCompleted:
		return j.completed(ctx, client, acquisition, entry)
	case download.StateFailed:
		return j.failed(ctx, client, acquisition, library.BlocklistDownloadFailed, entry.Error)
	case download.StateDownloading, download.StateProcessing:
		if acquisition.State == StateQueued {
			return j.config.Acquisitions.SetState(ctx, acquisition.ID, StateDownloading, "", now)
		}
	}
	return nil
}

// checkFingerprint records the client's own name and size when a download is first seen; a
// reference that later names another download is a different download (section 10.9).
func (j *Job) checkFingerprint(ctx context.Context, acquisition library.Acquisition, entry download.Download) (bool, error) {
	if entry.Name == "" {
		return false, nil // the client does not know the name yet
	}
	fingerprint := "client:" + entry.Name
	now := j.config.Clock.Now().Unix()
	if !strings.HasPrefix(acquisition.ClientFingerprint, "client:") {
		return false, j.config.Acquisitions.SetFingerprint(ctx, acquisition.ID, fingerprint, now)
	}
	if acquisition.ClientFingerprint == fingerprint {
		return false, nil
	}
	return true, j.setFailed(ctx, acquisition, "the download client reused this reference for another download")
}

// setFailed is the one way a download becomes FAILED, so every failure reaches History.
func (j *Job) setFailed(ctx context.Context, acquisition library.Acquisition, detail string) error {
	if err := j.config.Acquisitions.SetState(ctx, acquisition.ID, StateFailed, detail, j.config.Clock.Now().Unix()); err != nil {
		return err
	}
	j.config.History(ctx, download.HistoryFailed, acquisition, detail)
	return nil
}

func (j *Job) completed(ctx context.Context, client Client, acquisition library.Acquisition, entry download.Download) error {
	now := j.config.Clock.Now().Unix()
	localPath := entry.Path
	if client.LocalPath != nil {
		localPath = client.LocalPath(entry.Path)
	}
	if err := j.config.Acquisitions.SetPath(ctx, acquisition.ID, localPath, now); err != nil {
		return err
	}
	if problem := j.checkPath(ctx, localPath); problem != "" {
		// Reported and kept: the next poll tries again once the folders or the mapping are right.
		return j.config.Acquisitions.SetState(ctx, acquisition.ID, StateCompleted, problem, now)
	}
	acquisition.DownloadDir = localPath
	if err := j.config.Import(ctx, acquisition, localPath); err != nil {
		return j.config.Acquisitions.SetState(ctx, acquisition.ID, StateCompleted, "import could not start: "+err.Error(), now)
	}
	return j.config.Acquisitions.SetState(ctx, acquisition.ID, StateImporting, "", now)
}

// checkPath refuses a path outside every download folder or that does not exist (ADR-14): a path
// the client reports is evidence, not truth, and never a reason to walk anywhere else.
func (j *Job) checkPath(ctx context.Context, localPath string) string {
	if localPath == "" {
		return "the download client reported no folder for this download"
	}
	folders, err := j.config.DownloadFolders(ctx)
	if err != nil {
		return "download folders could not be read: " + err.Error()
	}
	clean := path.Clean(localPath)
	inside := false
	for _, folder := range folders {
		root := path.Clean(folder)
		if clean == root || strings.HasPrefix(clean, root+"/") {
			inside = true
			break
		}
	}
	if !inside {
		return fmt.Sprintf("%s is outside every download folder; add the folder, or a remote path mapping on the download client", localPath)
	}
	if _, err := j.config.FS.Stat(clean); err != nil {
		return fmt.Sprintf("%s cannot be read here: %v", localPath, err)
	}
	return ""
}

// followImport moves a download on once its import finishes: imported, waiting for a person, or failed.
func (j *Job) followImport(ctx context.Context, client Client, acquisition library.Acquisition) error {
	session, err := j.config.Sessions.LatestForAcquisition(ctx, acquisition.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Not started yet: the queue has it.
	}
	if err != nil {
		return err
	}
	if session.State != library.ImportDone && session.State != library.ImportFailed {
		return nil
	}
	units, err := j.config.Sessions.ListUnits(ctx, session.ID)
	if err != nil {
		return err
	}
	// Placed, not merely decided: a committed unit with no root to go to brought nothing in.
	placed, waiting := 0, 0
	for _, unit := range units {
		switch {
		case unit.WorkID != nil:
			placed++
		case unit.Decision == library.UnitQueue:
			waiting++
		}
	}
	now := j.config.Clock.Now().Unix()
	switch {
	case placed > 0:
		if err := j.config.Acquisitions.SetState(ctx, acquisition.ID, StateImported, "", now); err != nil {
			return err
		}
		acquisition.State = StateImported
		j.config.History(ctx, download.HistoryImported, acquisition, "")
		if j.config.Imported != nil {
			j.config.Imported(ctx, acquisition)
		}
		return j.cleanUp(ctx, client, acquisition)
	case waiting > 0:
		return j.config.Acquisitions.SetState(ctx, acquisition.ID, StateImporting, WaitingForReview, now)
	default:
		reason := "the import found nothing it could use"
		if session.Error != "" {
			reason = session.Error
		}
		return j.failed(ctx, client, acquisition, library.BlocklistImportFailed, reason)
	}
}

// WaitingForReview is the note of a download whose import is held for a person: nothing is moving, so it may be removed.
const WaitingForReview = "waiting for you to identify it on the Import page"

// cleanUp removes an imported download from the client, files included, when the client is set
// to (answered 2026-09-24); the import has already verified the library copy (Invariant 18).
func (j *Job) cleanUp(ctx context.Context, client Client, acquisition library.Acquisition) error {
	if !client.RemoveCompleted {
		return nil
	}
	if err := client.Client.Remove(ctx, acquisition.DownloadClientRef, true); err != nil {
		return err
	}
	return j.config.Acquisitions.SetState(ctx, acquisition.ID, StateCleaned, "", j.config.Clock.Now().Unix())
}

// failed blocklists the release for 30 days, removes it from the client and searches again
// (answered 2026-09-24, Lidarr's "Redownload failed").
func (j *Job) failed(
	ctx context.Context, client Client, acquisition library.Acquisition, reason library.BlocklistReason, detail string,
) error {
	now := j.config.Clock.Now()
	if acquisition.CandidateGUID != "" || acquisition.ContentHash != "" {
		expires := now.Add(failureMemory).Unix()
		if _, err := j.config.Blocklist.Create(ctx, library.NewBlocklistEntry{
			IndexerID: acquisition.IndexerID, GUID: acquisition.CandidateGUID, ContentHash: acquisition.ContentHash,
			Title:         acquisition.CandidateTitle,
			DesiredItemID: acquisition.DesiredItemID, Reason: reason, Source: library.BlocklistSourceAuto,
			ExpiresAt: &expires, CreatedAt: now.Unix(),
		}); err != nil {
			return err
		}
	}
	if err := client.Client.Remove(ctx, acquisition.DownloadClientRef, true); err != nil {
		j.config.Logger.Warn("failed download could not be removed from the client",
			slog.Int64("acquisition_id", acquisition.ID), slog.Any("error", err))
	}
	if err := j.setFailed(ctx, acquisition, detail); err != nil {
		return err
	}
	if acquisition.DesiredItemID != nil {
		if err := j.config.Search(ctx, *acquisition.DesiredItemID); err != nil {
			j.config.Logger.Warn("search after a failed download did not run",
				slog.Int64("desired_item_id", *acquisition.DesiredItemID), slog.Any("error", err))
		}
	}
	return nil
}
