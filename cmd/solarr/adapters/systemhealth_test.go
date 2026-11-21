package adapters

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/recovery"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

type fakeActive []library.Acquisition

func (f fakeActive) ListActive(context.Context) ([]library.Acquisition, error) { return f, nil }

func TestSystemHealth_CountsOnlyDownloadsHeldForAPerson(t *testing.T) {
	health := SystemHealth{Acquisitions: fakeActive{
		{State: poll.StateImporting, LastError: poll.WaitingForReview},
		{State: poll.StateImporting, LastError: poll.WaitingForReview},
		{State: poll.StateImporting},
		{State: poll.StateDownloading, LastError: poll.WaitingForReview},
	}}
	checks := health.waitingForReview(t.Context())
	if len(checks) != 1 || !strings.HasPrefix(checks[0].Message, "2 downloads are waiting") || checks[0].Link != "/storage/import" {
		t.Fatalf("checks = %+v, want one warning for 2 downloads linking to the Import page", checks)
	}
	room := (SystemHealth{Acquisitions: fakeActive{{State: poll.StateImporting, LastError: poll.WaitingForRoom}}}).waitingForReview(t.Context())
	if len(room) != 1 || room[0].ID != "downloads_waiting_for_room" || room[0].Link != "/storage/folders" {
		t.Errorf("checks = %+v, want one warning for a download waiting for room, linking to the folders", room)
	}
	if none := (SystemHealth{Acquisitions: fakeActive{{State: poll.StateImporting}}}).waitingForReview(t.Context()); len(none) != 0 {
		t.Errorf("checks = %+v, want none", none)
	}
}

// A download kept DOWNLOADING with the client's own complaint is named; a waiting-for-review leftover is not (TRACK-10).
func TestSystemHealth_NamesDownloadsWhoseClientCannotUseItsFiles(t *testing.T) {
	health := SystemHealth{Acquisitions: fakeActive{
		{State: poll.StateDownloading, LastError: "qBittorrent reports missingFiles"},
		{State: poll.StateDownloading, LastError: "Deluge reports No space left"},
		{State: poll.StateDownloading},
		{State: poll.StateDownloading, LastError: poll.WaitingForReview},
	}}
	checks := health.waitingForReview(t.Context())
	if len(checks) != 1 || checks[0].ID != "downloads_client_cannot_write" || !strings.HasPrefix(checks[0].Message, "2 downloads are waiting") {
		t.Fatalf("checks = %+v, want one warning for 2 downloads", checks)
	}
}

func TestSystemHealth_NamesAPausedMetadataProviderAndSkipsAnUnknownConnection(t *testing.T) {
	retry := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	health := SystemHealth{Paused: func() []httpx.BreakerSnapshot {
		return []httpx.BreakerSnapshot{
			{Provider: "tmdb", RetryAt: retry, KeyRejected: true},
			{Provider: "indexer:abc", RetryAt: retry},
			{Provider: "arr:3", RetryAt: retry},
		}
	}}
	checks := health.pausedProviders(t.Context())
	want := []apiserver.HealthCheck{{
		ID: "provider_paused_tmdb", Severity: apiserver.HealthWarning,
		Message: "tmdb is paused: its key was rejected, so check it.", Link: "/settings/metadata", Until: &retry,
	}}
	if !reflect.DeepEqual(checks, want) {
		t.Fatalf("checks = %+v, want %+v", checks, want)
	}
}

type fakeTool struct{ err error }

func (f fakeTool) Available(context.Context) error { return f.err }

type fakeRuns []jobs.ScheduleRun

func (f fakeRuns) List(context.Context) ([]jobs.ScheduleRun, error) { return f, nil }

func TestSystemHealth_MissingMediaToolIsAnErrorAndRunnableToolsSayNothing(t *testing.T) {
	missing := SystemHealth{Media: fakeTool{err: errors.New("ffprobe not found")}}.missingMediaTools(t.Context())
	want := []apiserver.HealthCheck{{
		ID: "media_tools_missing", Severity: apiserver.HealthError,
		Message: "Solarr cannot run the tools that read media files, so nothing can be scanned or imported: ffprobe not found",
	}}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("checks = %+v, want %+v", missing, want)
	}
	if none := (SystemHealth{Media: fakeTool{}}).missingMediaTools(t.Context()); len(none) != 0 {
		t.Fatalf("checks = %+v, want none", none)
	}
}

func TestSystemHealth_NamesEachTaskWhoseLastRunFailed(t *testing.T) {
	health := SystemHealth{
		Tasks: func() []jobs.Task {
			return []jobs.Task{{Key: "hunt", Name: "Search"}, {Key: "rss", Name: "Feeds"}, {Key: "backup", Name: "Backup"}}
		},
		TaskRuns: fakeRuns{
			{TaskKey: "hunt", LastOutcome: "error", LastError: "queue is full"},
			{TaskKey: "rss", LastOutcome: "enqueued"},
			{TaskKey: "backup", LastOutcome: "error"},
		},
	}
	want := []apiserver.HealthCheck{
		{ID: "task_failing_hunt", Severity: apiserver.HealthWarning, Message: `The scheduled task "Search" failed on its last run: queue is full.`, Link: "/settings/scheduler"},
		{ID: "task_failing_backup", Severity: apiserver.HealthWarning, Message: `The scheduled task "Backup" failed on its last run.`, Link: "/settings/scheduler"},
	}
	if got := health.failingTasks(t.Context()); !reflect.DeepEqual(got, want) {
		t.Fatalf("checks = %+v, want %+v", got, want)
	}
}

