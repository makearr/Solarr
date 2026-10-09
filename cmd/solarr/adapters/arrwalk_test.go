package adapters

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
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
	source := arrSource[walked]{
		connect: func(context.Context) error { return nil },
		list:    func(context.Context) ([]walked, error) { return items, nil },
		describe: func(item walked) arrItem {
			return arrItem{Name: item.name, RootFolder: item.root, Monitored: item.monitored}
		},
		workers: 3,
		sync: func(_ context.Context, categoryID int64, item walked) (library.ArrImportCounts, error) {
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
	progress := NewArrProgress()
	walk := arrWalk{
		importID: 9, progress: progress,
		options: apiserver.ArrSyncOptions{Monitored: true, PathRewrites: []apiserver.PathRewrite{{From: "/arr/music", To: "/music"}}},
		place: func(root string) (int64, bool) {
			if strings.HasPrefix(root, "/music/") {
				return 4, true
			}
			return 0, false
		},
	}

	counts, err := walkArr(t.Context(), walk, source)
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
	if figure := progress.runs[9]; figure != [2]int64{4, 4} {
		t.Errorf("progress = %v, want 4 of 4", figure)
	}

	source.connect = func(context.Context) error { return errors.New("refused") }
	if counts, err := walkArr(t.Context(), walk, source); counts != (library.ArrImportCounts{}) || err == nil || err.Error() != "connect: refused" {
		t.Errorf("unreachable *Arr: %+v, %v; want nothing and the connect error", counts, err)
	}
}
