package adapters

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/recovery"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// SystemHealth answers the health checks that need more than the settings
// snapshot apiserver already holds -- the connection lists, which are real
// rows rather than cached scalars.
type SystemHealth struct {
	Cache *settings.Cache
	// Acquisitions finds downloads waiting on a person; nil skips that check.
	Acquisitions interface {
		ListActive(ctx context.Context) ([]library.Acquisition, error)
	}
	// Vanished finds full scans that held a mass vanish back; nil skips that check.
	Vanished apiserver.VanishedFiles
	// Registry lets each media type add its own checks; nil adds none.
	Registry *media.Registry
	// AboveRating counts a library's works its maximum rating does not admit; nil skips that check.
	AboveRating func(ctx context.Context, categoryID int64, limit library.RatingLimit) (int64, error)
	// Paused lists the providers whose circuit is open; nil skips that check.
	Paused func() []httpx.BreakerSnapshot
	// Media reports the tools media files are read with that cannot run; nil skips that check.
	Media interface {
		Available(ctx context.Context) error
	}
	// Tasks lists the periodic tasks and their last runs; nil skips the failing-task check.
	Tasks func() []jobs.Task
	// TaskRuns reads the persisted last run of every task.
	TaskRuns interface {
		List(ctx context.Context) ([]jobs.ScheduleRun, error)
	}
	// Backups lists the backup files present; nil skips the stale-backup check.
	Backups interface {
		List(ctx context.Context) ([]apiserver.BackupFileRow, error)
	}
	// BackupInterval is the gap between two scheduled backups; zero when none is scheduled.
	BackupInterval func(now time.Time) time.Duration
	// ProbeFailures counts the files the probe leaves alone because ffprobe could not read them; nil skips that check.
	ProbeFailures func(ctx context.Context) (int64, error)
	// Dangling lists the library rows that name a setting that no longer exists; nil skips that check.
	Dangling func(ctx context.Context) ([]recovery.OrphanedReference, error)
	// Folders lists the download folders and ReadDir lists one, live: a finished folder Solarr cannot see fails every import; nil skips that check.
	Folders func(ctx context.Context) ([]settings.DownloadFolder, error)
	ReadDir func(path string) error
	// FollowsWithoutLibrary counts the enabled follows no library was recorded for; nil skips that check.
	FollowsWithoutLibrary func(ctx context.Context) (int64, error)
	// Now stamps the stale-backup check.
	Now func() time.Time
}

// Checks reports what is missing. Every probe is best-effort: a failure to
// read one list is reported as nothing rather than failing the whole screen,
// since a health page that will not render is worse than an incomplete one.
func (a SystemHealth) Checks(ctx context.Context) []apiserver.HealthCheck {
	var checks []apiserver.HealthCheck

	if indexers, err := a.Cache.Indexers(ctx); err == nil && !anyEnabledIndexer(indexers) {
		checks = append(checks, apiserver.HealthCheck{
			ID: "no_indexer", Severity: apiserver.HealthWarning,
			Message: "No indexer is enabled, so nothing can be searched for.",
			Link:    "/connections/indexers",
		})
	}
	if indexers, err := a.Cache.Indexers(ctx); err == nil {
		checks = append(checks, unreadableIndexers(indexers)...)
	}
	if a.searchingOffWithConnections(ctx) {
		checks = append(checks, apiserver.HealthCheck{
			ID: "searching_off", Severity: apiserver.HealthWarning,
			Message: "Automatic searching is off, so nothing wanted is searched for until you switch on \"Search for what is missing\" in the scheduler.",
			Link:    "/settings/scheduler",
		})
	}
	if downloaders, err := a.Cache.Downloaders(ctx); err == nil && !anyEnabledDownloader(downloaders) {
		checks = append(checks, apiserver.HealthCheck{
			ID: "no_downloader", Severity: apiserver.HealthWarning,
			Message: "No download client is enabled, so nothing found could be fetched.",
			Link:    "/connections/downloaders",
		})
	}
	checks = append(checks, a.waitingForReview(ctx)...)
	snapshot := a.Cache.Snapshot()
	checks = append(checks, a.vanishedHeld(ctx, snapshot)...)
	checks = append(checks, a.mediaChecks(snapshot)...)
	checks = append(checks, a.heldByRating(ctx, snapshot)...)
	checks = append(checks, a.pausedProviders(ctx)...)
	checks = append(checks, a.missingMediaTools(ctx)...)
	checks = append(checks, a.failingTasks(ctx)...)
	checks = append(checks, a.staleBackup(ctx)...)
	checks = append(checks, a.unreadableFiles(ctx)...)
	checks = append(checks, a.danglingReferences(ctx)...)
	checks = append(checks, a.followsWithoutLibrary(ctx)...)
	checks = append(checks, a.unreadableFinishedFolders(ctx)...)
	return append(checks, pausedForRoom(snapshot)...)
}

