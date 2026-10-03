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
	if none := (SystemHealth{Acquisitions: fakeActive{{State: poll.StateImporting}}}).waitingForReview(t.Context()); len(none) != 0 {
		t.Errorf("checks = %+v, want none", none)
	}
}

func TestTVKeyCheck_AsksForTheKeyOnlyWhereATVLibraryLacksIt(t *testing.T) {
	if checks := tvKeyCheck(true, false); len(checks) != 1 || checks[0].ID != "tv_no_tmdb_key" || checks[0].Link != "/settings/metadata" {
		t.Errorf("checks = %+v, want one warning linking to Metadata", checks)
	}
	for _, quiet := range [][2]bool{{false, false}, {true, true}, {false, true}} {
		if checks := tvKeyCheck(quiet[0], quiet[1]); len(checks) != 0 {
			t.Errorf("tvKeyCheck(%v, %v) = %+v, want none", quiet[0], quiet[1], checks)
		}
	}
}
