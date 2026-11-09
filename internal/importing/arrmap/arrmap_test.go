package arrmap

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

type walked struct {
	name, root string
	monitored  bool
}

// The shared walk syncs what the options want in the library its root folder names, keeps going past a failure,
// adds up every item's counts and reports each failure by name.
func TestWalkArr_SyncsWhatTheOptionsWantAndKeepsGoing(t *testing.T) {
	items := []walked{
		{"Alpha", "/arr/music/Alpha", true},
		{"Bravo", "/arr/music/Bravo", false},
		{"Charlie", "/elsewhere/Charlie", true},
		{"Delta", "/arr/music/Delta", true},
		{"Echo", "/arr/music/Echo", true},
	}
	var mu sync.Mutex
	var synced []string
	source := Source[walked]{
		Connect: func(context.Context) error { return nil },
		List:    func(context.Context) ([]walked, error) { return items, nil },
		Describe: func(item walked) Item {
			return Item{Name: item.name, RootFolder: item.root, Monitored: item.monitored}
		},
		Workers: 3,
		Sync: func(_ context.Context, categoryID int64, item walked) (library.ArrImportCounts, error) {
			mu.Lock()
			synced = append(synced, item.name)
			mu.Unlock()
			if categoryID != 4 {
				t.Errorf("%s synced into library %d, want 4", item.name, categoryID)
			}
			if item.name == "Delta" {
				return library.ArrImportCounts{ArtistsMatched: 1, AlbumsCreated: 1}, errors.New("one album failed")
			}
			return library.ArrImportCounts{ArtistsCreated: 1, AlbumsCreated: 2}, nil
		},
	}
	var figure [2]int64
	walk := Run{
		Options:  Options{Monitored: true},
		Progress: func(done, total int64) { figure = [2]int64{done, total} },
		Place: func(root string) (int64, bool) {
			if strings.HasPrefix(root, "/arr/music/") {
				return 4, true
			}
			return 0, false
		},
	}

	counts, err := Walk(t.Context(), walk, source)
	want := library.ArrImportCounts{ArtistsCreated: 2, ArtistsMatched: 1, AlbumsCreated: 5}
	if counts != want {
		t.Errorf("counts = %+v, want %+v", counts, want)
	}
	slices.Sort(synced)
	if !slices.Equal(synced, []string{"Alpha", "Delta", "Echo"}) {
		t.Errorf("synced %v, want the monitored ones under a library's root", synced)
	}
	if err == nil || !strings.Contains(err.Error(), `Charlie: no category matches root folder "/elsewhere/Charlie"`) ||
		!strings.Contains(err.Error(), "Delta: one album failed") {
		t.Errorf("error = %v, want Charlie's and Delta's failures by name", err)
	}
	if figure != [2]int64{4, 4} {
		t.Errorf("progress = %v, want 4 of 4", figure)
	}

	source.Connect = func(context.Context) error { return errors.New("refused") }
	if counts, err := Walk(t.Context(), walk, source); counts != (library.ArrImportCounts{}) || err == nil || err.Error() != "connect: refused" {
		t.Errorf("unreachable *Arr: %+v, %v; want nothing and the connect error", counts, err)
	}
}