// unreadableIndexers names each enabled indexer whose capabilities were never read: searches skip it and say why.
func unreadableIndexers(indexers []settings.Indexer) []apiserver.HealthCheck {
	var checks []apiserver.HealthCheck
	for _, found := range indexers {
		if found.Enabled && found.CategoriesJSON == "" {
			checks = append(checks, apiserver.HealthCheck{
				ID: fmt.Sprintf("indexer_unreadable_%d", found.ID), Severity: apiserver.HealthWarning,
				Message: found.Name + " is not searched: its capabilities were never read (is the host reachable?).",
				Link:    "/connections/indexers",
			})
		}
	}
	return checks
}

// missingMediaTools says ffprobe or ffmpeg cannot run: no file can be read, so no import or scan can work.
func (a SystemHealth) missingMediaTools(ctx context.Context) []apiserver.HealthCheck {
	if a.Media == nil {
		return nil
	}
	err := a.Media.Available(ctx)
	if err == nil {
		return nil
	}
	return []apiserver.HealthCheck{{
		ID: "media_tools_missing", Severity: apiserver.HealthError,
		Message: "Solarr cannot run the tools that read media files, so nothing can be scanned or imported: " + err.Error(),
	}}
}

// unreadableFinishedFolders names each watched download folder Solarr cannot list now: the classic remote-path mistake, after which every
// import of that client's downloads fails.
func (a SystemHealth) unreadableFinishedFolders(ctx context.Context) []apiserver.HealthCheck {
	if a.Folders == nil || a.ReadDir == nil {
		return nil
	}
	folders, err := a.Folders(ctx)
	if err != nil {
		return nil
	}
	var checks []apiserver.HealthCheck
	for _, folder := range folders {
		if !folder.Watch {
			continue
		}
		if err := a.ReadDir(folder.Path); err != nil {
			checks = append(checks, apiserver.HealthCheck{
				ID: fmt.Sprintf("download_folder_unreadable_%d", folder.ID), Severity: apiserver.HealthError,
				Message: fmt.Sprintf("Solarr cannot read %s, where finished downloads are expected: mount it into the container or set the remote and local path of the download client.", folder.Path),
				Link:    "/storage/downloads",
			})
		}
	}
	return checks
}

// followsWithoutLibrary says how many follows have no library: they are skipped until one is chosen.
func (a SystemHealth) followsWithoutLibrary(ctx context.Context) []apiserver.HealthCheck {
	if a.FollowsWithoutLibrary == nil {
		return nil
	}
	count, err := a.FollowsWithoutLibrary(ctx)
	if err != nil || count == 0 {
		return nil
	}
	return []apiserver.HealthCheck{{
		ID: "follows_without_library", Severity: apiserver.HealthWarning,
		Message: fmt.Sprintf("%d follows have no library, so the daily check skips them. Open one and choose where its releases go.", count),
	}}
}

// danglingReferences names each setting that library rows still point at after it was removed; nothing repairs them.
func (a SystemHealth) danglingReferences(ctx context.Context) []apiserver.HealthCheck {
	if a.Dangling == nil {
		return nil
	}
	dangling, err := a.Dangling(ctx)
	if err != nil {
		return nil
	}
	var checks []apiserver.HealthCheck
	for _, reference := range dangling {
		checks = append(checks, apiserver.HealthCheck{
			ID: fmt.Sprintf("dangling_%s_%d", reference.Column, reference.ID), Severity: apiserver.HealthWarning,
			Message: fmt.Sprintf("%d rows of %s name %s %d, which no longer exists.", reference.Rows, reference.Table, strings.ReplaceAll(strings.TrimSuffix(reference.Column, "_id"), "_", " "), reference.ID),
		})
	}
	return checks
}

