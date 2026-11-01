package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

func TestRewritePath_TheFirstMatchingPrefixApplies(t *testing.T) {
	rewrites := []apiserver.PathRewrite{{From: "/media/music/", To: "/mnt/media/music"}, {From: "/media", To: "/other"}}
	for path, want := range map[string]string{
		"/media/music/Katupoken": "/mnt/media/music/Katupoken",
		"/media/music":           "/mnt/media/music",
		"/media/musicals/Heka":   "/other/musicals/Heka",
		"/elsewhere/Heka":        "/elsewhere/Heka",
	} {
		if got := rewritePath(path, rewrites); got != want {
			t.Errorf("rewritePath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestArrProgress_OnlyARunningImportCarriesTheLiveFigure(t *testing.T) {
	board := NewArrProgress()
	board.set(7, 3, 10)
	running := board.annotate(apiserver.ArrImportRun{ID: 7, State: library.ArrImportRunning})
	if running.ArtistsDone != 3 || running.ArtistsTotal != 10 {
		t.Errorf("running = %+v, want 3 of 10", running)
	}
	if done := board.annotate(apiserver.ArrImportRun{ID: 7, State: library.ArrImportDone}); done.ArtistsTotal != 0 {
		t.Errorf("a finished run carries %+v", done)
	}
	board.clear(7)
	if gone := board.annotate(apiserver.ArrImportRun{ID: 7, State: library.ArrImportRunning}); gone.ArtistsTotal != 0 {
		t.Errorf("a cleared run carries %+v", gone)
	}
	var none *ArrProgress
	none.set(1, 1, 1)
	if got := none.annotate(apiserver.ArrImportRun{ID: 1, State: library.ArrImportRunning}); got.ArtistsTotal != 0 {
		t.Errorf("a nil board carries %+v", got)
	}
}
