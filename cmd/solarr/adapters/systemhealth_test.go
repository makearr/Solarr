package adapters

import (
	"context"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/poll"
	"github.com/makearr/Solarr/internal/db/library"
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