// unreadableFiles says how many media files ffprobe could not read; they are left alone and retried with growing waits.
func (a SystemHealth) unreadableFiles(ctx context.Context) []apiserver.HealthCheck {
	if a.ProbeFailures == nil {
		return nil
	}
	count, err := a.ProbeFailures(ctx)
	if err != nil || count == 0 {
		return nil
	}
	return []apiserver.HealthCheck{{
		ID: "unreadable_files", Severity: apiserver.HealthWarning,
		Message: fmt.Sprintf("%d media files could not be read (damaged, copy-protected or an unusual format) and are tried again only after growing waits. \"Read unreadable files again\" in Settings, General tries them now.", count),
		Link:    "/settings/general",
	}}
}

// staleBackup warns when the newest backup is older than two scheduled gaps; no backup yet says nothing,
// since a fresh install has none until its first scheduled run.
func (a SystemHealth) staleBackup(ctx context.Context) []apiserver.HealthCheck {
	if a.Backups == nil || a.BackupInterval == nil || a.Now == nil {
		return nil
	}
	now := a.Now()
	interval := a.BackupInterval(now)
	if interval <= 0 {
		return nil
	}
	rows, err := a.Backups.List(ctx)
	if err != nil {
		return nil
	}
	var newest time.Time
	for _, row := range rows {
		if !row.PreMigration && row.TakenAt.After(newest) {
			newest = row.TakenAt
		}
	}
	if newest.IsZero() || now.Sub(newest) <= 2*interval {
		return nil
	}
	return []apiserver.HealthCheck{{
		ID: "backup_stale", Severity: apiserver.HealthWarning,
		Message: fmt.Sprintf("The newest backup is %d days old, older than two scheduled backups.", int(now.Sub(newest).Hours()/24)),
		Link:    "/settings/backups",
	}}
}

// failingTasks names each periodic task whose last run ended in an error.
func (a SystemHealth) failingTasks(ctx context.Context) []apiserver.HealthCheck {
	if a.Tasks == nil || a.TaskRuns == nil {
		return nil
	}
	runs, err := a.TaskRuns.List(ctx)
	if err != nil {
		return nil
	}
	failed := make(map[string]string, len(runs))
	for _, run := range runs {
		if run.LastOutcome == "error" {
			failed[run.TaskKey] = run.LastError
		}
	}
	var checks []apiserver.HealthCheck
	for _, task := range a.Tasks() {
		reason, found := failed[task.Key]
		if !found {
			continue
		}
		message := fmt.Sprintf("The scheduled task %q failed on its last run", task.Name)
		if reason != "" {
			message += ": " + reason
		}
		checks = append(checks, apiserver.HealthCheck{
			ID: "task_failing_" + task.Key, Severity: apiserver.HealthWarning,
			Message: message + ".", Link: "/settings/scheduler",
		})
	}
	return checks
}

// workLevel is the key of the level a media type lists its works on ("series", "movies").
func (a SystemHealth) workLevel(mediaType string) string {
	if a.Registry != nil {
		for _, descriptor := range a.Registry.Descriptors() {
			if string(descriptor.Key) != mediaType {
				continue
			}
			for _, level := range descriptor.Levels {
				if level.Entity == model.LevelEntityWork {
					return level.Key
				}
			}
		}
	}
	return "works"
}

// heldByRating names each library holding works its maximum rating does not admit: they are never searched there.
func (a SystemHealth) heldByRating(ctx context.Context, snapshot *settings.Snapshot) []apiserver.HealthCheck {
	if a.AboveRating == nil {
		return nil
	}
	var checks []apiserver.HealthCheck
	for _, category := range snapshot.Categories() {
		if category.MaxAge == nil {
			continue
		}
		count, err := a.AboveRating(ctx, category.ID, library.RatingLimit{MaxAge: *category.MaxAge, AllowUnrated: category.AllowUnrated})
		if err != nil || count == 0 {
			continue
		}
		checks = append(checks, apiserver.HealthCheck{
			ID: fmt.Sprintf("above_rating_%d", category.ID), Severity: apiserver.HealthWarning,
			Message: fmt.Sprintf("%d in %s are above its maximum rating and are never searched; move them to another library or raise the limit.", count, category.Name),
			Link:    fmt.Sprintf("/library/%d/%s?above_rating=1", category.ID, a.workLevel(category.MediaType)),
		})
	}
	return checks
}