type fakeBackups []apiserver.BackupFileRow

func (f fakeBackups) List(context.Context) ([]apiserver.BackupFileRow, error) { return f, nil }

func TestSystemHealth_StaleBackupWarnsPastTwoGapsAndIgnoresPreMigrationCopies(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	health := func(rows ...apiserver.BackupFileRow) SystemHealth {
		return SystemHealth{
			Backups: fakeBackups(rows), BackupInterval: func(time.Time) time.Duration { return week },
			Now: func() time.Time { return now },
		}
	}
	old := apiserver.BackupFileRow{TakenAt: now.Add(-15 * 24 * time.Hour)}
	recentPre := apiserver.BackupFileRow{TakenAt: now.Add(-time.Hour), PreMigration: true}
	want := []apiserver.HealthCheck{{
		ID: "backup_stale", Severity: apiserver.HealthWarning,
		Message: "The newest backup is 15 days old, older than two scheduled backups.", Link: "/settings/backups",
	}}
	if got := health(old, recentPre).staleBackup(t.Context()); !reflect.DeepEqual(got, want) {
		t.Fatalf("checks = %+v, want %+v", got, want)
	}
	if got := health(apiserver.BackupFileRow{TakenAt: now.Add(-13 * 24 * time.Hour)}).staleBackup(t.Context()); len(got) != 0 {
		t.Fatalf("checks = %+v, want none", got)
	}
	if got := health().staleBackup(t.Context()); len(got) != 0 {
		t.Fatalf("no backup yet: checks = %+v, want none", got)
	}
}

func TestUnreadableIndexers_NameEachEnabledIndexerWithoutCapabilities(t *testing.T) {
	checks := unreadableIndexers([]settings.Indexer{
		{ID: 1, Name: "Read", Enabled: true, CategoriesJSON: "{}"},
		{ID: 2, Name: "Dead", Enabled: true},
		{ID: 3, Name: "Off", Enabled: false},
	})
	if len(checks) != 1 || checks[0].ID != "indexer_unreadable_2" || !strings.Contains(checks[0].Message, "Dead") {
		t.Errorf("checks = %+v, want one for the enabled indexer without capabilities", checks)
	}
}

func TestUnreadableFiles_ReportsTheCountOnlyWhenThereAreSome(t *testing.T) {
	count := int64(0)
	health := SystemHealth{ProbeFailures: func(context.Context) (int64, error) { return count, nil }}
	if checks := health.unreadableFiles(t.Context()); len(checks) != 0 {
		t.Errorf("no failures: %+v, want none", checks)
	}
	count = 3
	checks := health.unreadableFiles(t.Context())
	if len(checks) != 1 || checks[0].ID != "unreadable_files" || !strings.Contains(checks[0].Message, "3 media files") {
		t.Errorf("checks = %+v, want one naming 3 files", checks)
	}
}

func TestSystemHealth_NamesASettingLibraryRowsStillPointAt(t *testing.T) {
	health := SystemHealth{Dangling: func(context.Context) ([]recovery.OrphanedReference, error) {
		return []recovery.OrphanedReference{{Table: "desired_profile_override", Column: "profile_id", ID: 7, Rows: 12}}, nil
	}}
	want := []apiserver.HealthCheck{{
		ID: "dangling_profile_id_7", Severity: apiserver.HealthWarning,
		Message: "12 rows of desired_profile_override name profile 7, which no longer exists.",
	}}
	if got := health.danglingReferences(t.Context()); !reflect.DeepEqual(got, want) {
		t.Fatalf("checks = %+v, want %+v", got, want)
	}
	if none := (SystemHealth{}).danglingReferences(t.Context()); len(none) != 0 {
		t.Fatalf("checks = %+v, want none", none)
	}
}

func TestSystemHealth_NamesAWatchedDownloadFolderItCannotRead(t *testing.T) {
	health := SystemHealth{
		Folders: func(context.Context) ([]settings.DownloadFolder, error) {
			return []settings.DownloadFolder{
				{ID: 1, Path: "/data/finished", Watch: true},
				{ID: 2, Path: "/data/readable", Watch: true},
				{ID: 3, Path: "/data/unwatched", Watch: false},
			}, nil
		},
		ReadDir: func(path string) error {
			if path == "/data/readable" {
				return nil
			}
			return errors.New("no such file or directory")
		},
	}
	want := []apiserver.HealthCheck{{
		ID: "download_folder_unreadable_1", Severity: apiserver.HealthError,
		Message: "Solarr cannot read /data/finished, where finished downloads are expected: mount it into the container or set the remote and local path of the download client.",
		Link:    "/storage/downloads",
	}}
	if got := health.unreadableFinishedFolders(t.Context()); !reflect.DeepEqual(got, want) {
		t.Fatalf("checks = %+v, want %+v", got, want)
	}
}
