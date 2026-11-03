package adapters

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/db/library"
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
	if len(checks) != 1 || !strings.HasPrefix(checks[0].Message, "2 downloaded albums are waiting") || checks[0].Link != "/storage/import" {
		t.Fatalf("checks = %+v, want one warning for 2 albums linking to the Import page", checks)
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
	if len(checks) != 1 || checks[0].ID != "provider_paused_tmdb" || checks[0].Link != "/settings/metadata" ||
		!strings.Contains(checks[0].Message, "tmdb is paused until") || !strings.Contains(checks[0].Message, "key was rejected") {
		t.Fatalf("checks = %+v, want only the tmdb warning", checks)
	}
}
