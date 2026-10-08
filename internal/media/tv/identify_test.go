package tv_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/paginate"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

func TestAPersonPlacesAQueuedFileByChoosingTheSeriesAndTheEpisodeTheNameDoesNotSay(t *testing.T) {
	on := false
	consumer, opened, fsys := newConsumer(t, switchSkyhook{on: &on, series: fixtureSkyhook()})
	root := settings.RootFolder{ID: 1, Path: scanRoot, Online: true}
	batch := []scan.Observed{writeVideo(t, fsys, "Weekend Special/the one about the road trip.mkv")}
	if err := consumer.Observe(t.Context(), root, 3, batch); err != nil {
		t.Fatal(err)
	}
	queue := library.NewUnidentified(opened)
	rows, err := queue.List(t.Context(), nil, paginate.Cursor{}, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("queued = %+v, err = %v", rows, err)
	}
	fake := clock.NewFakeAtEpoch()
	fake.Set(addNow)
	service := tv.Service{Skyhook: searchSkyhook{series: fixtureSkyhook()}, TVmaze: fakeTVmaze{err: tvmaze.ErrNotFound}, Series: library.NewSeries(opened), Exclusions: library.NewExclusions(opened), Reader: library.NewSeriesReader(opened), Desired: library.NewDesired(opened), Clock: fake}
	identifier := tv.Identifier{Service: service, Unidentified: queue, Clock: fake, DefaultProfileID: 1, Categories: categories{}}

	if _, err := identifier.Resolve(t.Context(), 3, rows[0], tv.IDs{TVDB: 1234}, 0, nil); !errors.Is(err, tv.ErrNoEpisodes) {
		t.Fatalf("a name with no episode in it: err = %v, want ErrNoEpisodes", err)
	}
	if _, err := identifier.Resolve(t.Context(), 3, rows[0], tv.IDs{TVDB: 1234}, 1, []int{99}); !errors.Is(err, tv.ErrEpisodeUnknown) {
		t.Fatalf("an episode the series lacks: err = %v, want ErrEpisodeUnknown", err)
	}
	workID, err := identifier.Resolve(t.Context(), 3, rows[0], tv.IDs{TVDB: 1234}, 1, []int{2})
	if err != nil || workID == 0 {
		t.Fatalf("workID = %d, err = %v", workID, err)
	}
	detail, err := library.NewSeriesReader(opened).Get(t.Context(), 3, workID, addNow)
	if err != nil {
		t.Fatal(err)
	}
	present := map[int]bool{}
	for _, episode := range detail.Episodes {
		if episode.Present {
			present[episode.Number] = true
		}
	}
	if len(present) != 1 || !present[2] {
		t.Errorf("present = %v, want only episode 2 of season 1", present)
	}
	left, err := librarydb.New(opened.Read()).CountUnidentified(t.Context(), librarydb.CountUnidentifiedParams{FilterRootFolders: 0, RootFolderIds: "[]"})
	if err != nil || left != 0 {
		t.Errorf("queued = %d, err = %v; the file leaves the queue", left, err)
	}
}