// mediaChecks asks each media type that can say what its libraries lack.
func (a SystemHealth) mediaChecks(snapshot *settings.Snapshot) []apiserver.HealthCheck {
	if a.Registry == nil {
		return nil
	}
	var checks []apiserver.HealthCheck
	for _, descriptor := range a.Registry.Descriptors() {
		module, ok := media.Capability[media.Healthy](a.Registry, descriptor.Key)
		if !ok {
			continue
		}
		libraries := len(snapshot.CategoryIDsOf(string(descriptor.Key)))
		for _, check := range module.HealthChecks(libraries) {
			checks = append(checks, apiserver.HealthCheck{ID: check.ID, Severity: check.Severity, Message: check.Message, Link: check.Link})
		}
	}
	return checks
}

// waitingForReview counts the downloads whose import is held: until someone identifies them, or until a root has room.
func (a SystemHealth) waitingForReview(ctx context.Context) []apiserver.HealthCheck {
	if a.Acquisitions == nil {
		return nil
	}
	active, err := a.Acquisitions.ListActive(ctx)
	if err != nil {
		return nil
	}
	waiting, noRoom, clientFault := 0, 0, 0
	for _, acquisition := range active {
		if acquisition.State == poll.StateDownloading && acquisition.LastError != "" &&
			acquisition.LastError != poll.WaitingForReview && acquisition.LastError != poll.WaitingForRoom {
			clientFault++
		}
		if acquisition.State != poll.StateImporting {
			continue
		}
		switch acquisition.LastError {
		case poll.WaitingForReview:
			waiting++
		case poll.WaitingForRoom:
			noRoom++
		}
	}
	var checks []apiserver.HealthCheck
	if clientFault > 0 {
		checks = append(checks, apiserver.HealthCheck{
			ID: "downloads_client_cannot_write", Severity: apiserver.HealthWarning,
			Message: fmt.Sprintf("%d %s waiting because %s download client cannot use its own files (a full disk or an unmounted share?): free the space or fix the mount, and %s on.",
				clientFault, plural(clientFault, "download is", "downloads are"), plural(clientFault, "its", "their"), plural(clientFault, "it carries", "they carry")),
			Link: "/activity/queue",
		})
	}
	if waiting > 0 {
		checks = append(checks, apiserver.HealthCheck{
			ID: "downloads_waiting_for_review", Severity: apiserver.HealthWarning,
			Message: fmt.Sprintf("%d %s waiting for you to identify %s on the Import page.",
				waiting, plural(waiting, "download is", "downloads are"), plural(waiting, "it", "them")),
			Link: "/storage/import",
		})
	}
	if noRoom > 0 {
		checks = append(checks, apiserver.HealthCheck{
			ID: "downloads_waiting_for_room", Severity: apiserver.HealthWarning,
			Message: fmt.Sprintf("%d %s waiting because no root folder of %s library can take %s: add one, free space or bring it online; %s imported then.",
				noRoom, plural(noRoom, "download is", "downloads are"), plural(noRoom, "its", "their"), plural(noRoom, "it", "them"), plural(noRoom, "it is", "they are")),
			Link: "/storage/folders",
		})
	}
	return checks
}

