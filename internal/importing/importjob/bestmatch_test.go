package importjob_test

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

// fittingImporter fits a unit by its files' extensions, and holds what it is handed as resolved but waiting to place.
type fittingImporter struct {
	fits   map[string]float64
	floor  int64
	workID int64
	handed []handedUnit
	held   []batch.HeldRequest
}

type handedUnit struct {
	directory  string
	categoryID int64
	files      int
}

func (f *fittingImporter) Fit(unit inspect.Unit) float64 {
	var fit float64
	for _, file := range unit.Media {
		fit = f.fits[path.Ext(file.RelPath)]
		if fit == 0 {
			return 0
		}
	}
	return fit
}

func (f *fittingImporter) MediaSizeFloorBytes() int64 { return f.floor }

func (f *fittingImporter) ImportBatch(_ context.Context, request batch.Request) (batch.Result, error) {
	result := batch.Result{Placements: make([]*library.Placement, len(request.Units))}
	for index, unit := range request.Units {
		f.handed = append(f.handed, handedUnit{directory: unit.Directory, categoryID: request.CategoryID, files: len(unit.Media)})
		result.Decisions = append(result.Decisions,
			batch.WaitToPlace(library.UnitDecision{UnitID: request.UnitIDs[index]}, f.workID, errors.New("no root yet")))
	}
	return result, nil
}

func (f *fittingImporter) CommitHeld(_ context.Context, request batch.HeldRequest) (library.Placement, error) {
	f.held = append(f.held, request)
	return library.Placement{WorkID: f.workID}, nil
}

// routedUnit is what a session recorded of one unit, for comparing whole.
type routedUnit struct {
	categoryID      int64
	files           int64
	decision, cause string
}

func routedUnits(t *testing.T, fx *fixture, sessionID int64) map[string]routedUnit {
	t.Helper()
	units, err := fx.sessions().ListUnits(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]routedUnit{}
	for _, unit := range units {
		row := routedUnit{files: unit.FileCount, decision: unit.Decision, cause: unit.Reason}
		if unit.CategoryID != nil {
			row.categoryID = *unit.CategoryID
		}
		got[unit.RelPath] = row
	}
	return got
}

// bestMatchFixture is an album, an episode beside a sample too small for TV, a video no type takes and a file two types
// fit equally, imported with Best match into a music library and three TV libraries.
func bestMatchFixture(t *testing.T) (*fixture, *fittingImporter, *fittingImporter, int64) {
	t.Helper()
	fx := newFixture(t, "Album/01.flac", "Album/02.flac", "Clip/clip.avi", "Show/sample.mkv")
	writeFile(t, fx.fsys, filepath.Join(root, "Show/Show.S01E01.mkv"), 400*1024)
	writeFile(t, fx.fsys, filepath.Join(root, "Odd/a.ogg"), 400*1024)
	workID := seedWork(t, fx)
	music := &fittingImporter{fits: map[string]float64{".flac": 0.6, ".ogg": 0.6}, workID: workID}
	tv := &fittingImporter{fits: map[string]float64{".mkv": 0.9, ".ogg": 0.6}, floor: 300 * 1024, workID: workID}
	job := importjob.NewJob(importjob.Config{
		FS: fx.fsys, Sessions: fx.sessions(), Policy: fixedPolicy(0.9), Packs: library.NewPacks(fx.store),
		Importers: map[string]batch.Importer{"music": music, "tv": tv}, Unassigned: "music",
		Libraries: func() []importjob.Library {
			return []importjob.Library{
				{CategoryID: 1, MediaType: "music", General: true, Rank: 5},
				{CategoryID: 2, MediaType: "tv", General: true, Rank: 5},
				{CategoryID: 3, MediaType: "tv", Rank: 0},
				{CategoryID: 4, MediaType: "tv", General: true, Rank: 1},
			}
		},
		Clock: fx.clock, Logger: slog.New(slog.DiscardHandler),
	})
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual, BestMatch: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	done, err := fx.sessions().List(t.Context(), library.ImportDone, false)
	if err != nil || len(done) != 1 {
		t.Fatalf("sessions = %+v, %v", done, err)
	}
	return fx, music, tv, done[0].ID
}

// Each unit goes to a library of the type its files fit best, with only the files that type takes; the general TV
// library of the highest priority wins over a narrowed one, a tie waits for a library and what no type fits is skipped.
func TestJob_BestMatchRoutesEachUnitToTheLibraryItFits(t *testing.T) {
	fx, music, tv, sessionID := bestMatchFixture(t)

	waiting := "no root yet"
	want := map[string]routedUnit{
		"Album": {categoryID: 1, files: 2, decision: library.UnitCommit, cause: waiting},
		"Clip":  {files: 1, decision: library.UnitSkip, cause: batch.ReasonNoLibraryFits},
		"Odd":   {files: 1, decision: library.UnitCommit, cause: waiting},
		"Show":  {categoryID: 4, files: 1, decision: library.UnitCommit, cause: waiting},
	}
	if got := routedUnits(t, fx, sessionID); !reflect.DeepEqual(got, want) {
		t.Errorf("units = %+v\nwant    %+v", got, want)
	}
	if want := []handedUnit{{"Album", 1, 2}, {"Odd", 0, 1}}; !reflect.DeepEqual(music.handed, want) {
		t.Errorf("music was handed %+v, want %+v", music.handed, want)
	}
	if want := []handedUnit{{"Show", 4, 1}}; !reflect.DeepEqual(tv.handed, want) {
		t.Errorf("tv was handed %+v, want %+v", tv.handed, want)
	}
}

// What a Best match session could not place yet is retried into each unit's own library, and a unit with none waits.
func TestManualCommitter_PlaceUnplacedPlacesEachRoutedUnitIntoItsOwnLibrary(t *testing.T) {
	fx, music, tv, sessionID := bestMatchFixture(t)
	sessions := fx.sessions()
	retried, err := sessions.SessionsWithUnplacedCommits(t.Context(), 0, 10)
	if err != nil || !reflect.DeepEqual(retried, []int64{sessionID}) {
		t.Fatalf("sessions to retry = %v, %v; want the Best match session although it has no library", retried, err)
	}

	manual := importjob.NewManualCommitter(fx.fsys, sessions, library.NewPacks(fx.store), func(categoryID int64) batch.Importer {
		if categoryID == 1 {
			return music
		}
		return tv
	})
	placed, errs := manual.PlaceUnplaced(t.Context(), sessionID)
	if placed != 2 || len(errs) != 0 {
		t.Fatalf("PlaceUnplaced = %d, %v; want the album and the episode placed", placed, errs)
	}
	got := map[string]int64{}
	for _, request := range append(append([]batch.HeldRequest(nil), music.held...), tv.held...) {
		got[request.Unit.Directory] = request.CategoryID
	}
	if want := map[string]int64{"Album": 1, "Show": 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("placed into %v, want %v", got, want)
	}
}
