package movie_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/housekeeping"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

type fileManagement struct{ unmonitor bool }

func (f fileManagement) Category(int64) (settings.Category, bool) {
	return settings.Category{FileManagement: settings.FileManagement{UnmonitorDeleted: f.unmonitor}}, true
}

func TestHousekeepingUnmonitorsOnlyTheMovieWhoseFileIsGone(t *testing.T) {
	service, opened := newService(t, byTitle{movies: []tmdb.Movie{simpleMovie(1, "Kept", "2001-01-01"), simpleMovie(2, "Deleted", "2002-02-02")}}, nil)
	desired := library.NewDesired(opened)
	physical := library.NewPhysical(opened)
	files := []library.PhysicalFile{
		{RelPath: "Kept (2001)/Kept (2001).mkv", SizeBytes: 1, QuickHash: "kept", FirstSeenAt: addNow},
		{RelPath: "Deleted (2002)/Deleted (2002).mkv", SizeBytes: 1, QuickHash: "deleted", FirstSeenAt: addNow},
	}
	registered, err := physical.RegisterBatch(t.Context(), 9, files)
	if err != nil {
		t.Fatal(err)
	}
	var items []int64
	for index, id := range []int64{1, 2} {
		added, err := service.Add(t.Context(), movie.AddRequest{IDs: movie.IDs{TMDB: id}, CategoryID: 5, ProfileID: 1, Monitor: movie.MonitorMovieOnly})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := library.NewSeries(opened).AttachWholeFiles(t.Context(), addNow, added.WorkID, 9, []int64{registered[index].MediaObjectID}); err != nil {
			t.Fatal(err)
		}
		items = append(items, added.ItemID)
	}
	files[0].FirstSeenAt = addNow.Add(2 * time.Hour)
	if _, err := physical.RegisterBatch(t.Context(), 9, files[:1]); err != nil {
		t.Fatal(err)
	}
	gone := addNow.Add(time.Hour)
	if marked, err := physical.MarkMissing(t.Context(), 9, gone); err != nil || marked != 1 {
		t.Fatalf("marked %d missing, %v; want the second file", marked, err)
	}

	sweep := scan.SweepOutcome{MissingSince: gone}
	grain := movie.Module{}.Descriptor().WantedGrain
	off := housekeeping.Sweep{Categories: fileManagement{false}, Desired: desired, Grain: grain}
	if err := off.AfterSweep(t.Context(), settings.RootFolder{ID: 9}, 5, sweep); err != nil {
		t.Fatal(err)
	}
	if kept, _ := desired.Get(t.Context(), items[1]); kept.Monitored != 1 {
		t.Fatalf("with the setting off the deleted movie stays monitored: %+v", kept)
	}
	on := housekeeping.Sweep{Categories: fileManagement{true}, Desired: desired, Grain: grain}
	if err := on.AfterSweep(t.Context(), settings.RootFolder{ID: 9}, 5, sweep); err != nil {
		t.Fatal(err)
	}
	keptItem, _ := desired.Get(t.Context(), items[0])
	deletedItem, _ := desired.Get(t.Context(), items[1])
	if keptItem.Monitored != 1 || deletedItem.Monitored != 0 {
		t.Errorf("monitored: kept %d, deleted %d; want 1 and 0", keptItem.Monitored, deletedItem.Monitored)
	}
}