// A monitoring-only walk takes every item whatever its flag, needs no library for its root folder, and never syncs.
func TestWalkArr_MonitoringOnlyTakesEveryItemAndAddsNothing(t *testing.T) {
	items := []walked{{"Alpha", "/nowhere/Alpha", true}, {"Bravo", "/nowhere/Bravo", false}}
	var mu sync.Mutex
	var monitored []string
	source := Source[walked]{
		Connect: func(context.Context) error { return nil },
		List:    func(context.Context) ([]walked, error) { return items, nil },
		Describe: func(item walked) Item {
			return Item{Name: item.name, RootFolder: item.root, Monitored: item.monitored}
		},
		Workers: 1,
		Sync: func(context.Context, int64, walked) (library.ArrImportCounts, error) {
			t.Error("a monitoring-only run added something")
			return library.ArrImportCounts{}, nil
		},
		Monitor: func(_ context.Context, _ int64, item walked) (library.ArrImportCounts, error) {
			mu.Lock()
			monitored = append(monitored, item.name)
			mu.Unlock()
			if item.name == "Bravo" {
				return Skipped, nil
			}
			return library.ArrImportCounts{ArtistsMatched: 1}, nil
		},
	}
	walk := Run{Options: Options{MonitoringOnly: true}, Progress: func(int64, int64) {}, Place: func(string) (int64, bool) { return 0, false }}
	counts, err := Walk(t.Context(), walk, source)
	if err != nil || counts != (library.ArrImportCounts{ArtistsMatched: 1, ItemsParked: 1}) {
		t.Errorf("counts = %+v, err = %v; want one matched and one skipped", counts, err)
	}
	slices.Sort(monitored)
	if !slices.Equal(monitored, []string{"Alpha", "Bravo"}) {
		t.Errorf("monitored %v, want both, whatever their flag", monitored)
	}
	source.Monitor = nil
	if _, err := Walk(t.Context(), walk, source); err == nil {
		t.Error("an app without a monitoring-only import was walked")
	}
}

// A limit tries an import on the first few chosen items; what the options leave out does not use it up.
func TestWalk_LimitCountsOnlyChosenItems(t *testing.T) {
	items := []walked{{"Alpha", "/a", false}, {"Bravo", "/b", true}, {"Charlie", "/c", true}, {"Delta", "/d", true}}
	var synced, prepared []string
	source := Source[walked]{
		Connect: func(context.Context) error { return nil },
		List:    func(context.Context) ([]walked, error) { return items, nil },
		Describe: func(item walked) Item {
			return Item{Name: item.name, RootFolder: item.root, Monitored: item.monitored}
		},
		Prepare: func(_ context.Context, chosen []walked) {
			for _, item := range chosen {
				prepared = append(prepared, item.name)
			}
		},
		Sync: func(_ context.Context, _ int64, item walked) (library.ArrImportCounts, error) {
			synced = append(synced, item.name)
			return library.ArrImportCounts{}, nil
		},
	}
	run := Run{Options: Options{Monitored: true, Limit: 2}, Progress: func(int64, int64) {}, Place: func(string) (int64, bool) { return 1, true }}
	if _, err := Walk(t.Context(), run, source); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(synced, []string{"Bravo", "Charlie"}) {
		t.Errorf("synced %v, want the first two monitored", synced)
	}
	if !slices.Equal(prepared, []string{"Bravo", "Charlie"}) {
		t.Errorf("prepared %v, want only what the limit keeps", prepared)
	}
}

// Present chooses what the *Arr holds files for whatever its monitored flag, beside the monitored and unmonitored choices.
func TestWalk_PresentChoosesItemsWithFilesWhateverTheirFlag(t *testing.T) {
	type entry struct {
		name               string
		monitored, hasFile bool
	}
	items := []entry{{"Kept", false, true}, {"Wanted", true, false}, {"Empty", false, false}}
	var synced []string
	source := Source[entry]{
		Connect: func(context.Context) error { return nil },
		List:    func(context.Context) ([]entry, error) { return items, nil },
		Describe: func(item entry) Item {
			return Item{Name: item.name, RootFolder: "/a", Monitored: item.monitored, HasFiles: item.hasFile}
		},
		Workers: 1,
		Sync: func(_ context.Context, _ int64, item entry) (library.ArrImportCounts, error) {
			synced = append(synced, item.name)
			return library.ArrImportCounts{}, nil
		},
	}
	run := Run{
		Options:  Options{Present: true},
		Progress: func(int64, int64) {},
		Place:    func(string) (int64, bool) { return 1, true },
		Record:   func(context.Context, library.NewArrImportItem) error { return nil },
	}
	if _, err := Walk(context.Background(), run, source); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Kept"}; !slices.Equal(synced, want) {
		t.Fatalf("present only synced %v, want %v", synced, want)
	}
}
