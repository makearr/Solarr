package housekeeping_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/housekeeping"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

type deleteEmpty bool

func (d deleteEmpty) Category(int64) (settings.Category, bool) {
	return settings.Category{FileManagement: settings.FileManagement{DeleteEmptyFolders: bool(d)}}, true
}

// A movie or season folder emptied outside Solarr goes with its emptied parents; the root and anything holding a file stay.
func TestSweepDeletesFoldersEmptiedOutsideSolarrOnlyWhenTheLibraryAsks(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, folder := range []string{"/movies/Gone (2002)", "/movies/Kept (2001)", "/tv/Show/Season 01"} {
		if err := fsys.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	file, err := fsys.Create("/movies/Kept (2001)/Kept (2001).mkv")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	present := func(paths ...string) []string {
		var out []string
		for _, path := range paths {
			if _, err := fsys.Stat(path); err == nil {
				out = append(out, path)
			}
		}
		return out
	}
	all := []string{"/movies", "/movies/Gone (2002)", "/movies/Kept (2001)", "/tv", "/tv/Show", "/tv/Show/Season 01"}
	movies := scan.SweepOutcome{EmptyDirectories: []string{"Gone (2002)", "Kept (2001)"}}
	tv := scan.SweepOutcome{EmptyDirectories: []string{"Show/Season 01"}}

	off := housekeeping.Sweep{Categories: deleteEmpty(false), FS: fsys}
	_ = off.AfterSweep(t.Context(), settings.RootFolder{ID: 1, Path: "/movies"}, 5, movies)
	if got := present(all...); !slices.Equal(got, all) {
		t.Fatalf("with the setting off, present = %v, want %v", got, all)
	}

	on := housekeeping.Sweep{Categories: deleteEmpty(true), FS: fsys}
	if err := on.AfterSweep(t.Context(), settings.RootFolder{ID: 1, Path: "/movies"}, 5, movies); err != nil {
		t.Fatal(err)
	}
	if err := on.AfterSweep(t.Context(), settings.RootFolder{ID: 2, Path: "/tv"}, 6, tv); err != nil {
		t.Fatal(err)
	}
	if got, want := present(all...), []string{"/movies", "/movies/Kept (2001)", "/tv"}; !slices.Equal(got, want) {
		t.Errorf("with the setting on, present = %v, want %v", got, want)
	}
}