// vanishedHeld names each root whose last full scan found too many files gone to mark them missing on its own.
func (a SystemHealth) vanishedHeld(ctx context.Context, snapshot *settings.Snapshot) []apiserver.HealthCheck {
	if a.Vanished == nil {
		return nil
	}
	held, err := a.Vanished.Held(ctx)
	if err != nil {
		return nil
	}
	var checks []apiserver.HealthCheck
	for _, root := range snapshot.RootFolders() {
		count, found := held[root.ID]
		if !found {
			continue
		}
		checks = append(checks, apiserver.HealthCheck{
			ID: fmt.Sprintf("vanished_held_%d", root.ID), Severity: apiserver.HealthWarning,
			Message: fmt.Sprintf("The last full scan of %s found %d %s gone. Nothing is marked missing or downloaded "+
				"again until you look: if the disk is back, run a full scan; if you deleted them, accept it under Root folders.",
				root.Path, count, plural(int(count), "file", "files")),
			Link: "/storage/folders",
		})
	}
	return checks
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

// pausedForRoom names each library whose automatic downloads stop for want of free space
// (ADR-12; Invariant 29: every pause is visible, with its resume condition).
func pausedForRoom(snapshot *settings.Snapshot) []apiserver.HealthCheck {
	var checks []apiserver.HealthCheck
	for _, category := range snapshot.Categories() {
		if room, measured := snapshot.AcquisitionHeadroom(category.ID); measured && room <= 0 {
			checks = append(checks, apiserver.HealthCheck{
				ID: fmt.Sprintf("no_room_%d", category.ID), Severity: apiserver.HealthWarning,
				// No link: the fix is free disk space, and the reserve's page is advanced (2026-09-29 tier rule).
				Message: fmt.Sprintf("Automatic downloads for %s are paused: no root folder taking new files has "+
					"free space above its reserve. Free up space, or lower the reserve under Storage → Root folders; "+
					"your own grabs still go.", category.Name),
			})
		}
	}
	return checks
}

func anyEnabledIndexer(indexers []settings.Indexer) bool {
	for _, indexer := range indexers {
		if indexer.Enabled {
			return true
		}
	}
	return false
}

func anyEnabledDownloader(downloaders []settings.Downloader) bool {
	for _, downloader := range downloaders {
		if downloader.Enabled {
			return true
		}
	}
	return false
}

// pausedProviders names each provider the breaker has stopped asking, and says when its key was refused. An indexer or
// download client is named by its own connection, linked to its screen.
func (a SystemHealth) pausedProviders(ctx context.Context) []apiserver.HealthCheck {
	if a.Paused == nil {
		return nil
	}
	var checks []apiserver.HealthCheck
	for _, paused := range a.Paused() {
		name, link := a.pausedConnection(ctx, paused.Provider)
		if name == "" {
			continue
		}
		retryAt := paused.RetryAt
		reason := "it keeps failing"
		if paused.KeyRejected {
			reason = "its key was rejected, so check it"
		}
		checks = append(checks, apiserver.HealthCheck{
			ID: "provider_paused_" + paused.Provider, Severity: apiserver.HealthWarning,
			Message: fmt.Sprintf("%s is paused: %s.", name, reason),
			Link:    link,
			Until:   &retryAt,
		})
	}
	return checks
}

// pausedConnection is a paused provider's display name and the screen that fixes it; empty for a user's own *Arr or
// other service, which have screens of their own.
func (a SystemHealth) pausedConnection(ctx context.Context, provider string) (name, link string) {
	kind, idText, connection := strings.Cut(provider, ":")
	if !connection {
		return provider, "/settings/metadata"
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return "", ""
	}
	switch kind {
	case "indexer":
		indexers, err := a.Cache.Indexers(ctx)
		if err != nil {
			return "", ""
		}
		for _, indexer := range indexers {
			if indexer.ID == id {
				return "Indexer " + indexer.Name, "/connections/indexers"
			}
		}
	case "downloader":
		downloaders, err := a.Cache.Downloaders(ctx)
		if err != nil {
			return "", ""
		}
		for _, downloader := range downloaders {
			if downloader.ID == id {
				return "Download client " + downloader.Name, "/connections/downloaders"
			}
		}
	}
	return "", ""
}

// searchingOffWithConnections says a client and an indexer are ready while the search task is switched off.
func (a SystemHealth) searchingOffWithConnections(ctx context.Context) bool {
	if a.Cache.Snapshot().ScheduleEnabled("hunt") {
		return false
	}
	indexers, indexerErr := a.Cache.Indexers(ctx)
	downloaders, downloaderErr := a.Cache.Downloaders(ctx)
	return indexerErr == nil && downloaderErr == nil && anyEnabledIndexer(indexers) && anyEnabledDownloader(downloaders)
}
