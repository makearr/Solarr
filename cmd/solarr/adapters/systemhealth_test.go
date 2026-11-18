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
